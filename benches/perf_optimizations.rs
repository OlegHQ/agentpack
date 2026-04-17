//! Benchmarks for the performance-critical operations optimized in the refactoring:
//!
//! 1. **hash_directory_contents** — streaming 8KB-chunk hashing vs old full-file-read approach
//! 2. **hash_and_copy** — single-pass hash+copy vs sequential hash-then-copy
//! 3. **tar path parsing** — split_once vs Vec<&str> collect for archive entry paths
//! 4. **PackLock save** — direct sort+serialize vs old clone+sync_views round-trip
//! 5. **PackLock filter iterators** — skills()/plugins() access patterns at scale
//! 6. **truncate_str** — optimized byte-slicing vs old chars().take().collect()

use std::fs;
use std::io::{Read, Write};
use std::path::Path;

use divan::Bencher;
use sha2::{Digest, Sha256};

fn main() {
    divan::main();
}

// ---------------------------------------------------------------------------
// Helpers: build realistic test trees
// ---------------------------------------------------------------------------

fn build_tree_with_file_sizes(n: usize, file_size: usize) -> tempfile::TempDir {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let subdirs = [
        "commands",
        "skills/analysis",
        "hooks",
        "agents",
        "rules/project",
    ];
    for sub in &subdirs {
        fs::create_dir_all(root.join(sub)).unwrap();
    }
    // Content of the target size (repeating pattern)
    let pattern = "# Benchmark content\n\nLorem ipsum dolor sit amet. ";
    let content: String = pattern.chars().cycle().take(file_size).collect();
    for i in 0..n {
        let subdir = subdirs[i % subdirs.len()];
        fs::write(root.join(subdir).join(format!("f_{i}.md")), &content).unwrap();
    }
    dir
}

// ---------------------------------------------------------------------------
// 1. Hashing: streaming vs full-read
// ---------------------------------------------------------------------------

/// OLD approach: read entire file into Vec<u8>, then hash
fn hash_directory_full_read(root: &Path, files: &[std::path::PathBuf]) -> String {
    let mut hash = Sha256::new();
    for rel in files {
        hash.update(rel.as_os_str().as_encoded_bytes());
        hash.update([0]);
        let bytes = fs::read(root.join(rel)).unwrap();
        hash.update(&bytes);
    }
    let full = hex::encode(hash.finalize());
    full[..40].to_string()
}

/// NEW approach: stream file contents in 8KB chunks
fn hash_directory_streaming(root: &Path, files: &[std::path::PathBuf]) -> String {
    let mut hash = Sha256::new();
    let mut buf = [0u8; 8192];
    for rel in files {
        hash.update(rel.as_os_str().as_encoded_bytes());
        hash.update([0]);
        let path = root.join(rel);
        let file = fs::File::open(&path).unwrap();
        let mut reader = std::io::BufReader::new(file);
        loop {
            let n = reader.read(&mut buf).unwrap();
            if n == 0 {
                break;
            }
            hash.update(&buf[..n]);
        }
    }
    let full = hex::encode(hash.finalize());
    full[..40].to_string()
}

fn collect_files_sorted(root: &Path) -> Vec<std::path::PathBuf> {
    let mut files = Vec::new();
    let walker = ignore::WalkBuilder::new(root)
        .hidden(false)
        .git_ignore(true)
        .git_global(false)
        .git_exclude(false)
        .parents(false)
        .follow_links(false)
        .build();
    for entry in walker {
        let entry = entry.unwrap();
        if entry.file_type().is_some_and(|ft| ft.is_file()) {
            let rel = entry.path().strip_prefix(root).unwrap();
            files.push(rel.to_path_buf());
        }
    }
    files.sort();
    files
}

#[divan::bench(args = [10, 100, 500])]
fn hash_full_read(bencher: Bencher, n: usize) {
    let dir = build_tree_with_file_sizes(n, 2048);
    let files = collect_files_sorted(dir.path());
    bencher.bench_local(|| hash_directory_full_read(dir.path(), &files));
}

#[divan::bench(args = [10, 100, 500])]
fn hash_streaming(bencher: Bencher, n: usize) {
    let dir = build_tree_with_file_sizes(n, 2048);
    let files = collect_files_sorted(dir.path());
    bencher.bench_local(|| hash_directory_streaming(dir.path(), &files));
}

// ---------------------------------------------------------------------------
// 2. Hash+copy: single-pass vs double-pass
// ---------------------------------------------------------------------------

