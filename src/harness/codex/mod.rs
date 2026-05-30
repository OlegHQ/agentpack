mod auth;
mod hooks;

use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::Context;
use serde_json::Value;

use super::launch::{apply_yolo_codex, resolve_harness_binary};
use super::{require, Harness, HarnessTarget, LaunchCtx, StageCtx};
use crate::artifacts::ArtifactKind;
use crate::error::{AgentpackError, Result};
use crate::hooks::capabilities::SupportLevel;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::render::HookRenderer;
use crate::hooks::runtime::output::codex_output;
use crate::paths::staging_codex_home_dir_for_mode;
use crate::staging::mcp::{merge_into_toml_mcp_config, StagedMcpEntries};
use crate::staging::{copy_selected_entries, keep_attribution};

/// Codex home entries preserved before overlaying pack content. `auth.json` is linked separately
/// so every staged `CODEX_HOME` shares the same refresh state.
const CODEX_HOME_ENTRIES: &[&str] = &["config.toml", "hooks.json", "skills", "themes"];

/// Codex: launched with a redirected `CODEX_HOME`; pack content rendered as portable skills.
pub(super) struct Codex;

impl Harness for Codex {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Codex
    }

    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf> {
        staging_codex_home_dir_for_mode(project_root, mode)
    }

    fn prepare(&self, ctx: &StageCtx) -> Result<()> {
        let root = self.staged_root(ctx.project_root, ctx.mode.name())?;
        fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
        seed_codex_home(&root)?;
        force_codex_attribution_off(&root)
    }

    fn write_mcp(&self, merged: &StagedMcpEntries, ctx: &StageCtx) -> Result<()> {
        let home = self.staged_root(ctx.project_root, ctx.mode.name())?;
        merge_into_toml_mcp_config(&home.join("config.toml"), merged)
    }

    fn hook_support(&self, event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel {
        hooks::codex_support(event, handler)
    }

    fn hook_output(&self, _event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
        codex_output(result)
    }

    fn hook_renderer(&self) -> Option<Box<dyn HookRenderer>> {
        Some(Box::new(hooks::CodexHookRenderer))
    }

    fn rendered_artifact_kind(&self, _source: ArtifactKind) -> ArtifactKind {
        // Codex only has a skills surface: commands, agents, and rules all fold into skills.
        ArtifactKind::Skill
    }

    fn stages_command_agent_trees(&self) -> bool {
        false
    }

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let root = staging_codex_home_dir_for_mode(ctx.project_root, ctx.mode.name())?;
        require(root.is_dir(), || {
            format!("codex home staging missing {}", root.display())
        })
    }

    fn launch_command(&self, ctx: LaunchCtx) -> anyhow::Result<Command> {
        let mut passthrough = ctx.passthrough;
        if ctx.yolo {
            apply_yolo_codex(&mut passthrough);
        }
        let codex_home = self.staged_root(ctx.project_root, ctx.mode.name())?;
        ctx.ui
            .debug_message(format!("Codex home: {}", codex_home.display()));
        let codex = resolve_harness_binary("CODEX_PATH", "codex").with_context(|| {
            "Codex CLI (`codex`) not found.\n\
             Install the Codex CLI and ensure `codex` is on your PATH, or set CODEX_PATH to the executable."
        })?;
        let mut cmd = Command::new(&codex);
        cmd.env("CODEX_HOME", codex_home);
        cmd.args(passthrough);
        Ok(cmd)
    }
}

/// Seed `$CODEX_HOME` from the user's real `~/.codex`, then bridge credentials so staged homes
/// share one refresh-token file (Codex keys keychain accounts by the canonical home path).
fn seed_codex_home(root: &Path) -> Result<()> {
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };
    let user_root = home.join(".codex");
    copy_selected_entries(&user_root, root, CODEX_HOME_ENTRIES)?;
    auth::prepare_staged_codex_auth(&user_root, root)?;
    auth::force_staged_codex_credentials_store_to_file(root)?;
    Ok(())
}

/// Force-disable Codex commit attribution in `<codex_home>/config.toml`.
fn force_codex_attribution_off(codex_home: &Path) -> Result<()> {
    if keep_attribution() {
        return Ok(());
    }
    let path = codex_home.join("config.toml");
    let mut value = crate::fs_util::read_toml_value_or_default(&path)?;
    let Some(table) = value.as_table_mut() else {
        return Ok(());
    };
    table.insert(
        "commit_attribution".into(),
        toml::Value::String(String::new()),
    );
    let out = toml::to_string(&value)
        .map_err(|e| AgentpackError::Staging(format!("serialize {}: {e}", path.display())))?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    fs::write(&path, out).map_err(|e| AgentpackError::io(&path, e))?;
    tracing::debug!(path = %path.display(), "forced Codex commit_attribution off");
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
    fn codex_attribution_inserts_top_level_field() {
        with_keep_unset(|| {
            let dir = tempfile::tempdir().unwrap();
            std::fs::write(dir.path().join("config.toml"), "model = \"o-vega\"\n").unwrap();
            force_codex_attribution_off(dir.path()).unwrap();
            let s = std::fs::read_to_string(dir.path().join("config.toml")).unwrap();
            assert!(s.contains("commit_attribution = \"\""));
            assert!(s.contains("model = \"o-vega\""));
        });
    }

    #[test]
    fn codex_attribution_creates_missing_config() {
        with_keep_unset(|| {
            let dir = tempfile::tempdir().unwrap();
            force_codex_attribution_off(dir.path()).unwrap();
            let s = std::fs::read_to_string(dir.path().join("config.toml")).unwrap();
            assert!(s.contains("commit_attribution = \"\""));
        });
    }
}
