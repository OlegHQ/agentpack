use std::fs;
use std::path::{Path, PathBuf};

use serde_json::Value;

use super::{require, Harness, HarnessTarget, StageCtx};
use crate::error::{AgentpackError, Result};
use crate::hooks::capabilities::{opencode_support, SupportLevel};
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::render::{HookRenderer, OpenCodeHookRenderer};
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
}
