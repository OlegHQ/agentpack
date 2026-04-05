//! Command dispatch — wires CLI variants to the correct subsystem.

use std::env;
use std::path::Path;

use anyhow::Context;

use super::{Cli, Command};
use crate::{launcher, lockfile, manifest, paths, sync};
use crate::ui::Ui;

/// Main entry for tests and binary.
pub fn run(cli: Cli) -> anyhow::Result<()> {
    let ui = Ui::new(cli.quiet, cli.no_progress, cli.debug);

    // Init is special: it doesn't require an existing project root.
    if let Command::Init { name, version } = cli.command {
        let root = init_root(cli.project_root.as_deref())?;
        ui.message("Initializing agentpack\u{2026}");
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
        return Ok(());
    }

    let root = paths::resolve_project_root(cli.project_root.as_deref())?;

    match cli.command {
        Command::Init { .. } => unreachable!(),
        Command::Lock { update } => {
            sync::run_lock(&root, update, &ui)?;
        }
        Command::Add { spec, no_sync } => {
            sync::run_add(&root, &spec, no_sync, &ui)?;
        }
        Command::Remove { spec, no_sync } => {
            sync::run_remove(&root, &spec, no_sync, &ui)?;
        }
        Command::Sync {
            dry_run,
            verify_only,
            update_lock,
        } => {
            sync::run_sync(&root, dry_run, verify_only, update_lock, &ui)?;
        }
        Command::Claude { args } => {
            launcher::run_claude(&root, args, cli.yolo, &ui)?;
        }
        Command::Opencode { args } => {
            launcher::run_opencode(&root, args, cli.yolo, &ui)?;
        }
        Command::Codex { args } => {
            launcher::run_codex(&root, args, cli.yolo, &ui)?;
        }
        Command::Agent { args } => {
            launcher::run_agent(&root, args, cli.yolo, &ui)?;
        }
    }
    Ok(())
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
