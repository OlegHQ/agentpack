use std::ffi::OsString;
use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::Context;
use serde_json::Value;
use serde_norway::Mapping;

use super::{require, Harness, HarnessTarget, LaunchCtx, StageCtx};
use crate::artifacts::yaml::insert_string;
use crate::artifacts::ArtifactKind;
use crate::error::{AgentpackError, Result};
use crate::hooks::capabilities::{cursor_support, SupportLevel};
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::render::{CursorHookRenderer, HookRenderer};
use crate::hooks::runtime::translate::cursor_output;
use crate::launcher::common::{apply_yolo_cursor_agent, resolve_harness_binary};
use crate::paths::{
    cursor_workspace_dir, staging_cursor_bundle_dir_for_mode, staging_cursor_home_dir_for_mode,
    staging_cursor_pack_plugin_dir_for_mode,
};
use crate::staging::mcp::{write_mcp_servers_json, StagedMcpEntries};
use crate::staging::{
    finalize_cursor_workspace_overlay, force_cursor_attribution_off,
    prepare_cursor_staging_without_pack_overlay, read_cursor_overlay_manifest,
};

/// Cursor: pack plugin tree plus a fake `HOME` and an optional workspace `.cursor/agents` overlay.
pub(super) struct Cursor;

impl Harness for Cursor {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Cursor
    }

    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf> {
        staging_cursor_pack_plugin_dir_for_mode(project_root, mode)
    }

    fn reset_paths(&self, project_root: &Path, mode: &str) -> Result<Vec<PathBuf>> {
        // The pack plugin lives under the bundle root; also wipe the fake HOME.
        Ok(vec![
            staging_cursor_bundle_dir_for_mode(project_root, mode)?,
            staging_cursor_home_dir_for_mode(project_root, mode)?,
        ])
    }

    fn prepare(&self, ctx: &StageCtx) -> Result<()> {
        let mode = ctx.mode.name();
        prepare_cursor_staging_without_pack_overlay(ctx.project_root, mode)?;
        let cursor_pack = self.staged_root(ctx.project_root, mode)?;
        let cursor_bundle = staging_cursor_bundle_dir_for_mode(ctx.project_root, mode)?;
        force_cursor_attribution_off(&cursor_bundle)?;
        force_cursor_attribution_off(&cursor_pack)
    }

    fn write_mcp(&self, merged: &StagedMcpEntries, ctx: &StageCtx) -> Result<()> {
        // Only the pack `mcp.json`; the fake-HOME re-merge with the user's `~/.cursor/mcp.json`
        // stays in finalize_cursor_staging_common.
        let pack = self.staged_root(ctx.project_root, ctx.mode.name())?;
        write_mcp_servers_json(&pack.join("mcp.json"), merged)
    }

    fn hook_support(&self, event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel {
        cursor_support(event, handler)
    }

    fn hook_output(&self, event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
        cursor_output(event, result)
    }

    fn hook_renderer(&self) -> Option<Box<dyn HookRenderer>> {
        Some(Box::new(CursorHookRenderer))
    }

    fn raw_plugin_subdirs(&self) -> &'static [&'static str] {
        // Cursor plugins often ship `skills/<slug>/…` plus `commands` / `agents` / `rules` at the
        // repo root. Copy these subtrees verbatim first so non-`.md` assets (eval JSON, reference
        // snippets, etc.) survive; the markdown pass then overlays rendered artifacts.
        &[
            "hooks", "assets", "scripts", "commands", "agents", "rules", "skills",
        ]
    }

    fn seed_command_frontmatter(&self, m: &mut Mapping, name: &str, description: &str) {
        insert_string(m, "name", name);
        insert_string(m, "description", description);
    }

    fn command_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[
            "agent",
            "allowed-tools",
            "context",
            "disable-model-invocation",
            "model",
            "permission",
            "subtask",
        ]
    }

    fn rendered_artifact_kind(&self, source: ArtifactKind) -> ArtifactKind {
        match source {
            // Cursor has native rule files, so rules stay rules.
            ArtifactKind::Rule => ArtifactKind::Rule,
            other => other,
        }
    }

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let mode = ctx.mode.name();
        let bundle_root = staging_cursor_bundle_dir_for_mode(ctx.project_root, mode)?;
        let pack_plugin = staging_cursor_pack_plugin_dir_for_mode(ctx.project_root, mode)?;
        let home = staging_cursor_home_dir_for_mode(ctx.project_root, mode)?;
        require(bundle_root.is_dir(), || {
            format!("cursor staging missing {}", bundle_root.display())
        })?;
        require(
            pack_plugin.join(".cursor-plugin/plugin.json").is_file(),
            || {
                format!(
                    "cursor pack plugin missing {}",
                    pack_plugin.join(".cursor-plugin/plugin.json").display()
                )
            },
        )?;
        require(
            bundle_root
                .join(".cursor-plugin/marketplace.json")
                .is_file(),
            || {
                format!(
                    "cursor staging missing {}",
                    bundle_root.join(".cursor-plugin/marketplace.json").display()
                )
            },
        )?;
        require(home.join(".cursor").is_dir(), || {
            format!("cursor fake home missing .cursor/ under {}", home.display())
        })?;
        if ctx.launch_target == Some(HarnessTarget::Cursor) {
            for rel in read_cursor_overlay_manifest(ctx.project_root)? {
                let tracked = cursor_workspace_dir(ctx.project_root).join(&rel);
                if !tracked.exists() {
                    return Err(AgentpackError::Staging(format!(
                        "cursor workspace overlay missing at {} (from cursor-overlay.manifest entry {})",
                        tracked.display(),
                        rel.display()
                    )));
                }
            }
        }
        Ok(())
    }

    fn launch_command(&self, ctx: LaunchCtx) -> anyhow::Result<Command> {
        let fake_home_path = staging_cursor_home_dir_for_mode(ctx.project_root, ctx.mode.name())?;
        let fake_home: OsString = fake_home_path.into_os_string();
        let project_norm = normalize_path(ctx.project_root);

        let mut args = ctx.passthrough;
        let workspace = match explicit_workspace_arg(&args) {
            Some(p) => normalize_path(&p),
            None => {
                args.splice(
                    0..0,
                    [
                        "--workspace".to_string(),
                        project_norm.display().to_string(),
                    ],
                );
                project_norm.clone()
            }
        };

        let mut envs: Vec<(&str, OsString)> = vec![("HOME", fake_home.clone())];
        #[cfg(windows)]
        {
            envs.push(("USERPROFILE", fake_home.clone()));
            let roaming = Path::new(&fake_home).join("AppData").join("Roaming");
            let local = Path::new(&fake_home).join("AppData").join("Local");
            envs.push(("APPDATA", roaming.into_os_string()));
            envs.push(("LOCALAPPDATA", local.into_os_string()));
        }
        #[cfg(target_os = "linux")]
        {
            let cfg = Path::new(&fake_home).join(".config");
            envs.push(("XDG_CONFIG_HOME", cfg.into_os_string()));
            let data = Path::new(&fake_home).join(".local/share");
            envs.push(("XDG_DATA_HOME", data.into_os_string()));
        }

        // Cursor skill / command / agent discovery still appears tied to the HOME-backed `.cursor`
        // tree, so keep the fake HOME layout that Cursor already knows how to scan.
        let cursor_config_dir = Path::new(&fake_home).join(".cursor");
        envs.push(("CURSOR_CONFIG_DIR", cursor_config_dir.into_os_string()));

        if let Some(real_home) = dirs::home_dir() {
            push_env_if_absent(&mut envs, "CARGO_HOME", real_home.join(".cargo"));
            push_env_if_absent(&mut envs, "RUSTUP_HOME", real_home.join(".rustup"));
            push_env_if_absent(&mut envs, "DOCKER_CONFIG", real_home.join(".docker"));
        }

        // Cursor stores workspace trust at `$CURSOR_DATA_DIR/projects/<slug>/.workspace-trusted`,
        // defaulting `CURSOR_DATA_DIR` to `homedir()/.cursor`. With `HOME` redirected to ephemeral
        // staging, keep trust state on the real profile unless the env already sets it.
        let mut injected_cursor_data_dir = false;
        if std::env::var_os("CURSOR_DATA_DIR").is_none() {
            if let Some(h) = dirs::home_dir() {
                envs.push(("CURSOR_DATA_DIR", h.join(".cursor").into_os_string()));
                injected_cursor_data_dir = true;
            }
        }

        let mut msg = format!(
            "Cursor Agent workspace (--workspace): {}\nCursor fake HOME (agentpack): {}",
            workspace.display(),
            Path::new(&fake_home).display()
        );
        msg.push_str("\nCURSOR_CONFIG_DIR: fake HOME .cursor (pack agents/commands)");
        if std::env::var_os("CARGO_HOME").is_none() {
            msg.push_str("\nCARGO_HOME: real ~/.cargo");
        }
        if std::env::var_os("RUSTUP_HOME").is_none() {
            msg.push_str("\nRUSTUP_HOME: real ~/.rustup");
        }
        if std::env::var_os("DOCKER_CONFIG").is_none() {
            msg.push_str("\nDOCKER_CONFIG: real ~/.docker");
        }
        if injected_cursor_data_dir {
            msg.push_str("\nCURSOR_DATA_DIR: real ~/.cursor (workspace trust + projects; avoids ephemeral staging)");
        }
        if ctx.yolo {
            apply_yolo_cursor_agent(&mut args);
        }
        prepend_trust_if_needed(&mut args);
        ctx.ui.debug_message(msg);

        let agent = resolve_harness_binary("CURSOR_AGENT_PATH", "agent").with_context(|| {
            "Cursor Agent CLI (`agent`) not found.\n\
             Install Cursor with the Agent CLI available on your PATH, or set CURSOR_AGENT_PATH to the `agent` executable."
        })?;
        let mut cmd = Command::new(&agent);
        for (key, value) in &envs {
            cmd.env(key, value);
        }
        cmd.args(args);
        Ok(cmd)
    }

    fn finalize_workspace_overlay(&self, ctx: &StageCtx) -> Result<()> {
        finalize_cursor_workspace_overlay(ctx.project_root, ctx.mode.name())
    }
}

