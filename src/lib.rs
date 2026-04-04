mod artifacts;
pub mod cache;
mod cli;
mod error;
mod fs_util;
mod github;
mod index;
pub mod launcher;
pub mod lockfile;
mod manifest;
mod module_id;
pub mod paths;
mod resolve;
mod staging;
pub mod sync;
mod ui;

pub use cli::{Cli, Command};
pub use error::{AgentpackError, Result};
pub use ui::Ui;

use std::env;
use std::path::Path;

use anyhow::Context;

/// Main entry for tests and binary.
pub fn run(cli: Cli) -> anyhow::Result<()> {
    let ui = Ui::new(cli.quiet, cli.no_progress, cli.debug);
    match cli.command {
        Command::Init { name, version } => {
            let root = init_root(cli.project_root.as_deref())?;
            ui.message("Initializing agentpack…");
            let dirname = root
                .file_name()
                .and_then(|n| n.to_str())
                .unwrap_or("project");
            let n = name.unwrap_or_else(|| dirname.to_string());
            let v = version.unwrap_or_else(|| "0.0.1".to_string());
            manifest::AgentpackManifest::write_stub(&root, &n, &v)?;
            lockfile::init_lockfile(&root, Some(n.clone()), Some(v))?;
            ui.message(format!(
                "Created {} and {}",
                paths::manifest_path(&root).display(),
                paths::lock_path(&root).display()
            ));
            Ok(())
        }
        Command::Lock => {
            let root = paths::resolve_project_root(cli.project_root.as_deref())?;
            sync::run_lock(&root, &ui)?;
            Ok(())
        }
        Command::Add { spec, no_sync } => {
            let root = paths::resolve_project_root(cli.project_root.as_deref())?;
            sync::run_add(&root, &spec, no_sync, &ui)?;
            Ok(())
        }
        Command::Remove { spec, no_sync } => {
            let root = paths::resolve_project_root(cli.project_root.as_deref())?;
            sync::run_remove(&root, &spec, no_sync, &ui)?;
            Ok(())
        }
        Command::Sync {
            dry_run,
            verify_only,
        } => {
            let root = paths::resolve_project_root(cli.project_root.as_deref())?;
            sync::run_sync(&root, dry_run, verify_only, &ui)?;
            Ok(())
        }
        Command::Claude { args } => {
            let root = paths::resolve_project_root(cli.project_root.as_deref())?;
            launcher::run_claude(&root, args, cli.yolo, &ui)
        }
        Command::Opencode { args } => {
            let root = paths::resolve_project_root(cli.project_root.as_deref())?;
            launcher::run_opencode(&root, args, cli.yolo, &ui)
        }
        Command::Codex { args } => {
            let root = paths::resolve_project_root(cli.project_root.as_deref())?;
            launcher::run_codex(&root, args, cli.yolo, &ui)
        }
        Command::Agent { args } => {
            let root = paths::resolve_project_root(cli.project_root.as_deref())?;
            launcher::run_agent(&root, args, cli.yolo, &ui)
        }
    }
}

fn init_root(explicit: Option<&Path>) -> anyhow::Result<std::path::PathBuf> {
    if let Some(p) = explicit {
        return p
            .canonicalize()
            .with_context(|| format!("project root {}", p.display()));
    }
    env::current_dir()
        .context("cwd")
        .and_then(|p| p.canonicalize().context("canonicalize cwd"))
}
