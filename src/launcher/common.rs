use std::ffi::OsString;
use std::io;
use std::path::{Path, PathBuf};
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
    let path_os = std::env::var_os("PATH").ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::NotFound,
            "PATH environment variable is not set",
        )
    })?;
    for dir in std::env::split_paths(&path_os) {
        for candidate in executable_candidates(&dir, program) {
            if candidate.is_file() {
                return Ok(candidate);
            }
        }
    }
    Err(io::Error::new(
        io::ErrorKind::NotFound,
        format!("`{program}` not found in PATH"),
    ))
}

#[cfg(unix)]
fn executable_candidates(dir: &Path, program: &str) -> Vec<PathBuf> {
    vec![dir.join(program)]
}

#[cfg(windows)]
fn executable_candidates(dir: &Path, program: &str) -> Vec<PathBuf> {
    let mut out = Vec::new();
    let base = dir.join(program);
    out.push(base.clone());
    if Path::new(program).extension().is_none() {
        for ext in pathext_suffixes() {
            out.push(dir.join(format!("{program}{ext}")));
        }
    }
    out
}

#[cfg(windows)]
fn pathext_suffixes() -> Vec<String> {
    match std::env::var("PATHEXT") {
        Ok(raw) => raw
            .split(';')
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect(),
        Err(_) => vec![".EXE".into(), ".BAT".into(), ".CMD".into(), ".COM".into()],
    }
}

/// Reads optional **`env_key`**; blank / unset falls back to **`default_cmd`**, then resolves on **`PATH`**.
pub fn resolve_harness_binary(env_key: &str, default_cmd: &str) -> anyhow::Result<PathBuf> {
    let raw = std::env::var(env_key).unwrap_or_default();
    let program = if raw.trim().is_empty() {
        default_cmd.to_string()
    } else {
        raw.trim().to_string()
    };
    resolve_program(&program).with_context(|| format!("could not find `{program}`"))
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
    resolved_exe: &Path,
    envs: &[(&str, OsString)],
    passthrough: Vec<String>,
) -> anyhow::Result<()> {
    let mut cmd = Command::new(resolved_exe);
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