fn explicit_workspace_arg(args: &[String]) -> Option<PathBuf> {
    for (idx, arg) in args.iter().enumerate() {
        if arg == "--workspace" {
            return args.get(idx + 1).map(PathBuf::from);
        }
        if let Some(value) = arg.strip_prefix("--workspace=") {
            return Some(PathBuf::from(value));
        }
    }
    None
}

fn args_contain_trust_flag(args: &[String]) -> bool {
    args.iter().any(|a| a == "--trust")
}

/// Cursor `agent` only allows `--trust` together with `--print` / stream output.
fn args_allow_trust_with_print(args: &[String]) -> bool {
    for (i, a) in args.iter().enumerate() {
        if a == "-p" || a == "--print" {
            return true;
        }
        if a.starts_with("--output-format=") {
            return true;
        }
        if a == "--output-format" {
            return args.get(i + 1).is_some();
        }
    }
    false
}

/// Prepends `--trust` in headless mode (when `--print` / `-p` / `--output-format` is present).
fn prepend_trust_if_needed(args: &mut Vec<String>) {
    if args_allow_trust_with_print(args) && !args_contain_trust_flag(args) {
        args.insert(0, "--trust".into());
    }
}

fn normalize_path(path: &Path) -> PathBuf {
    path.canonicalize().unwrap_or_else(|_| path.to_path_buf())
}

fn push_env_if_absent(envs: &mut Vec<(&'static str, OsString)>, key: &'static str, value: PathBuf) {
    if std::env::var_os(key).is_none() {
        envs.push((key, value.into_os_string()));
    }
}
