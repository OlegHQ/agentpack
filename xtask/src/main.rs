//! Workspace dev tasks — replaces `scripts/*.py`.
//!
//! Usage:
//!   cargo xtask bump-version minor|patch
//!   cargo xtask read-version
//!   cargo xtask sync-homebrew <tap-root> <owner/repo> <version>

use std::path::{Path, PathBuf};
use std::{env, fs, process};

use sha2::{Digest, Sha256};

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

// ── sync-homebrew ─────────────────────────────────────────────────────────────

fn sync_homebrew(tap_root: &str, slug: &str, version: &str) {
    if !slug.contains('/') {
        eprintln!("error: owner/repo required");
        process::exit(2);
    }

    let tag = format!("v{version}");
    let url = format!("https://github.com/{slug}/archive/refs/tags/{tag}.tar.gz");
    let formula = Path::new(tap_root).join("Formula/agentpack.rb");

    if !formula.is_file() {
        eprintln!(
            "error: missing {} -- clone the tap or run tap-bootstrap (see README)",
            formula.display()
        );
        process::exit(1);
    }

    let response = ureq::get(&url).call().unwrap_or_else(|e| {
        eprintln!("error: could not fetch {url} ({e})");
        eprintln!("hint: push the git tag and wait a few seconds, then retry");
        process::exit(1);
    });

    let data = response
        .into_body()
        .read_to_vec()
        .expect("read response body");

    let sha = hex::encode(Sha256::digest(&data));
    let text = fs::read_to_string(&formula).expect("read formula");

    let (text, n_url) = replace_first_match(&text, r#"  url ""#, &format!("  url \"{url}\""));
    let (text, n_sha) = replace_first_match(&text, r#"  sha256 ""#, &format!("  sha256 \"{sha}\""));

    if n_url != 1 || n_sha != 1 {
        eprintln!(
            "error: formula must contain exactly one top-level `url` and `sha256` line \
             (got url={n_url}, sha256={n_sha})"
        );
        process::exit(1);
    }

    fs::write(&formula, text).expect("write formula");
    println!("updated {}", formula.display());
    println!("  url {url}");
    println!("  sha256 {sha}");
}

/// Replace the first line starting with `prefix` with `replacement`. Returns (new text, count).
fn replace_first_match(text: &str, prefix: &str, replacement: &str) -> (String, usize) {
    let mut out = String::with_capacity(text.len());
    let mut replaced = 0usize;
    for line in text.lines() {
        if replaced == 0 && line.trim_start().starts_with(prefix.trim_start()) {
            out.push_str(replacement);
            replaced += 1;
        } else {
            out.push_str(line);
        }
        out.push('\n');
    }
    if !text.ends_with('\n') && out.ends_with('\n') {
        out.pop();
    }
    (out, replaced)
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
        "sync-homebrew" => {
            if args.len() != 5 {
                eprintln!("usage: cargo xtask sync-homebrew <tap-root> <owner/repo> <version>");
                process::exit(2);
            }
            sync_homebrew(&args[2], &args[3], &args[4]);
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
         read-version               Print current version\n  \
         sync-homebrew <tap> <owner/repo> <ver>  Update Homebrew formula"
    );
    process::exit(2)
}
