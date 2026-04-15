//! Benchmark for directory-tree copy performance (mirrors `copy_tree_files` logic).
//!
//! Tests with 10, 100, and 1000 files to measure scaling behaviour.

use std::fs;
use std::io;
use std::path::Path;

use divan::Bencher;

fn main() {
    divan::main();
}

/// Recursive directory copy -- same algorithm as `cache::tree::copy_tree_files`
/// but without the agentpack error wrapper so the benchmark has no internal deps.
fn copy_tree(src: &Path, dst: &Path) -> io::Result<()> {
    let meta = fs::symlink_metadata(src)?;

    if meta.file_type().is_symlink() {
        match fs::canonicalize(src) {
            Ok(resolved) => return copy_tree(&resolved, dst),
            Err(_) => return Ok(()), // dangling symlink -- skip
        }
    }

    if meta.is_file() {
        if let Some(parent) = dst.parent() {
            fs::create_dir_all(parent)?;
        }
        fs::copy(src, dst)?;
        return Ok(());
    }

    if meta.is_dir() {
        fs::create_dir_all(dst)?;
        for entry in fs::read_dir(src)? {
            let entry = entry?;
            copy_tree(&entry.path(), &dst.join(entry.file_name()))?;
        }
    }

    Ok(())
}

/// Build a source tree with `n` files spread across a nested directory structure.
fn build_source_tree(n: usize) -> tempfile::TempDir {
    let dir = tempfile::tempdir().expect("failed to create temp dir");
    let root = dir.path();

    // Distribute files across subdirectories to simulate realistic plugin/skill layouts.
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

    for i in 0..n {
        let subdir = subdirs[i % subdirs.len()];
        let file_path = root.join(subdir).join(format!("file_{i}.md"));
        // Realistic file size: ~300 bytes, typical for a small skill/command markdown file.
        let content = format!(
            "---\nname: item-{i}\ndescription: Test file {i}\n---\n\n# Item {i}\n\n\
             Content for benchmarking copy_tree_files.\n\n\
             ## Details\n\n- Point one\n- Point two\n- Point three\n"
        );
        fs::write(&file_path, content).unwrap();
    }

    dir
}

#[divan::bench(args = [10, 100, 1000])]
fn copy_tree_n_files(bencher: Bencher, n: usize) {
    // Setup: build the source tree ONCE, outside the timed region.
    let src_dir = build_source_tree(n);

    bencher.bench_local(|| {
        let dst_dir = tempfile::tempdir().unwrap();
        copy_tree(src_dir.path(), dst_dir.path()).unwrap();
        // dst_dir is returned so drop (cleanup) happens outside black_box scope
        dst_dir
    });
}

/// Reflink-or-copy walker: same traversal shape as `copy_tree`, but leaf copies use
/// `reflink_copy::reflink_or_copy` which maps to APFS `clonefile` on macOS, btrfs/XFS
/// `FICLONE` on Linux, and ReFS on Windows. Falls back to `fs::copy` cross-fs.
fn copy_tree_reflink(src: &Path, dst: &Path) -> io::Result<()> {
    let meta = fs::symlink_metadata(src)?;
    if meta.file_type().is_symlink() {
        return Ok(());
    }
    if meta.is_file() {
        if let Some(parent) = dst.parent() {
            fs::create_dir_all(parent)?;
        }
        // reflink_or_copy requires dst not to exist.
        if dst.exists() {
            fs::remove_file(dst)?;
        }
        reflink_copy::reflink_or_copy(src, dst)?;
        return Ok(());
    }
    if meta.is_dir() {
        fs::create_dir_all(dst)?;
        for entry in fs::read_dir(src)? {
            let entry = entry?;
            let ty = entry.file_type()?;
            let child_src = entry.path();
            let child_dst = dst.join(entry.file_name());
            if ty.is_dir() {
                copy_tree_reflink(&child_src, &child_dst)?;
            } else if ty.is_file() {
                if let Some(parent) = child_dst.parent() {
                    fs::create_dir_all(parent)?;
                }
                reflink_copy::reflink_or_copy(&child_src, &child_dst)?;
            }
        }
    }
    Ok(())
}

#[divan::bench(args = [10, 100, 1000])]
fn copy_tree_reflink_n_files(bencher: Bencher, n: usize) {
    let src_dir = build_source_tree(n);
    bencher.bench_local(|| {
        let dst_dir = tempfile::tempdir().unwrap();
        copy_tree_reflink(src_dir.path(), dst_dir.path()).unwrap();
        dst_dir
    });
}

/// Models the OLD behavior where four harness targets each walked the source tree
/// independently (e.g. `copy_raw_plugin_support_dirs` before the fold). Four full copies.
#[divan::bench(args = [100, 1000])]
fn copy_tree_4harness_sequential_bytecopy(bencher: Bencher, n: usize) {
    let src_dir = build_source_tree(n);
    bencher.bench_local(|| {
        let dst = tempfile::tempdir().unwrap();
        for i in 0..4 {
            copy_tree(src_dir.path(), &dst.path().join(format!("h{i}"))).unwrap();
        }
        dst
    });
}

/// Models the NEW behavior: single walk, fan-out to four destinations per file,
/// leaf copies via `reflink_or_copy`.
#[divan::bench(args = [100, 1000])]
fn copy_tree_4harness_single_walk_reflink(bencher: Bencher, n: usize) {
    let src_dir = build_source_tree(n);
    bencher.bench_local(|| {
        let dst = tempfile::tempdir().unwrap();
        let roots: Vec<_> = (0..4).map(|i| dst.path().join(format!("h{i}"))).collect();
        for entry in walkdir::WalkDir::new(src_dir.path()) {
            let entry = entry.unwrap();
            if !entry.file_type().is_file() {
                continue;
            }
            let rel = entry.path().strip_prefix(src_dir.path()).unwrap();
            for root in &roots {
                let target = root.join(rel);
                if let Some(parent) = target.parent() {
                    fs::create_dir_all(parent).unwrap();
                }
                reflink_copy::reflink_or_copy(entry.path(), &target).unwrap();
            }
        }
        dst
    });
}
