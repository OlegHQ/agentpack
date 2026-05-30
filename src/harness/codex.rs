use std::fs;
use std::path::{Path, PathBuf};

use serde_json::Value;

use super::{require, Harness, HarnessTarget, StageCtx};
use crate::artifacts::ArtifactKind;
use crate::error::{AgentpackError, Result};
use crate::hooks::capabilities::{codex_support, SupportLevel};
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::render::{CodexHookRenderer, HookRenderer};
use crate::hooks::runtime::translate::codex_output;
use crate::paths::staging_codex_home_dir_for_mode;
use crate::staging::mcp::{merge_into_toml_mcp_config, StagedMcpEntries};
use crate::staging::{force_codex_attribution_off, seed_codex_home};

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
        codex_support(event, handler)
    }

    fn hook_output(&self, _event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
        codex_output(result)
    }

    fn hook_renderer(&self) -> Option<Box<dyn HookRenderer>> {
        Some(Box::new(CodexHookRenderer))
    }

    fn rendered_artifact_kind(&self, _source: ArtifactKind) -> ArtifactKind {
        // Codex only has a skills surface: commands, agents, and rules all fold into skills.
        ArtifactKind::Skill
    }

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let root = staging_codex_home_dir_for_mode(ctx.project_root, ctx.mode.name())?;
        require(root.is_dir(), || {
            format!("codex home staging missing {}", root.display())
        })
    }
}
