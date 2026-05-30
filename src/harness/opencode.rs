use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::Context;
use serde_json::Value;

use super::{require, Harness, HarnessTarget, LaunchCtx, StageCtx};
use crate::error::{AgentpackError, Result};
use crate::fs_util::{read_json_value_opt, write_json_value};
use crate::hooks::capabilities::{opencode_support, SupportLevel};
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::render::{HookRenderer, OpenCodeHookRenderer};
use crate::launcher::common::resolve_harness_binary;
use crate::paths::staging_opencode_dir_for_mode;
use crate::staging::mcp::{merge_into_opencode_config, StagedMcpEntries};
use crate::staging::{force_opencode_attribution_off, seed_opencode_root};

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
        opencode_support(event, handler)
    }

    fn hook_asset_root(&self, target_root: &Path) -> PathBuf {
        target_root.join("plugins/agentpack-hooks/assets")
    }

    fn hook_output(&self, _event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
        serde_json::to_value(result).unwrap_or(Value::Null)
    }

    fn hook_renderer(&self) -> Option<Box<dyn HookRenderer>> {
        Some(Box::new(OpenCodeHookRenderer))
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

/// OpenCode has no CLI flag for bypassing permissions; it reads `permission` from `opencode.json`.
/// Patch the staged config so `agentpack --yolo opencode` actually skips prompts. This is a
/// staged-file mutation, not an arg (see HARNESS_TRAIT.md §4 Step 7).
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
