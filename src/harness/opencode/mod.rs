mod hooks;

use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::Context;
use serde_json::Value;

use super::{require, Harness, HarnessTarget, LaunchCtx, StageCtx};
use crate::error::{AgentpackError, Result};
use crate::fs_util::{read_json_value_opt, write_json_value, write_text_file};
use crate::hooks::capabilities::SupportLevel;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::render::HookRenderer;
use crate::launcher::common::resolve_harness_binary;
use crate::paths::staging_opencode_dir_for_mode;
use crate::staging::mcp::{merge_into_opencode_config, StagedMcpEntries};
use crate::staging::{copy_selected_entries, keep_attribution, NO_ATTRIBUTION_BODY};

/// OpenCode config-root entries preserved before overlaying pack content.
const OPENCODE_USER_ROOT_ENTRIES: &[&str] = &[
    "opencode.json",
    "agents",
    "commands",
    "modes",
    "plugins",
    "skills",
];
const OPENCODE_INSTRUCTIONS_FILE: &str = "agentpack-no-attribution.md";

/// OpenCode: launched with a redirected `OPENCODE_CONFIG_DIR`.
pub(super) struct OpenCode;

impl Harness for OpenCode {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::OpenCode
    }

    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf> {
        staging_opencode_dir_for_mode(project_root, mode)
    }

    fn prepare(&self, ctx: &StageCtx) -> Result<()> {
        let root = self.staged_root(ctx.project_root, ctx.mode.name())?;
        fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
        seed_opencode_root(&root)?;
        force_opencode_attribution_off(&root)
    }

    fn write_mcp(&self, merged: &StagedMcpEntries, ctx: &StageCtx) -> Result<()> {
        let root = self.staged_root(ctx.project_root, ctx.mode.name())?;
        merge_into_opencode_config(&root.join("opencode.json"), merged)
    }

    fn hook_support(&self, event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel {
        hooks::opencode_support(event, handler)
    }

    fn hook_asset_root(&self, target_root: &Path) -> PathBuf {
        target_root.join("plugins/agentpack-hooks/assets")
    }

    fn hook_output(&self, _event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
        serde_json::to_value(result).unwrap_or(Value::Null)
    }

    fn hook_renderer(&self) -> Option<Box<dyn HookRenderer>> {
        Some(Box::new(hooks::OpenCodeHookRenderer))
    }

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let root = staging_opencode_dir_for_mode(ctx.project_root, ctx.mode.name())?;
        require(root.is_dir(), || {
            format!("opencode staging missing {}", root.display())
        })
    }

    fn launch_command(&self, ctx: LaunchCtx) -> anyhow::Result<Command> {
        let config_dir = self.staged_root(ctx.project_root, ctx.mode.name())?;
        ctx.ui
            .debug_message(format!("OpenCode config dir: {}", config_dir.display()));
        if ctx.yolo {
            apply_yolo_opencode_config(&config_dir)?;
        }
        let opencode = resolve_harness_binary("OPENCODE_PATH", "opencode").with_context(|| {
            "OpenCode CLI (`opencode`) not found.\n\
             Install OpenCode and ensure `opencode` is on your PATH, or set OPENCODE_PATH to the executable."
        })?;
        let mut cmd = Command::new(&opencode);
        cmd.env("OPENCODE_CONFIG_DIR", config_dir);
        cmd.args(ctx.passthrough);
        Ok(cmd)
    }
}

/// Seed `$OPENCODE_CONFIG_DIR` from the user's real `~/.config/opencode`, then ensure a config stub.
fn seed_opencode_root(root: &Path) -> Result<()> {
    let Some(home) = dirs::home_dir() else {
        write_opencode_config_stub(root)?;
        return Ok(());
    };
    let user_root = home.join(".config").join("opencode");
    copy_selected_entries(&user_root, root, OPENCODE_USER_ROOT_ENTRIES)?;
    write_opencode_config_stub(root)?;
    Ok(())
}

fn write_opencode_config_stub(root: &Path) -> Result<()> {
    let config_path = root.join("opencode.json");
    if config_path.exists() {
        return Ok(());
    }
    let value = serde_json::json!({ "$schema": "https://opencode.ai/config.json" });
    write_json_value(&config_path, &value)
}

/// Force-disable OpenCode attribution by writing an instruction file and adding it to the
/// `instructions` array in `opencode.json`. OpenCode has no first-class attribution setting
/// (sst/opencode#919, sst/opencode#1135) so this is a system-prompt nudge.
fn force_opencode_attribution_off(root: &Path) -> Result<()> {
    if keep_attribution() {
        return Ok(());
    }
    let instructions_path = root.join(OPENCODE_INSTRUCTIONS_FILE);
    write_text_file(&instructions_path, NO_ATTRIBUTION_BODY)?;

    let config_path = root.join("opencode.json");
    let mut value = read_json_value_opt(&config_path)?.unwrap_or_else(|| serde_json::json!({}));
    if !value.is_object() {
        value = serde_json::json!({});
    }
    let obj = value.as_object_mut().expect("ensured object above");
    let entry = obj
        .entry("instructions".to_string())
        .or_insert_with(|| Value::Array(Vec::new()));
    if !entry.is_array() {
        *entry = Value::Array(Vec::new());
    }
    let arr = entry.as_array_mut().expect("ensured array above");
    let already = arr
        .iter()
        .any(|v| v.as_str() == Some(OPENCODE_INSTRUCTIONS_FILE));
    if !already {
        arr.push(Value::String(OPENCODE_INSTRUCTIONS_FILE.to_string()));
    }
    write_json_value(&config_path, &value)?;
    tracing::debug!(path = %config_path.display(), "forced OpenCode attribution off via instructions[]");
    Ok(())
}

/// OpenCode has no CLI flag for bypassing permissions; it reads `permission` from `opencode.json`.
/// Patch the staged config so `agentpack --yolo opencode` actually skips prompts. This is a
/// staged-file mutation, not an arg.
fn apply_yolo_opencode_config(config_dir: &Path) -> anyhow::Result<()> {
    let config_path = config_dir.join("opencode.json");
    let mut value = read_json_value_opt(&config_path)?.unwrap_or_else(|| serde_json::json!({}));
    let Some(obj) = value.as_object_mut() else {
        anyhow::bail!(
            "staged {} is not a JSON object; cannot apply --yolo",
            config_path.display()
        );
    };
    obj.insert("permission".into(), serde_json::json!("allow"));
    write_json_value(&config_path, &value)?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn opencode_attribution_adds_instructions_entry_idempotently() {
        let prev = std::env::var_os("AGENTPACK_KEEP_ATTRIBUTION");
        std::env::remove_var("AGENTPACK_KEEP_ATTRIBUTION");
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join("opencode.json"),
            r#"{"$schema":"https://opencode.ai/config.json","instructions":["docs/team.md"]}"#,
        )
        .unwrap();
        force_opencode_attribution_off(dir.path()).unwrap();
        force_opencode_attribution_off(dir.path()).unwrap();
        let v = read_json_value_opt(&dir.path().join("opencode.json"))
            .unwrap()
            .unwrap();
        let arr = v["instructions"].as_array().unwrap();
        assert!(arr.iter().any(|x| x == "docs/team.md"));
        let count = arr
            .iter()
            .filter(|x| x.as_str() == Some(OPENCODE_INSTRUCTIONS_FILE))
            .count();
        assert_eq!(count, 1);
        assert!(dir.path().join(OPENCODE_INSTRUCTIONS_FILE).is_file());
        if let Some(v) = prev {
            std::env::set_var("AGENTPACK_KEEP_ATTRIBUTION", v);
        }
    }
}
