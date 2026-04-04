use std::ffi::OsString;
use std::path::Path;
use std::process::Command;

use anyhow::Context;

fn args_contain_any(args: &[String], needles: &[&str]) -> bool {
    args.iter().any(|a| needles.contains(&a.as_str()))
}

/// Injects Claude Code / OpenCode **`--dangerously-skip-permissions`** when **`agentpack --yolo`** is set.
pub fn apply_yolo_claude_opencode(args: &mut Vec<String>) {
    const FLAG: &str = "--dangerously-skip-permissions";
    if args_contain_any(args, &[FLAG]) {
        return;
    }
    args.insert(0, FLAG.into());
}

/// Injects Codex **`--dangerously-bypass-approvals-and-sandbox`** (alias **`--yolo`**) when **`agentpack --yolo`** is set.
/// Codex expects global flags **after** a subcommand (for example `codex exec --flag …`); if the first arg is a subcommand token, the flag is inserted in second position.
pub fn apply_yolo_codex(args: &mut Vec<String>) {
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
pub fn apply_yolo_cursor_agent(args: &mut Vec<String>) {
    if args_contain_any(args, &["--force", "--yolo"]) {
        return;
    }
    args.insert(0, "--force".into());
}

/// Replace the current process with **`cmd`** on Unix, or run it to completion on Windows.
pub fn exec_inherit(mut cmd: Command) -> anyhow::Result<()> {
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

pub fn exec_with_env(
    executable_env: &str,
    default_executable: &str,
    envs: &[(&str, OsString)],
    passthrough: Vec<String>,
) -> anyhow::Result<()> {
    let executable =
        std::env::var(executable_env).unwrap_or_else(|_| default_executable.to_string());
    let mut cmd = Command::new(&executable);
    for (key, value) in envs {
        cmd.env(key, value);
    }
    cmd.args(passthrough);
    exec_inherit(cmd)
}

pub fn single_dir_override(env_key: &str, staged_path: &Path) -> OsString {
    std::env::var_os(env_key).unwrap_or_else(|| staged_path.as_os_str().to_os_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn yolo_claude_prepends_once() {
        let mut a = vec!["chat".into()];
        apply_yolo_claude_opencode(&mut a);
        assert_eq!(a, vec!["--dangerously-skip-permissions", "chat"]);
        apply_yolo_claude_opencode(&mut a);
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
}