/// OLD: hash all files, then copy all files (two complete passes)
fn hash_then_copy(src: &Path, dst: &Path, files: &[std::path::PathBuf]) -> String {
    // Pass 1: hash
    let hash = hash_directory_full_read(src, files);
    // Pass 2: copy
    for rel in files {
        let src_file = src.join(rel);
        let dst_file = dst.join(rel);
        if let Some(parent) = dst_file.parent() {
            fs::create_dir_all(parent).unwrap();
        }
        fs::copy(&src_file, &dst_file).unwrap();
    }
    hash
}

/// NEW: hash and copy simultaneously in one read pass
fn hash_and_copy_single_pass(src: &Path, dst: &Path, files: &[std::path::PathBuf]) -> String {
    let mut hash = Sha256::new();
    let mut buf = [0u8; 8192];
    for rel in files {
        hash.update(rel.as_os_str().as_encoded_bytes());
        hash.update([0]);
        let src_file = src.join(rel);
        let dst_file = dst.join(rel);
        if let Some(parent) = dst_file.parent() {
            fs::create_dir_all(parent).unwrap();
        }
        let in_file = fs::File::open(&src_file).unwrap();
        let mut reader = std::io::BufReader::new(in_file);
        let mut writer = std::io::BufWriter::new(fs::File::create(&dst_file).unwrap());
        loop {
            let n = reader.read(&mut buf).unwrap();
            if n == 0 {
                break;
            }
            hash.update(&buf[..n]);
            writer.write_all(&buf[..n]).unwrap();
        }
    }
    let full = hex::encode(hash.finalize());
    full[..40].to_string()
}

#[divan::bench(args = [10, 100, 500])]
fn copy_double_pass(bencher: Bencher, n: usize) {
    let src = build_tree_with_file_sizes(n, 2048);
    let files = collect_files_sorted(src.path());
    bencher.bench_local(|| {
        let dst = tempfile::tempdir().unwrap();
        let h = hash_then_copy(src.path(), dst.path(), &files);
        (dst, h)
    });
}

#[divan::bench(args = [10, 100, 500])]
fn copy_single_pass(bencher: Bencher, n: usize) {
    let src = build_tree_with_file_sizes(n, 2048);
    let files = collect_files_sorted(src.path());
    bencher.bench_local(|| {
        let dst = tempfile::tempdir().unwrap();
        let h = hash_and_copy_single_pass(src.path(), dst.path(), &files);
        (dst, h)
    });
}

// ---------------------------------------------------------------------------
// 3. Tar path parsing: split_once vs Vec collect
// ---------------------------------------------------------------------------

fn make_tar_paths(n: usize) -> Vec<String> {
    (0..n)
        .map(|i| format!("reponame-abc123/plugins/pkg/agents/file_{i}.md"))
        .collect()
}

/// OLD: collect into Vec, slice from [1..]
fn parse_tar_paths_vec(paths: &[String]) -> Vec<String> {
    let mut out = Vec::with_capacity(paths.len());
    for path in paths {
        let components: Vec<&str> = path.split('/').collect();
        if components.len() < 2 {
            continue;
        }
        out.push(components[1..].join("/"));
    }
    out
}

/// NEW: split_once, take remainder
fn parse_tar_paths_split_once(paths: &[String]) -> Vec<String> {
    let mut out = Vec::with_capacity(paths.len());
    for path in paths {
        let Some((_prefix, rest)) = path.split_once('/') else {
            continue;
        };
        out.push(rest.to_string());
    }
    out
}

#[divan::bench(args = [100, 1000, 5000])]
fn tar_parse_vec_collect(bencher: Bencher, n: usize) {
    let paths = make_tar_paths(n);
    bencher.bench_local(|| parse_tar_paths_vec(&paths));
}

#[divan::bench(args = [100, 1000, 5000])]
fn tar_parse_split_once(bencher: Bencher, n: usize) {
    let paths = make_tar_paths(n);
    bencher.bench_local(|| parse_tar_paths_split_once(&paths));
}

// ---------------------------------------------------------------------------
// 4. PackLock save: old clone+sync vs new direct sort
// ---------------------------------------------------------------------------

fn make_lock_packages(n: usize) -> Vec<(String, String, String, String, String, String, bool)> {
    (0..n)
        .map(|i| {
            let module = format!("github.com/owner/repo/pkg_{i}");
            let url = format!("https://github.com/owner/repo/tree/main/pkg_{i}");
            let commit = format!("{:0>40x}", i);
            let cache_key = format!("{:0>64x}", i);
            let is_plugin = i % 3 == 0;
            (
                module,
                url,
                "owner".to_string(),
                "repo".to_string(),
                commit,
                cache_key,
                is_plugin,
            )
        })
        .collect()
}

