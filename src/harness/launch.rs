//! Launch dispatch + shared launch helpers.
//!
//! [`launch`] is the single entry point behind every `agentpack <harness>` subcommand: it syncs
//! staging, builds the per-harness [`Command`](std::process::Command) via
//! [`Harness::launch_command`](super::Harness::launch_command), and execs it. The yolo-flag
//! injectors, binary resolution, and exec wrappers below are the cross-harness utilities those
//! `launch_command` impls share.

use std::io;
use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::Context;

use super::{HarnessTarget, LaunchCtx};
use crate::sync::sync_for_launch;
use crate::ui::Ui;

/// Sync staging for `id`, then build and exec that harness's configured `Command`. The single
/// dispatch point for the six `agentpack <harness>` subcommands; each harness owns its own
/// `launch_command`.
pub(crate) fn launch(
    id: HarnessTarget,
    project_root: &Path,
    passthrough: Vec<String>,
    selected_mode: Option<&str>,
    yolo: bool,
    proxy: bool,
    ui: &Ui,
) -> anyhow::Result<()> {
    let mode = sync_for_launch(project_root, selected_mode, id, ui)?;
    let ctx = LaunchCtx {
        project_root,
        passthrough,
        mode: &mode,
        yolo,
        ui,
    };
    let mut cmd = id.harness().launch_command(ctx)?;
    if proxy {
        if id != HarnessTarget::Claude {
            return Err(anyhow::anyhow!("proxy launch is only supported for Claude"));
        }
        let running = crate::proxy::start(ui)?;
        running.apply_claude_env(&mut cmd);
        return spawn_inherit_and_wait(cmd, Some(running));
    }
    exec_inherit(cmd)
}

fn args_contain_any(args: &[String], needles: &[&str]) -> bool {
    args.iter().any(|a| needles.contains(&a.as_str()))
}

/// Whether `args` already supplies `flag` with a value, in either `--flag value` or `--flag=value`
/// form. Used by launchers that inject a default (e.g. `--cwd`, `--add-dir`) only when absent.
pub(super) fn args_have_flag_with_value(args: &[String], flag: &str) -> bool {
    let eq_prefix = format!("{flag}=");
    args.iter().enumerate().any(|(idx, arg)| {
        (arg == flag && args.get(idx + 1).is_some()) || arg.starts_with(&eq_prefix)
    })
}

/// The directory a workspace-based harness (Cursor `--workspace`, Antigravity `--add-dir`) should
/// treat as its workspace: the user's **CWD** where they invoked `agentpack` — *not* the pack root
/// (`project_root`, which in a monorepo may be a parent holding `agentpack.toml` above the subproject
/// they `cd`'d into). The workspace overlay (`.cursor/agents` / `.agents/plugins/...`) and the
/// harness's workspace trust follow this same dir. Falls back to `project_root` if the CWD can't be
/// read. Returns a canonicalized absolute path.
pub(crate) fn workspace_root(project_root: &Path) -> PathBuf {
    let raw = std::env::current_dir().unwrap_or_else(|_| project_root.to_path_buf());
    raw.canonicalize().unwrap_or(raw)
}

/// Injects Claude Code **`--dangerously-skip-permissions`** when **`agentpack --yolo`** is set.
pub(super) fn apply_yolo_claude(args: &mut Vec<String>) {
    const FLAG: &str = "--dangerously-skip-permissions";
    if args_contain_any(args, &[FLAG]) {
        return;
    }
    args.insert(0, FLAG.into());
}

/// Injects Codex **`--dangerously-bypass-approvals-and-sandbox`** (alias **`--yolo`**) when **`agentpack --yolo`** is set.
/// Codex expects global flags **after** a subcommand (for example `codex exec --flag …`); if the first arg is a subcommand token, the flag is inserted in second position.
pub(super) fn apply_yolo_codex(args: &mut Vec<String>) {
    const FLAG: &str = "--dangerously-bypass-approvals-and-sandbox";
    if args_contain_any(args, &[FLAG, "--yolo"]) {
        return;
    }
    if args.first().is_none_or(|a| a.starts_with('-')) {
        args.insert(0, FLAG.into());
    } else {
        args.insert(1, FLAG.into());
    }
}

