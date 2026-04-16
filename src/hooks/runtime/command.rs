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
    cmd.current_dir(&spec.working_dir);
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
    #[cfg(windows)]
    {
        let mut cmd = Command::new("cmd");
        cmd.arg("/C").arg(command);
        cmd
    }
    #[cfg(not(windows))]
    {
        let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/sh".to_string());
        let mut cmd = Command::new(shell);
        cmd.arg("-lc").arg(command);
        cmd
    }
}
