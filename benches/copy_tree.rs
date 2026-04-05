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
