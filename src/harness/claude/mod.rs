mod hooks;
mod settings;

use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::Context;
use serde_json::Value;
use serde_norway::Mapping;

use super::{require, Harness, HarnessTarget, LaunchCtx, StageCtx};
use crate::artifacts::yaml::insert_string;
use crate::error::{AgentpackError, Result};
use crate::hooks::capabilities::SupportLevel;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::render::HookRenderer;
use crate::hooks::runtime::output::claude_fallback_output;
use crate::launcher::common::{apply_yolo_claude, resolve_harness_binary};
use crate::paths::{
    agentpack_claude_settings_path, staging_plugins_dir_for_mode, STAGED_AGENTPACK_BUNDLE_NAME,
};
use crate::staging::keep_attribution;
use crate::staging::list_plugin_dirs;
use crate::staging::mcp::{write_claude_mcp_servers_json, StagedMcpEntries};

/// Claude Code: staged as a `--plugin-dir` bundle; attribution overlay via `--settings`.
pub(super) struct Claude;

/// Claude and Grok share the same verbatim plugin subtrees and `commands/*.md` key order, so the
/// data lives here and `Grok` reuses it.
pub(super) const CLAUDE_RAW_PLUGIN_SUBDIRS: &[&str] = &[
    "hooks", "matchers", "core", "examples", "utils", "commands", "agents", "rules", "skills",
];

pub(super) fn seed_description_then_name(m: &mut Mapping, name: &str, description: &str) {
    insert_string(m, "description", description);
    insert_string(m, "name", name);
}

/// Write the Claude plugin manifest (`.claude-plugin/plugin.json`) into the bundle root.
fn write_bundle_manifest(bundle: &Path) -> Result<()> {
    let plugin_dir = bundle.join(".claude-plugin");
    fs::create_dir_all(&plugin_dir).map_err(|e| AgentpackError::io(&plugin_dir, e))?;
    let manifest = r#"{"name":"agentpack-bundle","version":"1.0.0","description":"Merged pack.lock plugins/skills; optional user settings.json and .claude.json"}"#;
    let plugin_json = plugin_dir.join("plugin.json");
    fs::write(&plugin_json, manifest).map_err(|e| AgentpackError::io(&plugin_json, e))?;
    Ok(())
}

impl Harness for Claude {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Claude
    }

    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf> {
        Ok(staging_plugins_dir_for_mode(project_root, mode)?.join(STAGED_AGENTPACK_BUNDLE_NAME))
    }

    fn reset_paths(&self, project_root: &Path, mode: &str) -> Result<Vec<PathBuf>> {
        // Wipe the whole `plugins/` parent (Claude loads it via `--plugin-dir`), not just the bundle.
        Ok(vec![staging_plugins_dir_for_mode(project_root, mode)?])
    }

    fn prepare(&self, ctx: &StageCtx) -> Result<()> {
        // Claude bundle (loaded via `--plugin-dir`; user settings live in the staged config dir).
        let plugins_base = staging_plugins_dir_for_mode(ctx.project_root, ctx.mode.name())?;
        fs::create_dir_all(&plugins_base).map_err(|e| AgentpackError::io(&plugins_base, e))?;
        let bundle = self.staged_root(ctx.project_root, ctx.mode.name())?;
        fs::create_dir_all(&bundle).map_err(|e| AgentpackError::io(&bundle, e))?;
        write_bundle_manifest(&bundle)?;
        // Attribution overlay consumed by the launcher via `claude --settings <path>`.
        settings::materialize_claude_settings_overlay()
    }

    fn write_mcp(&self, merged: &StagedMcpEntries, ctx: &StageCtx) -> Result<()> {
        let bundle = self.staged_root(ctx.project_root, ctx.mode.name())?;
        write_claude_mcp_servers_json(&bundle.join(".mcp.json"), merged)
    }

    fn finalize(&self, merged: &StagedMcpEntries, _ctx: &StageCtx) -> Result<()> {
        // Pre-approve the staged MCP servers in the `--settings` overlay so Claude doesn't drop
        // them as untrusted project-scope MCPs.
        if !merged.is_empty() && !keep_attribution() {
            let names: Vec<String> = merged.keys().cloned().collect();
            settings::set_claude_settings_mcp_allowlist(&names)?;
        }
        Ok(())
    }

    fn hook_support(&self, _event: ClaudeEvent, _handler: &ClaudeHandler) -> SupportLevel {
        SupportLevel::Native
    }

    fn hook_output(&self, _event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
        claude_fallback_output(result)
    }

    fn hook_renderer(&self) -> Option<Box<dyn HookRenderer>> {
        Some(Box::new(hooks::ClaudeHookRenderer))
    }

    fn raw_plugin_subdirs(&self) -> &'static [&'static str] {
        CLAUDE_RAW_PLUGIN_SUBDIRS
    }

    fn seed_command_frontmatter(&self, m: &mut Mapping, name: &str, description: &str) {
        seed_description_then_name(m, name, description);
    }

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let bundle = staging_plugins_dir_for_mode(ctx.project_root, ctx.mode.name())?
            .join(STAGED_AGENTPACK_BUNDLE_NAME);
        require(bundle.join(".claude-plugin/plugin.json").is_file(), || {
            format!("bundle missing manifest {}", bundle.display())
        })?;

        // Claude attribution overlay (passed via `claude --settings`). Lives under
        // `$AGENTPACK_HOME` so credentials stay in the user-global keychain entry.
        if !keep_attribution() {
            let overlay = agentpack_claude_settings_path()?;
            require(overlay.is_file(), || {
                format!("claude --settings overlay missing {}", overlay.display())
            })?;
        }
        Ok(())
    }

    fn launch_command(&self, ctx: LaunchCtx) -> anyhow::Result<Command> {
        let mut passthrough = ctx.passthrough;
        if ctx.yolo {
            apply_yolo_claude(&mut passthrough);
        }

        let plugin_dirs = list_plugin_dirs(ctx.project_root, ctx.mode.name())?;
        if !plugin_dirs.is_empty() {
            let rendered = plugin_dirs
                .iter()
                .map(|dir| format!("  {}", dir.display()))
                .collect::<Vec<_>>()
                .join("\n");
            ctx.ui
                .debug_message(format!("Claude plugin dirs:\n{rendered}"));
        }

        let claude = resolve_harness_binary("CLAUDE_CODE_PATH", "claude").with_context(|| {
            "Claude Code CLI (`claude`) not found.\n\
             Install Claude Code and ensure `claude` is on your PATH, or set CLAUDE_CODE_PATH to the executable."
        })?;

        // We deliberately do NOT set `CLAUDE_CONFIG_DIR`: Claude namespaces credential storage by
        // `sha256(CLAUDE_CONFIG_DIR)`, so any override would forget login on every project switch.
        // The attribution-off overlay is loaded via `--settings` instead.
        let mut cmd = Command::new(&claude);
        let settings_overlay = agentpack_claude_settings_path()?;
        if settings_overlay.is_file() {
            ctx.ui
                .debug_message(format!("--settings {}", settings_overlay.display()));
            cmd.arg("--settings").arg(&settings_overlay);
        }
        for d in &plugin_dirs {
            cmd.arg("--plugin-dir").arg(d);
        }
        cmd.args(passthrough);
        Ok(cmd)
    }
}
