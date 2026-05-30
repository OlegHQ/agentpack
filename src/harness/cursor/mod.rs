mod approvals;
mod fake_home;
mod hooks;
mod manifests;
mod overlay;

use std::ffi::OsString;
use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::Context;
use serde_json::{json, Value};
use serde_norway::Mapping;

use super::{require, Harness, HarnessTarget, LaunchCtx, StageCtx};
use crate::artifacts::yaml::insert_string;
use crate::artifacts::ArtifactKind;
use crate::error::{AgentpackError, Result};
use crate::fs_util::{read_json_value_opt, remove_path_any, write_json_value};
use crate::hooks::capabilities::SupportLevel;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::render::HookRenderer;
use crate::hooks::runtime::output::cursor_output;
use crate::launcher::common::{apply_yolo_cursor_agent, resolve_harness_binary};
use crate::paths::{
    cursor_workspace_dir, staging_cursor_bundle_dir_for_mode, staging_cursor_home_dir_for_mode,
    staging_cursor_pack_plugin_dir_for_mode,
};
use crate::staging::copy_selected_entries;
use crate::staging::keep_attribution;
use crate::staging::mcp::{write_mcp_servers_json, StagedMcpEntries};

/// Cursor files copied from `~/.cursor` into `$STAGING/cursor/` before pack overlay. Omit
/// `agents`/`commands`/`skills`/`rules` — those come from `pack.lock`.
const CURSOR_USER_ROOT_ENTRIES: &[&str] = &["cli-config.json", "mcp.json"];
/// Top-level `~/.cursor` paths symlinked into `$STAGING/cursor-home/.cursor` for Cursor Agent auth.
pub(super) const CURSOR_FAKE_HOME_CREDENTIAL_FILES: &[&str] = &[
    "cli-config.json",
    "machineid",
    "agent-cli-state.json",
    "argv.json",
    "ide_state.json",
];
/// Symlinked into `$FAKE_HOME/.cursor/User/` so they resolve to the same trees Cursor's GUI/CLI use
/// for workspace trust (`state.vscdb` under `workspaceStorage`) and global state.
pub(super) const CURSOR_USER_SUBDIRS_IN_FAKE_HOME: &[&str] = &["globalStorage", "workspaceStorage"];
/// Pack plugin dirs symlinked from `agentpack-bundle/` into `$STAGING/cursor-home/.cursor`.
pub(super) const CURSOR_FAKE_HOME_PACK_SUBDIRS: &[&str] = &[
    "commands", "agents", "skills", "rules", "hooks", "assets", "scripts",
];
/// Relative to `./.cursor/` — symlink `./.cursor/agents` → staged pack agents for Cursor `agent`.
pub(super) const CURSOR_WORKSPACE_AGENTS_OVERLAY: &str = "agents";

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
        overlay::cleanup_cursor_overlay(ctx.project_root)?;
        let cursor_bundle = staging_cursor_bundle_dir_for_mode(ctx.project_root, mode)?;
        fs::create_dir_all(&cursor_bundle).map_err(|e| AgentpackError::io(&cursor_bundle, e))?;
        seed_cursor_root(&cursor_bundle)?;
        let cursor_pack = self.staged_root(ctx.project_root, mode)?;
        fs::create_dir_all(&cursor_pack).map_err(|e| AgentpackError::io(&cursor_pack, e))?;
        manifests::write_cursor_pack_plugin_manifests(&cursor_bundle)?;
        force_cursor_attribution_off(&cursor_bundle)?;
        force_cursor_attribution_off(&cursor_pack)
    }

    fn write_mcp(&self, merged: &StagedMcpEntries, ctx: &StageCtx) -> Result<()> {
        // Only the pack `mcp.json`; the fake-HOME re-merge with the user's `~/.cursor/mcp.json`
        // stays in finalize (materialize_cursor_fake_home).
        let pack = self.staged_root(ctx.project_root, ctx.mode.name())?;
        write_mcp_servers_json(&pack.join("mcp.json"), merged)
    }

    fn finalize(&self, merged: &StagedMcpEntries, ctx: &StageCtx) -> Result<()> {
        // After pack content is staged: write the pack README, build the fake HOME (symlinks pack
        // dirs), and pre-seed `~/.cursor` MCP approvals from the merged set.
        let mode = ctx.mode.name();
        manifests::write_cursor_pack_plugin_readme(&self.staged_root(ctx.project_root, mode)?)?;
        fake_home::materialize_cursor_fake_home(ctx.project_root, mode)?;
        approvals::seed_workspace_mcp_approvals(ctx.project_root, merged)
    }

    fn finalize_workspace_overlay(&self, ctx: &StageCtx) -> Result<()> {
        let entries = overlay::materialize_workspace_cursor_agents_symlink(
            ctx.project_root,
            ctx.mode.name(),
        )?;
        overlay::write_overlay_manifest(ctx.project_root, &entries)
    }

    fn hook_support(&self, event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel {
        hooks::cursor_support(event, handler)
    }

    fn hook_output(&self, event: ClaudeEvent, result: &NormalizedHookResult) -> Value {
        cursor_output(event, result)
    }

    fn hook_renderer(&self) -> Option<Box<dyn HookRenderer>> {
        Some(Box::new(hooks::CursorHookRenderer))
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
                    bundle_root
                        .join(".cursor-plugin/marketplace.json")
                        .display()
                )
            },
        )?;
        require(home.join(".cursor").is_dir(), || {
            format!("cursor fake home missing .cursor/ under {}", home.display())
        })?;
        if ctx.launch_target == Some(HarnessTarget::Cursor) {
            for rel in overlay::read_cursor_overlay_manifest(ctx.project_root)? {
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
}

/// Seed `$STAGING/cursor` from the user's real `~/.cursor` (`cli-config.json`, `mcp.json` only).
fn seed_cursor_root(root: &Path) -> Result<()> {
    let Some(home) = dirs::home_dir() else {
        return Ok(());
    };
    let user_root = home.join(".cursor");
    copy_selected_entries(&user_root, root, CURSOR_USER_ROOT_ENTRIES)
}

/// Patch a Cursor `cli-config.json` value: force `attribution.attributeCommitsToAgent` and
/// `attribution.attributePRsToAgent` to `false`. Returns the modified JSON.
fn patch_cursor_cli_config(mut value: Value) -> Value {
    if !value.is_object() {
        value = json!({});
    }
    let obj = value.as_object_mut().expect("ensured object above");
    let attribution = obj
        .entry("attribution".to_string())
        .or_insert_with(|| json!({}));
    if !attribution.is_object() {
        *attribution = json!({});
    }
    let attr_obj = attribution.as_object_mut().expect("ensured object above");
    attr_obj.insert("attributeCommitsToAgent".into(), Value::Bool(false));
    attr_obj.insert("attributePRsToAgent".into(), Value::Bool(false));
    value
}

/// Force-disable Cursor attribution in `<root>/cli-config.json`, preserving user fields.
fn force_cursor_attribution_off(root: &Path) -> Result<()> {
    if keep_attribution() {
        return Ok(());
    }
    let path = root.join("cli-config.json");
    let value = read_json_value_opt(&path)?.unwrap_or_else(|| json!({}));
    let patched = patch_cursor_cli_config(value);
    write_json_value(&path, &patched)?;
    tracing::debug!(path = %path.display(), "forced Cursor attribution off");
    Ok(())
}

/// Materialize a non-symlink Cursor `cli-config.json` inside the fake-home so writes from agentpack
/// don't bleed back into the user's real `~/.cursor/cli-config.json`.
pub(super) fn force_cursor_fake_home_attribution_off(
    fake_cursor: &Path,
    real_cursor_cli_config: Option<&Path>,
) -> Result<()> {
    if keep_attribution() {
        return Ok(());
    }
    let dest = fake_cursor.join("cli-config.json");
    remove_path_any(&dest)?;
    let base = match real_cursor_cli_config {
        Some(p) => read_json_value_opt(p)?.unwrap_or_else(|| json!({})),
        None => json!({}),
    };
    let patched = patch_cursor_cli_config(base);
    write_json_value(&dest, &patched)?;
    tracing::debug!(path = %dest.display(), "forced Cursor fake-home attribution off");
    Ok(())
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
    fn cursor_attribution_writes_both_flags() {
        with_keep_unset(|| {
            let dir = tempfile::tempdir().unwrap();
            force_cursor_attribution_off(dir.path()).unwrap();
            let v = read_json_value_opt(&dir.path().join("cli-config.json"))
                .unwrap()
                .unwrap();
            assert_eq!(v["attribution"]["attributeCommitsToAgent"], false);
            assert_eq!(v["attribution"]["attributePRsToAgent"], false);
        });
    }

    #[test]
    fn cursor_fake_home_breaks_symlink_via_real_copy() {
        with_keep_unset(|| {
            let dir = tempfile::tempdir().unwrap();
            let real = dir.path().join("real-cli-config.json");
            std::fs::write(
                &real,
                r#"{"editor":{"vimMode":true},"attribution":{"attributeCommitsToAgent":true}}"#,
            )
            .unwrap();
            let fake = dir.path().join("fake/.cursor");
            std::fs::create_dir_all(&fake).unwrap();
            force_cursor_fake_home_attribution_off(&fake, Some(&real)).unwrap();
            let v = read_json_value_opt(&fake.join("cli-config.json"))
                .unwrap()
                .unwrap();
            assert_eq!(v["editor"]["vimMode"], true);
            assert_eq!(v["attribution"]["attributeCommitsToAgent"], false);
            assert_eq!(v["attribution"]["attributePRsToAgent"], false);
            // Source untouched.
            let src = std::fs::read_to_string(&real).unwrap();
            assert!(src.contains("\"attributeCommitsToAgent\":true"));
        });
    }
}
