use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::Context;
use serde_json::Value;
use serde_norway::Mapping;

use super::claude::{seed_description_then_name, CLAUDE_RAW_PLUGIN_SUBDIRS};
use super::{require, Harness, HarnessTarget, LaunchCtx, StageCtx};
use crate::error::{AgentpackError, Result};
use crate::fs_util::{read_toml_value_or_default, remove_path_any, write_text_file};
use crate::hooks::capabilities::SupportLevel;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::runtime::translate::claude_fallback_output;
use crate::launcher::common::{apply_yolo_grok, args_have_flag_with_value, resolve_harness_binary};
use crate::paths::{
    staging_grok_bundle_dir_for_mode, staging_grok_dir_for_mode, staging_grok_home_dir_for_mode,
};
use crate::staging::mcp::{merge_into_toml_mcp_config, StagedMcpEntries};
use crate::staging::{copy_selected_entries, keep_attribution, NO_ATTRIBUTION_BODY};

/// Grok user-home entries preserved before overlaying pack content.
const GROK_HOME_ENTRIES: &[&str] = &["config.toml", "skills", "agents", "commands", "plugins"];
/// Grok credential/session files linked from the real user home when present.
const GROK_HOME_CREDENTIAL_FILES: &[&str] = &["auth.json", "mcp_credentials.json"];
const GROK_ATTRIBUTION_FILE: &str = "AGENTS.md";
const GROK_ATTRIBUTION_BEGIN: &str = "<!-- agentpack:no-attribution:begin -->";
const GROK_ATTRIBUTION_END: &str = "<!-- agentpack:no-attribution:end -->";

/// Grok: launched with a redirected `GROK_HOME`; pack content staged as a plugin bundle. Its
/// artifact-rendering knobs are identical to Claude's.
pub(super) struct Grok;

impl Harness for Grok {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Grok
    }

    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf> {
        staging_grok_bundle_dir_for_mode(project_root, mode)
    }

    fn reset_paths(&self, project_root: &Path, mode: &str) -> Result<Vec<PathBuf>> {
        // Pack content lives under `grok/`, but MCP/attribution/config live in `grok-home/`.
        Ok(vec![
            staging_grok_home_dir_for_mode(project_root, mode)?,
            staging_grok_dir_for_mode(project_root, mode)?,
        ])
    }

    fn prepare(&self, ctx: &StageCtx) -> Result<()> {
        let mode = ctx.mode.name();
        let grok_dir = staging_grok_dir_for_mode(ctx.project_root, mode)?;
        fs::create_dir_all(&grok_dir).map_err(|e| AgentpackError::io(&grok_dir, e))?;
        let grok_bundle = self.staged_root(ctx.project_root, mode)?;
        fs::create_dir_all(&grok_bundle).map_err(|e| AgentpackError::io(&grok_bundle, e))?;
        write_grok_bundle_manifest(&grok_bundle)?;
        let grok_home = staging_grok_home_dir_for_mode(ctx.project_root, mode)?;
        fs::create_dir_all(&grok_home).map_err(|e| AgentpackError::io(&grok_home, e))?;
        seed_grok_home(&grok_home, &grok_bundle)?;
        force_grok_attribution_off(&grok_home)
    }

    fn write_mcp(&self, merged: &StagedMcpEntries, ctx: &StageCtx) -> Result<()> {
        // MCP is native TOML in `grok-home/config.toml`, not in the pack bundle (staged_root).
        let grok_home = staging_grok_home_dir_for_mode(ctx.project_root, ctx.mode.name())?;
        merge_into_toml_mcp_config(&grok_home.join("config.toml"), merged)
    }

    fn hook_support(&self, _event: ClaudeEvent, _handler: &ClaudeHandler) -> SupportLevel {
        SupportLevel::Unsupported {
            reason: "Grok hooks are not staged because current Grok only loaded hooks from HOME/project-trusted roots in smoke tests",
        }
    }

    fn hook_output(&self, _event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
        claude_fallback_output(result)
    }

    fn raw_plugin_subdirs(&self) -> &'static [&'static str] {
        CLAUDE_RAW_PLUGIN_SUBDIRS
    }

    fn seed_command_frontmatter(&self, m: &mut Mapping, name: &str, description: &str) {
        seed_description_then_name(m, name, description);
    }

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let mode = ctx.mode.name();
        let grok_home = staging_grok_home_dir_for_mode(ctx.project_root, mode)?;
        let grok_bundle = staging_grok_bundle_dir_for_mode(ctx.project_root, mode)?;
        require(grok_home.join("config.toml").is_file(), || {
            format!(
                "grok home missing config.toml under {}",
                grok_home.display()
            )
        })?;
        require(grok_bundle.join("plugin.json").is_file(), || {
            format!(
                "grok bundle missing {}",
                grok_bundle.join("plugin.json").display()
            )
        })
    }

    fn launch_command(&self, ctx: LaunchCtx) -> anyhow::Result<Command> {
        let mut passthrough = ctx.passthrough;
        if !args_have_flag_with_value(&passthrough, "--cwd") {
            passthrough.splice(
                0..0,
                ["--cwd".to_string(), ctx.project_root.display().to_string()],
            );
        }
        if ctx.yolo {
            apply_yolo_grok(&mut passthrough);
        }
        let grok_home = staging_grok_home_dir_for_mode(ctx.project_root, ctx.mode.name())?;
        ctx.ui
            .debug_message(format!("Grok home: {}", grok_home.display()));
        let grok = resolve_harness_binary("GROK_PATH", "grok").with_context(|| {
            "Grok CLI (`grok`) not found.\n\
             Install Grok and ensure `grok` is on your PATH, or set GROK_PATH to the executable."
        })?;
        let mut cmd = Command::new(&grok);
        cmd.env("GROK_HOME", grok_home);
        cmd.args(passthrough);
        Ok(cmd)
    }
}

