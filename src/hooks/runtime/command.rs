use std::process::{Command, Stdio};

use anyhow::{anyhow, Context};

use super::bridge::HookExecutionSpec;
use crate::hooks::ir::ClaudeHandler;

pub struct CommandHookOutput {
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
    pub exit_code: i32,
}

pub fn execute(spec: &HookExecutionSpec, stdin_bytes: &[u8]) -> anyhow::Result<CommandHookOutput> {
    let ClaudeHandler::Command(handler) = &spec.handler else {
        return Err(anyhow!("command executor received non-command hook"));
    };
    let mut cmd = shell_command(&handler.command);
    // Inherit the parent harness's CWD (session/project root) — matches Claude/Cursor's native
    // hook semantics. Overriding CWD to the staged plugin dir silently breaks project-local
    // cargo/npm/etc. config (`.cargo/config.toml`, `package.json` scripts) whose discovery is
    // CWD-rooted. Plugins that need their own directory should use `$CLAUDE_PLUGIN_ROOT`.
    cmd.env("CLAUDE_PLUGIN_ROOT", &spec.working_dir);
    cmd.env("AGENTPACK_PLUGIN_ROOT", &spec.working_dir);
    cmd.stdin(Stdio::piped());
    cmd.stdout(Stdio::piped());
    cmd.stderr(Stdio::piped());
    let mut child = cmd
        .spawn()
        .with_context(|| format!("spawn hook command `{}`", handler.command))?;
    if let Some(stdin) = child.stdin.as_mut() {
        use std::io::Write;
        stdin.write_all(stdin_bytes)?;
    }
    let output = child.wait_with_output()?;
    Ok(CommandHookOutput {
        stdout: output.stdout,
        stderr: output.stderr,
        exit_code: output.status.code().unwrap_or(1),
    })
}

fn shell_command(command: &str) -> Command {
    // Match Claude Code's native hook semantics: `/bin/sh -c <cmd>` on Unix,
    // `cmd /C <cmd>` on Windows. Using `$SHELL -lc` would (a) run plugin hooks under
    // the user's interactive shell (fish/zsh/etc.) even though hook scripts target
    // POSIX, and (b) trigger `-l` login-profile re-init that often resets PATH —
    // e.g. fish login shells that don't re-add `~/.cargo/bin` cause `cargo`-based
    // hooks to fail with "Unknown command: cargo" despite the parent claude having
    // cargo on PATH.
    #[cfg(windows)]
    {
        let mut cmd = Command::new("cmd");
        cmd.arg("/C").arg(command);
        cmd
    }
    #[cfg(not(windows))]
    {
        let mut cmd = Command::new("/bin/sh");
        cmd.arg("-c").arg(command);
        cmd
    }
}
