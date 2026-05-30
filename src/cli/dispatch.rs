//! Command dispatch — wires CLI variants to the correct subsystem.

use std::env;
use std::path::Path;

use anyhow::Context;

use super::{Cli, Command, McpAction, ModeAction};
use crate::harness::HarnessTarget;
use crate::ui::Ui;
use crate::{launcher, lockfile, manifest, paths, sync};

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
    if let Command::HookExec(args) = cli.command {
        return crate::cli::hook_exec::run(args);
    }

    let root = paths::resolve_project_root(cli.project_root.as_deref())?;

    match cli.command {
        Command::Init { .. } => unreachable!(),
        Command::HookExec(..) => unreachable!(),
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
            // Bare `agentpack sync` is not tied to a specific harness, so we don't write the
            // workspace-side overlay symlinks (`.cursor/agents`, `.agents/plugins/...`).
            sync::run_sync(
                &root,
                dry_run,
                verify_only,
                update_lock,
                cli.mode.as_deref(),
                None,
                &ui,
            )?;
        }
        Command::Claude { args } => {
            launcher::launch(HarnessTarget::Claude, &root, args, cli.mode.as_deref(), cli.yolo, &ui)?;
        }
        Command::Opencode { args } => {
            launcher::launch(HarnessTarget::OpenCode, &root, args, cli.mode.as_deref(), cli.yolo, &ui)?;
        }
        Command::Codex { args } => {
            launcher::launch(HarnessTarget::Codex, &root, args, cli.mode.as_deref(), cli.yolo, &ui)?;
        }
        Command::Grok { args } => {
            launcher::launch(HarnessTarget::Grok, &root, args, cli.mode.as_deref(), cli.yolo, &ui)?;
        }
        Command::Agy { args } => {
            launcher::launch(HarnessTarget::Agy, &root, args, cli.mode.as_deref(), cli.yolo, &ui)?;
        }
        Command::Agent { args } => {
            launcher::launch(HarnessTarget::Cursor, &root, args, cli.mode.as_deref(), cli.yolo, &ui)?;
        }
        Command::Mcp { action } => {
            run_mcp(&root, action, &ui)?;
        }
        Command::Mode { action } => {
            run_mode(&root, action, &ui)?;
        }
    }
    Ok(())
}

fn run_mcp(root: &Path, action: McpAction, ui: &Ui) -> anyhow::Result<()> {
    match action {
        McpAction::Add {
            name,
            command,
            args,
            env,
            no_sync,
        } => {
            let entry = crate::staging::mcp::McpServerEntry {
                kind: None,
                command: Some(command),
                args,
                env: env.into_iter().collect(),
                url: None,
                disabled: None,
            };
            manifest::AgentpackManifest::add_mcp_server(root, &name, &entry)?;
            ui.message(format!("Added MCP server \"{name}\" to agentpack.toml"));
            if !no_sync {
                sync::run_sync(root, false, false, false, None, None, ui)?;
            }
        }
        McpAction::Remove { name, no_sync } => {
            manifest::AgentpackManifest::remove_mcp_server(root, &name)?;
            ui.message(format!("Removed MCP server \"{name}\" from agentpack.toml"));
            if !no_sync {
                sync::run_sync(root, false, false, false, None, None, ui)?;
            }
        }
        McpAction::List => {
            let lock = lockfile::PackLock::load(root).unwrap_or_default();
            let manifest = manifest::AgentpackManifest::load(root)?;
            let merged =
                crate::staging::mcp::collect_merged_mcp(root, &lock, manifest.as_ref(), None)?;
            if merged.is_empty() {
                ui.message("No MCP servers configured.");
            } else {
                for (name, (entry, source)) in &merged {
                    let disabled = if entry.disabled == Some(true) {
                        " (disabled)"
                    } else {
                        ""
                    };
                    let shown = if let Some(url) = &entry.url {
                        url.clone()
                    } else {
                        let args = entry.args.join(" ");
                        let cmd = entry.command.as_deref().unwrap_or("");
                        format!("{cmd} {args}").trim().to_string()
                    };
                    ui.message(format!("  {name}: {shown} [from {source}]{disabled}"));
                }
            }
        }
    }
    Ok(())
}

fn run_mode(root: &Path, action: ModeAction, ui: &Ui) -> anyhow::Result<()> {
    let manifest = manifest::AgentpackManifest::load(root)?
        .ok_or_else(|| anyhow::anyhow!("agentpack.toml required for mode management"))?;
    let lock = lockfile::PackLock::load(root).ok();
    let catalog =
        crate::mode::catalog::CapabilityCatalog::build(root, lock.as_ref(), Some(&manifest))?;

    match action {
        ModeAction::List => {
            for name in manifest.list_mode_names() {
                let definition = manifest
                    .mode_definition(&name)
                    .expect("listed mode should resolve");
                ui.message(format!(
                    "{name}: base={}, enable={}, disable={}",
                    definition.base,
                    definition.enable.len(),
                    definition.disable.len()
                ));
            }
        }
        ModeAction::Show { name } => {
            let definition = manifest
                .mode_definition(&name)
                .ok_or_else(|| anyhow::anyhow!("unknown mode: {name}"))?;
            ui.message(format!("mode: {name}"));
            ui.message(format!("base: {}", definition.base));
            ui.message(format!("enable: {:?}", definition.enable));
            ui.message(format!("disable: {:?}", definition.disable));
        }
        ModeAction::Create { name } => {
            manifest::AgentpackManifest::create_mode(root, &name)?;
            ui.message(format!("Created mode \"{name}\"."));
        }
        ModeAction::Delete { name } => {
            manifest::AgentpackManifest::delete_mode(root, &name)?;
            ui.message(format!("Deleted mode \"{name}\"."));
        }
        ModeAction::Enable { name, selectors } => {
            require_known_mode(&manifest, &name)?;
            validate_selectors(&catalog, &selectors)?;
            manifest::AgentpackManifest::add_mode_selectors(root, &name, true, &selectors)?;
            ui.message(format!("Updated mode \"{name}\"."));
        }
        ModeAction::Disable { name, selectors } => {
            require_known_mode(&manifest, &name)?;
            validate_selectors(&catalog, &selectors)?;
            manifest::AgentpackManifest::add_mode_selectors(root, &name, false, &selectors)?;
            ui.message(format!("Updated mode \"{name}\"."));
        }
        ModeAction::Base { name, base } => {
            require_known_mode(&manifest, &name)?;
            manifest::AgentpackManifest::set_mode_base(root, &name, base.into())?;
            ui.message(format!(
                "Set mode \"{name}\" base to {}.",
                crate::mode::ModeBase::from(base)
            ));
        }
        ModeAction::Tui { name } => {
            crate::mode::tui::run_mode_tui(root, &manifest, &catalog, name.as_deref())?;
        }
    }

    Ok(())
}

fn require_known_mode(manifest: &manifest::AgentpackManifest, name: &str) -> anyhow::Result<()> {
    if manifest.mode_definition(name).is_some() {
        return Ok(());
    }
    Err(anyhow::anyhow!("unknown mode: {name}"))
}

fn validate_selectors(
    catalog: &crate::mode::catalog::CapabilityCatalog,
    selectors: &[String],
) -> anyhow::Result<()> {
    for raw in selectors {
        let selector = crate::mode::selectors::Selector::parse(raw)?;
        catalog.validate_selector(&selector)?;
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
