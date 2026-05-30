//! Workspace dev tasks (the single task runner — there is no `scripts/` dir).
//!
//! Usage:
//!   cargo xtask bump-version minor|patch
//!   cargo xtask read-version
//!
//! Releases (binaries + Homebrew formula) are handled by cargo-dist on tag push — see
//! `dist-workspace.toml` and `.github/workflows/release.yml`.

use std::path::{Path, PathBuf};
use std::{env, fs, process};

fn workspace_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .expect("xtask must live one level below workspace root")
        .to_path_buf()
}

fn read_cargo_doc() -> (PathBuf, toml_edit::DocumentMut) {
    let path = workspace_root().join("Cargo.toml");
    let src = fs::read_to_string(&path).expect("read Cargo.toml");
    let doc: toml_edit::DocumentMut = src.parse().expect("parse Cargo.toml");
    (path, doc)
}

// ── bump-version ────────────────────────────────────────────────────────���─────

fn bump_version(kind: &str) {
    let (path, mut doc) = read_cargo_doc();

    let version_item = doc
        .get_mut("package")
        .and_then(|p| p.get_mut("version"))
        .expect("package.version missing");

    let old: semver::Version = version_item
        .as_str()
        .expect("version must be a string")
        .parse()
        .expect("invalid semver");

    let new = match kind {
        "minor" => semver::Version::new(old.major, old.minor + 1, 0),
        "patch" => semver::Version::new(old.major, old.minor, old.patch + 1),
        other => {
            eprintln!("error: expected `minor` or `patch`, got `{other}`");
            process::exit(2);
        }
    };

    *version_item = toml_edit::value(new.to_string());
    fs::write(&path, doc.to_string()).expect("write Cargo.toml");
    println!("{new}");
}

// ── read-version ──────────────────────────────────────────────────────────────

fn read_version() {
    let (_path, doc) = read_cargo_doc();

    let version = doc
        .get("package")
        .and_then(|p| p.get("version"))
        .and_then(|v| v.as_str())
        .expect("package.version missing");

    println!("{version}");
}

// ── main ──────────────────────────────────────────────────────────────────────

fn main() {
    let args: Vec<String> = env::args().collect();
    if args.len() < 2 {
        usage();
    }

    match args[1].as_str() {
        "bump-version" => {
            if args.len() != 3 {
                eprintln!("usage: cargo xtask bump-version minor|patch");
                process::exit(2);
            }
            bump_version(&args[2]);
        }
        "read-version" => {
            read_version();
        }
        other => {
            eprintln!("unknown task: {other}");
            usage();
        }
    }
}

fn usage() -> ! {
    eprintln!(
        "usage: cargo xtask <task>\n\n\
         tasks:\n  \
         bump-version minor|patch   Bump semver in Cargo.toml\n  \
         read-version               Print current version"
    );
    process::exit(2)
}
