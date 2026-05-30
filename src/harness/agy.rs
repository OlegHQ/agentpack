use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::Context;
use serde_json::Value;

use super::{require, Harness, HarnessTarget, LaunchCtx, StageCtx};
use crate::artifacts::ArtifactKind;
use crate::error::{AgentpackError, Result};
use crate::hooks::capabilities::SupportLevel;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::runtime::translate::codex_output;
use crate::launcher::common::{apply_yolo_agy, args_have_flag_with_value, resolve_harness_binary};
use crate::paths::{staging_agy_bundle_dir_for_mode, staging_agy_dir_for_mode};
use crate::staging::mcp::{write_agy_mcp_config_json, StagedMcpEntries};
use crate::staging::{
    agy_workspace_overlay_paths, finalize_agy_staging, force_agy_attribution_off,
    prepare_agy_staging_without_pack_overlay,
};

/// Antigravity (`agy`): pack content reaches it via a workspace plugin overlay; `HOME` untouched.
pub(super) struct Agy;

impl Harness for Agy {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Agy
    }

    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf> {
        staging_agy_bundle_dir_for_mode(project_root, mode)
    }

    fn reset_paths(&self, project_root: &Path, mode: &str) -> Result<Vec<PathBuf>> {
        // Remove the parent `agy/` dir, not just `agy/agentpack-bundle`, matching prior behavior.
        Ok(vec![staging_agy_dir_for_mode(project_root, mode)?])
    }

    fn prepare(&self, ctx: &StageCtx) -> Result<()> {
        let mode = ctx.mode.name();
        prepare_agy_staging_without_pack_overlay(ctx.project_root, mode)?;
        force_agy_attribution_off(&self.staged_root(ctx.project_root, mode)?)
    }

    fn write_mcp(&self, merged: &StagedMcpEntries, ctx: &StageCtx) -> Result<()> {
        let bundle = self.staged_root(ctx.project_root, ctx.mode.name())?;
        write_agy_mcp_config_json(&bundle.join("mcp_config.json"), merged)
    }

    fn hook_support(&self, _event: ClaudeEvent, _handler: &ClaudeHandler) -> SupportLevel {
        SupportLevel::Unsupported {
            reason: "Antigravity hook rendering is gated until plugin-local hook runtime smoke tests pass",
        }
    }

    fn hook_output(&self, _event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
        codex_output(result)
    }

    fn raw_plugin_subdirs(&self) -> &'static [&'static str] {
        &["hooks", "commands", "agents", "rules", "skills"]
    }

    // Antigravity rejects Claude-only frontmatter, so it allows no extra keys on any artifact.
    fn command_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[]
    }

    fn skill_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[]
    }

    fn agent_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[]
    }

    fn rendered_artifact_kind(&self, source: ArtifactKind) -> ArtifactKind {
        match source {
            // Antigravity has native rule files, so rules stay rules.
            ArtifactKind::Rule => ArtifactKind::Rule,
            other => other,
        }
    }

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let agy_bundle = staging_agy_bundle_dir_for_mode(ctx.project_root, ctx.mode.name())?;
        require(agy_bundle.join("plugin.json").is_file(), || {
            format!(
                "agy bundle missing {}",
                agy_bundle.join("plugin.json").display()
            )
        })?;
        if ctx.launch_target == Some(HarnessTarget::Agy) {
            for tracked in agy_workspace_overlay_paths(ctx.project_root)? {
                if !tracked.exists() {
                    return Err(AgentpackError::Staging(format!(
                        "agy workspace overlay missing at {}",
                        tracked.display()
                    )));
                }
            }
        }
        Ok(())
    }

    fn launch_command(&self, ctx: LaunchCtx) -> anyhow::Result<Command> {
        let mut passthrough = ctx.passthrough;
        if !args_have_flag_with_value(&passthrough, "--add-dir") {
            passthrough.splice(
                0..0,
                ["--add-dir".to_string(), ctx.project_root.display().to_string()],
            );
        }
        if ctx.yolo {
            apply_yolo_agy(&mut passthrough);
        }
        ctx.ui.debug_message(format!(
            "Antigravity workspace (--add-dir): {}",
            ctx.project_root.display()
        ));
        let agy = resolve_harness_binary("AGY_PATH", "agy").with_context(|| {
            "Antigravity CLI (`agy`) not found.\n\
             Install Antigravity and ensure `agy` is on your PATH, or set AGY_PATH to the executable."
        })?;
        let mut cmd = Command::new(&agy);
        cmd.args(passthrough);
        Ok(cmd)
    }

    fn finalize_workspace_overlay(&self, ctx: &StageCtx) -> Result<()> {
        finalize_agy_staging(ctx.project_root, ctx.mode.name())
    }
}
