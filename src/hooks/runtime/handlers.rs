//! The per-handler-kind hook executors. One `run_*` entry point per `ClaudeHandler` variant; all
//! share the `HookExecutionSpec` bridge and `NormalizedHookResult` output. `run_codex_exec` backs
//! both the agent and prompt handlers (they differ only in how they build the prompt).

use std::path::Path;
use std::process::{Command, Stdio};

use anyhow::{anyhow, Context};
use reqwest::blocking::Client;
use serde_json::Value;

use super::bridge::{extract_json_object, normalize_response, stdin_json, HookExecutionSpec};
use crate::hooks::ir::{ClaudeHandler, NormalizedHookResult};

// ---- command ----

pub struct CommandHookOutput {
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
    pub exit_code: i32,
}

pub fn run_command(
    spec: &HookExecutionSpec,
    stdin_bytes: &[u8],
) -> anyhow::Result<CommandHookOutput> {
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

// ---- http ----

pub fn run_http(
    spec: &HookExecutionSpec,
    stdin_bytes: &[u8],
) -> anyhow::Result<NormalizedHookResult> {
    let ClaudeHandler::Http(handler) = &spec.handler else {
        return Err(anyhow!("http executor received non-http hook"));
    };
    let method = handler.method.as_deref().unwrap_or("POST");
    let method = reqwest::Method::from_bytes(method.as_bytes()).context("invalid HTTP method")?;
    let client = Client::new();
    let mut req = client.request(method.clone(), &handler.url);
    for (key, value) in &handler.headers {
        req = req.header(key, value);
    }
    if let Some(body) = &handler.body {
        req = req.json(body);
    } else if method != reqwest::Method::GET && !stdin_bytes.is_empty() {
        req = req.header("content-type", "application/json");
        req = req.body(stdin_bytes.to_vec());
    }
    let response = req.send().context("send hook HTTP request")?;
    let status = response.status();
    let body = response.text().context("read hook HTTP response body")?;
    if body.trim().is_empty() {
        return Ok(NormalizedHookResult::default());
    }
    let parsed = serde_json::from_str::<Value>(&body)
        .or_else(|_| stdin_json(body.as_bytes()).ok_or_else(|| anyhow!("not JSON response")))?;
    let mut normalized = normalize_response(parsed);
    if !status.is_success() && normalized.message.is_none() {
        normalized.message = Some(format!("hook HTTP request failed with status {status}"));
    }
    Ok(normalized)
}

// ---- prompt + agent (both run `codex exec`) ----

pub fn run_prompt(
    spec: &HookExecutionSpec,
    stdin_bytes: &[u8],
) -> anyhow::Result<NormalizedHookResult> {
    let ClaudeHandler::Prompt(handler) = &spec.handler else {
        return Err(anyhow!("prompt executor received non-prompt hook"));
    };
    let stdin_json = stdin_json(stdin_bytes).unwrap_or(serde_json::Value::Null);
    let prompt = format!(
        "You are executing an agentpack prompt hook. Return ONLY JSON with keys decision, message, additional_context, updated_input, updated_tool_output.\n\nAuthor prompt:\n{}\n\nHook input JSON:\n{}",
        handler.prompt,
        serde_json::to_string_pretty(&stdin_json)?
    );
    run_codex_exec(&prompt, handler.model.as_deref(), &spec.working_dir)
}

pub fn run_agent(
    spec: &HookExecutionSpec,
    stdin_bytes: &[u8],
) -> anyhow::Result<NormalizedHookResult> {
    let ClaudeHandler::Agent(handler) = &spec.handler else {
        return Err(anyhow!("agent executor received non-agent hook"));
    };
    let input = stdin_json(stdin_bytes).unwrap_or(serde_json::Value::Null);
    let prompt = format!(
        "You are executing an agentpack agent hook{}.\nReturn ONLY JSON with keys decision, message, additional_context, updated_input, updated_tool_output.\n\nHook instructions:\n{}\n\nHook input JSON:\n{}",
        handler
            .agent
            .as_deref()
            .map(|agent| format!(" for agent `{agent}`"))
            .unwrap_or_default(),
        handler.prompt,
        serde_json::to_string_pretty(&input).context("serialize hook input")?
    );
    run_codex_exec(&prompt, handler.model.as_deref(), &spec.working_dir)
}

fn run_codex_exec(
    prompt: &str,
    model: Option<&str>,
    cwd: &Path,
) -> anyhow::Result<NormalizedHookResult> {
    let codex = std::env::var("CODEX_PATH").unwrap_or_else(|_| "codex".to_string());
    let mut cmd = Command::new(codex);
    cmd.arg("exec").arg("--ephemeral").arg("-C").arg(cwd);
    if let Some(model) = model {
        cmd.arg("--model").arg(model);
    }
    cmd.arg(prompt);
    let output = cmd.output().context("spawn codex exec for hook prompt")?;
    if !output.status.success() {
        return Err(anyhow!(
            "codex exec failed: {}",
            String::from_utf8_lossy(&output.stderr)
        ));
    }
    let parsed = extract_json_object(&String::from_utf8_lossy(&output.stdout))?;
    Ok(normalize_response(parsed))
}