/// Write Grok's minimal plugin manifest (`plugin.json`) into the bundle root.
fn write_grok_bundle_manifest(bundle: &Path) -> Result<()> {
    let plugin_json = bundle.join("plugin.json");
    fs::write(&plugin_json, r#"{"name":"agentpack-bundle"}"#)
        .map_err(|e| AgentpackError::io(&plugin_json, e))
}

/// Seed `$GROK_HOME` from the user's real `~/.grok` (config + assets), link credentials, and point
/// `config.toml`'s `[plugins].paths` at the staged pack bundle.
fn seed_grok_home(root: &Path, plugin_bundle: &Path) -> Result<()> {
    let Some(home) = dirs::home_dir() else {
        ensure_grok_plugin_path(&root.join("config.toml"), plugin_bundle)?;
        return Ok(());
    };
    let user_root = home.join(".grok");
    copy_selected_entries(&user_root, root, GROK_HOME_ENTRIES)?;
    for name in GROK_HOME_CREDENTIAL_FILES {
        symlink_or_copy_file(&user_root.join(name), &root.join(name))?;
    }
    ensure_grok_plugin_path(&root.join("config.toml"), plugin_bundle)?;
    Ok(())
}

fn symlink_or_copy_file(src: &Path, dst: &Path) -> Result<()> {
    if !src.is_file() {
        return Ok(());
    }
    if fs::symlink_metadata(dst).is_ok() {
        remove_path_any(dst)?;
    }
    if let Some(parent) = dst.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    #[cfg(unix)]
    {
        std::os::unix::fs::symlink(src, dst).map_err(|e| AgentpackError::io(dst, e))?;
        Ok(())
    }
    #[cfg(windows)]
    {
        std::os::windows::fs::symlink_file(src, dst)
            .or_else(|_| fs::copy(src, dst).map(|_| ()))
            .map_err(|e| AgentpackError::io(dst, e))
    }
    #[cfg(not(any(unix, windows)))]
    {
        fs::copy(src, dst)
            .map(|_| ())
            .map_err(|e| AgentpackError::io(dst, e))
    }
}

fn ensure_grok_plugin_path(config_path: &Path, plugin_bundle: &Path) -> Result<()> {
    let mut doc = read_toml_value_or_default(config_path)?;
    let root = doc.as_table_mut().ok_or_else(|| {
        AgentpackError::Staging(format!(
            "{}: top-level must be a TOML table",
            config_path.display()
        ))
    })?;
    let plugins = root
        .entry("plugins".to_string())
        .or_insert_with(|| toml::Value::Table(Default::default()))
        .as_table_mut()
        .ok_or_else(|| {
            AgentpackError::Staging(format!(
                "{}: `plugins` must be a TOML table",
                config_path.display()
            ))
        })?;
    let paths = plugins
        .entry("paths".to_string())
        .or_insert_with(|| toml::Value::Array(Vec::new()))
        .as_array_mut()
        .ok_or_else(|| {
            AgentpackError::Staging(format!(
                "{}: `plugins.paths` must be an array",
                config_path.display()
            ))
        })?;
    let plugin_path = plugin_bundle.to_string_lossy().into_owned();
    if !paths
        .iter()
        .any(|value| value.as_str().is_some_and(|path| path == plugin_path))
    {
        paths.push(toml::Value::String(plugin_path));
    }
    let out = toml::to_string(&doc)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", config_path.display())))?;
    write_text_file(config_path, &out)
}

/// Grok has no confirmed first-class attribution setting. Add staged prompt-level guidance to
/// `$GROK_HOME/AGENTS.md` only.
fn force_grok_attribution_off(grok_home: &Path) -> Result<()> {
    if keep_attribution() {
        return Ok(());
    }
    let path = grok_home.join(GROK_ATTRIBUTION_FILE);
    let existing = fs::read_to_string(&path).unwrap_or_default();
    if existing.contains(GROK_ATTRIBUTION_BEGIN) {
        return Ok(());
    }
    let mut out = existing.trim_end().to_string();
    if out.is_empty() {
        out.push_str("# AGENTS.md\n");
    }
    out.push_str("\n\n");
    out.push_str(GROK_ATTRIBUTION_BEGIN);
    out.push('\n');
    out.push_str(NO_ATTRIBUTION_BODY.trim());
    out.push('\n');
    out.push_str(GROK_ATTRIBUTION_END);
    out.push('\n');
    write_text_file(&path, &out)?;
    tracing::debug!(path = %path.display(), "staged Grok attribution-off guidance");
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn with_keep_unset<F: FnOnce()>(f: F) {
        let prev = std::env::var_os("AGENTPACK_KEEP_ATTRIBUTION");
        std::env::remove_var("AGENTPACK_KEEP_ATTRIBUTION");
        f();
        if let Some(v) = prev {
            std::env::set_var("AGENTPACK_KEEP_ATTRIBUTION", v);
        }
    }

    #[test]
    fn grok_attribution_adds_agents_guidance_idempotently() {
        with_keep_unset(|| {
            let dir = tempfile::tempdir().unwrap();
            force_grok_attribution_off(dir.path()).unwrap();
            force_grok_attribution_off(dir.path()).unwrap();
            let text = std::fs::read_to_string(dir.path().join("AGENTS.md")).unwrap();
            assert!(text.contains("Do not add any AI-attribution lines"));
            assert_eq!(text.matches(GROK_ATTRIBUTION_BEGIN).count(), 1);
        });
    }
}