/// Injects Cursor **`agent --force`** (YOLO / auto-approve) when **`agentpack --yolo`** is set.
pub(super) fn apply_yolo_cursor_agent(args: &mut Vec<String>) {
    if args_contain_any(args, &["--force", "--yolo"]) {
        return;
    }
    args.insert(0, "--force".into());
}

/// Injects Grok's auto-approve flag when **`agentpack --yolo`** is set.
pub(super) fn apply_yolo_grok(args: &mut Vec<String>) {
    const FLAG: &str = "--always-approve";
    if args_contain_any(args, &[FLAG]) {
        return;
    }
    args.insert(0, FLAG.into());
}

/// Injects Antigravity's permission bypass flag when **`agentpack --yolo`** is set.
pub(super) fn apply_yolo_agy(args: &mut Vec<String>) {
    const FLAG: &str = "--dangerously-skip-permissions";
    if args_contain_any(args, &[FLAG]) {
        return;
    }
    args.insert(0, FLAG.into());
}

fn program_has_separator(program: &str) -> bool {
    program.contains(std::path::MAIN_SEPARATOR) || program.contains('/')
}

/// Resolve **`program`**: absolute / relative path, or first match on **`PATH`** (Windows honors **`PATHEXT`**).
fn resolve_program(program: &str) -> io::Result<PathBuf> {
    if program.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::NotFound,
            "executable name is empty",
        ));
    }
    let path = Path::new(program);
    if path.is_absolute() || program_has_separator(program) {
        return if path.is_file() {
            Ok(path.to_path_buf())
        } else {
            Err(io::Error::new(
                io::ErrorKind::NotFound,
                format!("{} is not a file", program),
            ))
        };
    }
    search_path(program)
}

fn search_path(program: &str) -> io::Result<PathBuf> {
    // `which` handles PATH lookup, executability checks, and Windows PATHEXT resolution.
    which::which(program).map_err(|e| io::Error::new(io::ErrorKind::NotFound, e.to_string()))
}

/// Reads optional **`env_key`**; blank / unset falls back to **`default_cmd`**, then resolves on **`PATH`**.
pub(super) fn resolve_harness_binary(env_key: &str, default_cmd: &str) -> anyhow::Result<PathBuf> {
    let raw = std::env::var(env_key).unwrap_or_default();
    let program = if raw.trim().is_empty() {
        default_cmd.to_string()
    } else {
        raw.trim().to_string()
    };
    resolve_program(&program).with_context(|| format!("could not find `{program}`"))
}

/// Replace the current process with **`cmd`** on Unix, or run it to completion on Windows.
fn exec_inherit(mut cmd: Command) -> anyhow::Result<()> {
    let prog = cmd.get_program().to_string_lossy().into_owned();

    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        let err = cmd.exec();
        Err(anyhow::Error::new(err)).with_context(|| format!("failed to exec {prog}"))
    }

    #[cfg(not(unix))]
    {
        let status = cmd
            .status()
            .with_context(|| format!("failed to run {prog}"))?;
        std::process::exit(status.code().unwrap_or(1));
    }
}

