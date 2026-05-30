mod overlay;

use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::Context;
use serde_json::Value;

use super::launch::{apply_yolo_agy, args_have_flag_with_value, resolve_harness_binary};
use super::{require, Harness, HarnessTarget, LaunchCtx, StageCtx};
use crate::artifacts::ArtifactKind;
use crate::error::{AgentpackError, Result};
use crate::fs_util::{write_json_value, write_text_file};
use crate::hooks::capabilities::SupportLevel;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::runtime::output::codex_output;
use crate::paths::{staging_agy_bundle_dir_for_mode, staging_agy_dir_for_mode};
use crate::staging::mcp::{McpServerEntry, StagedMcpEntries};
use crate::staging::{keep_attribution, NO_ATTRIBUTION_BODY};

const AGY_ATTRIBUTION_RULE_FILE: &str = "agentpack-no-attribution.md";

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
        overlay::cleanup_agy_overlay(ctx.project_root)?;
        let bundle = self.staged_root(ctx.project_root, ctx.mode.name())?;
        fs::create_dir_all(&bundle).map_err(|e| AgentpackError::io(&bundle, e))?;
        write_agy_plugin_manifest(&bundle)?;
        force_agy_attribution_off(&bundle)
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
            for rel in overlay::read_agy_overlay_manifest(ctx.project_root)? {
                let tracked = ctx.project_root.join(rel);
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
                [
                    "--add-dir".to_string(),
                    ctx.project_root.display().to_string(),
                ],
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
        let entries =
            overlay::materialize_workspace_agy_plugin_symlink(ctx.project_root, ctx.mode.name())?;
        overlay::write_overlay_manifest(ctx.project_root, &entries)
    }
}

fn write_agy_plugin_manifest(bundle: &Path) -> Result<()> {
    write_json_value(
        &bundle.join("plugin.json"),
        &serde_json::json!({ "name": "agentpack-bundle" }),
    )
}

/// Antigravity has no confirmed first-class attribution setting. Stage a plugin-local always-apply
/// rule as prompt-level guidance only.
fn force_agy_attribution_off(bundle: &Path) -> Result<()> {
    if keep_attribution() {
        return Ok(());
    }
    let path = bundle.join("rules").join(AGY_ATTRIBUTION_RULE_FILE);
    let body = format!(
        "---\ndescription: Disable AI attribution footers\nalwaysApply: true\n---\n\n{}\n",
        NO_ATTRIBUTION_BODY.trim()
    );
    write_text_file(&path, &body)?;
    tracing::debug!(path = %path.display(), "staged Antigravity attribution-off rule");
    Ok(())
}

/// Write Antigravity's `mcp_config.json` (`{"mcpServers": …}` with `serverUrl` for remotes).
fn write_agy_mcp_config_json(dest: &Path, merged: &StagedMcpEntries) -> Result<()> {
    let entries: serde_json::Map<String, Value> = merged
        .iter()
        .map(|(name, (entry, _))| (name.clone(), agy_entry_value(entry)))
        .collect();
    let cfg = serde_json::json!({ "mcpServers": entries });
    let json = serde_json::to_string_pretty(&cfg)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", dest.display())))?;
    write_text_file(dest, &json)
}

fn agy_entry_value(entry: &McpServerEntry) -> Value {
    use serde_json::json;
    let mut obj = serde_json::Map::new();
    if entry.is_remote() {
        if let Some(url) = &entry.url {
            obj.insert("serverUrl".into(), json!(url));
        }
    } else {
        if let Some(command) = &entry.command {
            obj.insert("command".into(), json!(command));
        }
        if !entry.args.is_empty() {
            obj.insert("args".into(), json!(entry.args));
        }
        if !entry.env.is_empty() {
            let env_obj: serde_json::Map<String, Value> = entry
                .env
                .iter()
                .map(|(k, v)| (k.clone(), json!(v)))
                .collect();
            obj.insert("env".into(), Value::Object(env_obj));
        }
    }
    if let Some(disabled) = entry.disabled {
        obj.insert("disabled".into(), json!(disabled));
    }
    Value::Object(obj)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::staging::mcp::test_support::{merged, remote_entry, stdio_entry};

    #[test]
    fn agy_mcp_remote_uses_server_url() {
        let dir = tempfile::tempdir().unwrap();
        let cfg = dir.path().join("mcp_config.json");
        write_agy_mcp_config_json(&cfg, &merged(&[("linear", remote_entry())])).unwrap();
        let v: Value = serde_json::from_str(&std::fs::read_to_string(&cfg).unwrap()).unwrap();
        let entry = &v["mcpServers"]["linear"];
        assert_eq!(entry["serverUrl"], "https://mcp.example.com/mcp");
        assert!(entry.get("url").is_none());
        assert!(entry.get("httpUrl").is_none());
    }

    #[test]
    fn agy_mcp_local_uses_command_args_env() {
        let dir = tempfile::tempdir().unwrap();
        let cfg = dir.path().join("mcp_config.json");
        write_agy_mcp_config_json(&cfg, &merged(&[("codesight", stdio_entry())])).unwrap();
        let v: Value = serde_json::from_str(&std::fs::read_to_string(&cfg).unwrap()).unwrap();
        let entry = &v["mcpServers"]["codesight"];
        assert_eq!(entry["command"], "cargo");
        assert_eq!(entry["args"][0], "run");
        assert_eq!(entry["env"]["RUST_LOG"], "info");
    }
}