/// OLD: clone entire struct, sync views, then serialize
fn save_old_approach(
    packages: &[(String, String, String, String, String, String, bool)],
) -> String {
    // Simulate: clone all strings (the old sync_views_from_packages cloned the entire vec)
    let mut cloned: Vec<(String, String)> = packages
        .iter()
        .map(|(m, _, _, _, _, ck, _)| (m.clone(), ck.clone()))
        .collect();
    cloned.sort_by(|a, b| a.0.cmp(&b.0));
    // Simulate: rebuild from views (clone again)
    let mut rebuilt: Vec<(String, String)> = cloned
        .iter()
        .map(|(m, ck)| (m.clone(), ck.clone()))
        .collect();
    rebuilt.sort_by(|a, b| a.0.cmp(&b.0));
    format!("{}", rebuilt.len())
}

/// NEW: sort in place, serialize directly
fn save_new_approach(
    packages: &[(String, String, String, String, String, String, bool)],
) -> String {
    let mut sorted: Vec<&str> = packages
        .iter()
        .map(|(m, _, _, _, _, _, _)| m.as_str())
        .collect();
    sorted.sort();
    format!("{}", sorted.len())
}

#[divan::bench(args = [10, 50, 200])]
fn lock_save_old_clone(bencher: Bencher, n: usize) {
    let packages = make_lock_packages(n);
    bencher.bench_local(|| save_old_approach(&packages));
}

#[divan::bench(args = [10, 50, 200])]
fn lock_save_new_direct(bencher: Bencher, n: usize) {
    let packages = make_lock_packages(n);
    bencher.bench_local(|| save_new_approach(&packages));
}

// ---------------------------------------------------------------------------
// 5. truncate_str: old chars().take() vs new byte-slice
// ---------------------------------------------------------------------------

/// OLD: always iterate chars
fn truncate_old(value: &str, max_chars: usize) -> String {
    value.chars().take(max_chars).collect()
}

/// NEW: byte-slice fast path for ASCII
fn truncate_new(value: &str, max_chars: usize) -> String {
    if value.len() <= max_chars {
        return value.to_string();
    }
    if value.is_char_boundary(max_chars) {
        return value[..max_chars].to_string();
    }
    value.chars().take(max_chars).collect()
}

#[divan::bench]
fn truncate_str_old_short() -> String {
    // Common case: cache_key (64 hex chars) truncated to 12
    truncate_old(
        "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
        12,
    )
}

#[divan::bench]
fn truncate_str_new_short() -> String {
    truncate_new(
        "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
        12,
    )
}

#[divan::bench]
fn truncate_str_old_noop() -> String {
    // No-op case: already short enough
    truncate_old("short", 40)
}

#[divan::bench]
fn truncate_str_new_noop() -> String {
    truncate_new("short", 40)
}

// ---------------------------------------------------------------------------
// 6. PackLock filter: skills()/plugins() iterator vs old derived vecs
// ---------------------------------------------------------------------------

use agentpack::lockfile::{LockPackage, PackLock, PackageKind};

fn make_packlock(n: usize) -> PackLock {
    let mut lock = PackLock::default();
    for i in 0..n {
        lock.packages.push(LockPackage {
            module: format!("github.com/o/r/pkg_{i}"),
            direct: i % 2 == 0,
            kind: if i % 3 == 0 {
                PackageKind::Plugin
            } else {
                PackageKind::Skill
            },
            url: format!("https://github.com/o/r/tree/main/pkg_{i}"),
            owner: "o".into(),
            repo: "r".into(),
            path: format!("pkg_{i}"),
            commit: format!("{:0>40x}", i),
            cache_key: format!("{:0>64x}", i),
            name: String::new(),
        });
    }
    lock
}

#[divan::bench(args = [10, 50, 200])]
fn packlock_skill_count(bencher: Bencher, n: usize) {
    let lock = make_packlock(n);
    bencher.bench_local(|| lock.skill_count());
}

#[divan::bench(args = [10, 50, 200])]
fn packlock_plugin_iter_collect(bencher: Bencher, n: usize) {
    let lock = make_packlock(n);
    bencher.bench_local(|| {
        let v: Vec<&LockPackage> = lock.plugins().collect();
        v
    });
}

#[divan::bench(args = [10, 50, 200])]
fn packlock_skills_iter_collect(bencher: Bencher, n: usize) {
    let lock = make_packlock(n);
    bencher.bench_local(|| {
        let v: Vec<&LockPackage> = lock.skills().collect();
        v
    });
}