fn spawn_inherit_and_wait(
    mut cmd: Command,
    proxy: Option<crate::proxy::RunningProxy>,
) -> anyhow::Result<()> {
    let prog = cmd.get_program().to_string_lossy().into_owned();
    let status = match cmd.status() {
        Ok(status) => status,
        Err(err) => {
            if let Some(proxy) = proxy {
                proxy.shutdown();
            }
            return Err(anyhow::Error::new(err)).with_context(|| format!("failed to run {prog}"));
        }
    };
    if let Some(proxy) = proxy {
        proxy.shutdown();
    }
    std::process::exit(status.code().unwrap_or(1));
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn yolo_claude_prepends_once() {
        let mut a = vec!["chat".into()];
        apply_yolo_claude(&mut a);
        assert_eq!(a, vec!["--dangerously-skip-permissions", "chat"]);
        apply_yolo_claude(&mut a);
        assert_eq!(a, vec!["--dangerously-skip-permissions", "chat"]);
    }

    #[test]
    fn yolo_codex_after_subcommand() {
        let mut a = vec!["exec".into(), "hi".into()];
        apply_yolo_codex(&mut a);
        assert_eq!(
            a,
            vec!["exec", "--dangerously-bypass-approvals-and-sandbox", "hi"]
        );
    }

    #[test]
    fn yolo_codex_first_when_no_subcommand() {
        let mut a: Vec<String> = vec![];
        apply_yolo_codex(&mut a);
        assert_eq!(a, vec!["--dangerously-bypass-approvals-and-sandbox"]);
    }

    #[test]
    fn yolo_cursor_prepends_force() {
        let mut a = vec!["--print".into(), "ok".into()];
        apply_yolo_cursor_agent(&mut a);
        assert_eq!(a, vec!["--force", "--print", "ok"]);
    }

    #[test]
    fn yolo_grok_prepends_always_approve() {
        let mut a = vec!["inspect".into()];
        apply_yolo_grok(&mut a);
        assert_eq!(a, vec!["--always-approve", "inspect"]);
    }

    #[test]
    fn yolo_agy_prepends_skip_permissions() {
        let mut a = vec!["--print".into(), "ok".into()];
        apply_yolo_agy(&mut a);
        assert_eq!(a, vec!["--dangerously-skip-permissions", "--print", "ok"]);
    }

    #[test]
    fn resolve_program_explicit_file() {
        let dir = tempfile::tempdir().unwrap();
        let exe = dir.path().join("stub-harness");
        std::fs::write(&exe, b"").unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(&exe, std::fs::Permissions::from_mode(0o755)).unwrap();
        }
        let resolved = resolve_program(exe.to_str().unwrap()).unwrap();
        assert_eq!(resolved, exe);
    }

    #[test]
    #[serial_test::serial]
    fn resolve_program_finds_on_path() {
        let dir = tempfile::tempdir().unwrap();
        let exe = dir.path().join("agentpack-resolve-test");
        std::fs::write(&exe, b"").unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(&exe, std::fs::Permissions::from_mode(0o755)).unwrap();
        }
        let old = std::env::var_os("PATH");
        std::env::set_var("PATH", dir.path().as_os_str());
        let got = resolve_program("agentpack-resolve-test").unwrap();
        assert_eq!(got, exe);
        match old {
            Some(v) => std::env::set_var("PATH", v),
            None => std::env::remove_var("PATH"),
        }
    }

    #[test]
    #[serial_test::serial]
    fn resolve_harness_binary_blank_env_uses_default_name() {
        let dir = tempfile::tempdir().unwrap();
        let exe = dir.path().join("agentpack-harness-default");
        std::fs::write(&exe, b"").unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(&exe, std::fs::Permissions::from_mode(0o755)).unwrap();
        }
        let old_path = std::env::var_os("PATH");
        let old_env = std::env::var_os("_AGENTPACK_TEST_HARNESS_PATH");
        std::env::set_var("PATH", dir.path().as_os_str());
        std::env::set_var("_AGENTPACK_TEST_HARNESS_PATH", " ");
        let got =
            resolve_harness_binary("_AGENTPACK_TEST_HARNESS_PATH", "agentpack-harness-default")
                .unwrap();
        assert_eq!(got, exe);
        match old_path {
            Some(v) => std::env::set_var("PATH", v),
            None => std::env::remove_var("PATH"),
        }
        match old_env {
            Some(v) => std::env::set_var("_AGENTPACK_TEST_HARNESS_PATH", v),
            None => std::env::remove_var("_AGENTPACK_TEST_HARNESS_PATH"),
        }
    }
}
