//! Host-side hook dispatch for harnesses whose native matcher is coarser than Claude's
//! (currently Cursor). A single blanket hook entry per lifecycle event invokes us; we read
//! the harness stdin, normalize its tool name into candidate Claude tool names, iterate
//! stored specs, and fire every spec whose matcher matches.
//!
//! Results are aggregated: any `deny` wins over `allow`; messages and `additional_context`
//! are concatenated; `updated_input` / `updated_tool_output` use last-writer-wins.

use std::path::Path;

use regex::Regex;
use serde_json::Value;
use walkdir::WalkDir;

use super::bridge::{load_spec, stdin_json, HookExecutionSpec};
use super::handlers::{run_agent, run_command, run_http, run_prompt};
use crate::harness::HarnessTarget;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, HookDecision, NormalizedHookResult};

/// Extract the tool name from harness stdin. Cursor uses `tool_name`; we also accept Claude's
/// `tool_name` (same key), Codex's `tool`, and OpenCode's `tool.name` as fallbacks.
pub(crate) fn extract_tool_name(stdin_value: &Value) -> Option<String> {
    let obj = stdin_value.as_object()?;
    if let Some(s) = obj.get("tool_name").and_then(Value::as_str) {
        return Some(s.to_string());
    }
    if let Some(s) = obj.get("tool").and_then(Value::as_str) {
        return Some(s.to_string());
    }
    obj.get("tool")
        .and_then(|t| t.as_object())
        .and_then(|t| t.get("name"))
        .and_then(Value::as_str)
        .map(str::to_string)
}

/// Candidate tool names to test Claude matcher regex against. Covers Cursor↔Claude tool-class
/// renames (Shell↔Bash, Write↔Edit, Fetch↔WebFetch). Unknown names pass through as a single
/// candidate so literal matchers still match.
pub(crate) fn candidate_tool_names(raw: &str) -> Vec<String> {
    let mut candidates = vec![raw.to_string()];
    match raw {
        "Shell" => candidates.push("Bash".into()),
        "Bash" => candidates.push("Shell".into()),
        "Write" => candidates.push("Edit".into()),
        "Edit" => candidates.push("Write".into()),
        "Fetch" => candidates.push("WebFetch".into()),
        "WebFetch" => candidates.push("Fetch".into()),
        _ => {}
    }
    if let Some(rest) = raw.strip_prefix("MCP:") {
        candidates.push(format!("mcp__{rest}"));
    } else if let Some(rest) = raw.strip_prefix("mcp__") {
        candidates.push(format!("MCP:{rest}"));
    }
    candidates
}

/// Test a Claude matcher regex against every candidate tool name.
/// Empty/missing matcher matches everything. Fullmatch-anchored.
pub(crate) fn matcher_matches(matcher: Option<&str>, candidates: &[String]) -> bool {
    let Some(pattern) = matcher.map(str::trim).filter(|s| !s.is_empty()) else {
        return true;
    };
    let anchored = format!("^(?:{pattern})$");
    let Ok(re) = Regex::new(&anchored) else {
        return false;
    };
    candidates.iter().any(|c| re.is_match(c.as_str()))
}

/// Load every spec file under `specs_dir` (recursive, `.json` only). Non-JSON / non-spec files
/// are skipped with a debug log so stray artifacts don't break dispatch.
fn load_specs(specs_dir: &Path) -> Vec<(std::path::PathBuf, HookExecutionSpec)> {
    let mut out = Vec::new();
    for entry in WalkDir::new(specs_dir)
        .follow_links(false)
        .into_iter()
        .filter_map(|entry| entry.ok())
    {
        let path = entry.into_path();
        if !path.is_file() || path.extension().is_none_or(|ext| ext != "json") {
            continue;
        }
        match load_spec(&path) {
            Ok(spec) => out.push((path, spec)),
            Err(e) => tracing::debug!(path = %path.display(), error = %e, "skip non-spec"),
        }
    }
    out
}

/// Run the spec's handler and normalize the reply. Command handlers need special handling
/// (they stream stdout/stderr to the harness directly, but here we aggregate, so we capture
/// stdout as JSON and derive a decision from exit code).
fn execute_spec(
    spec: &HookExecutionSpec,
    stdin_bytes: &[u8],
) -> anyhow::Result<NormalizedHookResult> {
    match &spec.handler {
        ClaudeHandler::Command(_) => {
            let result = run_command(spec, stdin_bytes)?;
            let mut normalized = if result.stdout.is_empty() {
                NormalizedHookResult::default()
            } else {
                let text = String::from_utf8_lossy(&result.stdout);
                super::bridge::extract_json_object(&text)
                    .map(super::bridge::normalize_response)
                    .unwrap_or_default()
            };
            // Exit code 2 = deny per Claude/Cursor conventions; non-zero with no JSON = deny fail-closed.
            if result.exit_code == 2 {
                normalized.decision = HookDecision::Deny;
                if normalized.message.is_none() && !result.stderr.is_empty() {
                    normalized.message = Some(String::from_utf8_lossy(&result.stderr).into_owned());
                }
            }
            Ok(normalized)
        }
        ClaudeHandler::Http(_) => run_http(spec, stdin_bytes),
        ClaudeHandler::Prompt(_) => run_prompt(spec, stdin_bytes),
        ClaudeHandler::Agent(_) => run_agent(spec, stdin_bytes),
    }
}

fn merge_results(acc: &mut NormalizedHookResult, next: NormalizedHookResult) {
    if next.decision == HookDecision::Deny {
        acc.decision = HookDecision::Deny;
    } else if acc.decision != HookDecision::Deny && next.decision == HookDecision::Ask {
        acc.decision = HookDecision::Ask;
    }
    if let Some(msg) = next.message {
        match &mut acc.message {
            Some(existing) => {
                existing.push('\n');
                existing.push_str(&msg);
            }
            None => acc.message = Some(msg),
        }
    }
    if let Some(ctx) = next.additional_context {
        match &mut acc.additional_context {
            Some(existing) => {
                existing.push_str("\n\n");
                existing.push_str(&ctx);
            }
            None => acc.additional_context = Some(ctx),
        }
    }
    if next.updated_input.is_some() {
        acc.updated_input = next.updated_input;
    }
    if next.updated_tool_output.is_some() {
        acc.updated_tool_output = next.updated_tool_output;
    }
    acc.metadata.extend(next.metadata);
}

pub struct DispatchArgs<'a> {
    pub target: HarnessTarget,
    pub event: ClaudeEvent,
    pub specs_dir: &'a Path,
    pub stdin_bytes: &'a [u8],
}

pub struct DispatchOutcome {
    pub json: Value,
    /// Exit code convention: `2` for deny, `0` otherwise. Mirrors Cursor's fail-closed default.
    pub exit_code: i32,
}

pub fn dispatch(args: DispatchArgs<'_>) -> anyhow::Result<DispatchOutcome> {
    let stdin_value = stdin_json(args.stdin_bytes);
    let tool = stdin_value.as_ref().and_then(extract_tool_name);
    let candidates = tool
        .as_deref()
        .map(candidate_tool_names)
        .unwrap_or_default();

    let specs = load_specs(args.specs_dir);
    let mut merged = NormalizedHookResult::default();
    let mut fired = 0usize;

    for (path, spec) in specs {
        if spec.event != args.event {
            continue;
        }
        if !matcher_matches(spec.matcher.as_deref(), &candidates) {
            continue;
        }
        match execute_spec(&spec, args.stdin_bytes) {
            Ok(result) => {
                merge_results(&mut merged, result);
                fired += 1;
            }
            Err(e) => {
                tracing::warn!(spec = %path.display(), error = %e, "hook spec failed");
                // Fail-closed for strict events: report deny with the error message.
                if matches!(
                    args.event,
                    ClaudeEvent::PreToolUse | ClaudeEvent::PermissionRequest
                ) {
                    merged.decision = HookDecision::Deny;
                    let msg = format!("hook dispatch error ({}): {e}", path.display());
                    merged.message = Some(match merged.message.take() {
                        Some(existing) => format!("{existing}\n{msg}"),
                        None => msg,
                    });
                }
            }
        }
    }

    if fired == 0 && merged.decision != HookDecision::Deny {
        // No matching spec fired and nothing failed — allow silently (return empty object).
        return Ok(DispatchOutcome {
            json: Value::Object(serde_json::Map::new()),
            exit_code: 0,
        });
    }

    let exit_code = if merged.decision == HookDecision::Deny {
        2
    } else {
        0
    };
    let json = args.target.harness().hook_output(args.event, &merged);
    Ok(DispatchOutcome { json, exit_code })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn shell_matches_bash_matcher() {
        let candidates = candidate_tool_names("Shell");
        assert!(matcher_matches(Some("Bash"), &candidates));
        assert!(matcher_matches(Some("Shell"), &candidates));
        assert!(matcher_matches(Some("Bash|Shell"), &candidates));
    }

    #[test]
    fn write_matches_edit_write_matcher() {
        let candidates = candidate_tool_names("Write");
        assert!(matcher_matches(Some("Edit|Write"), &candidates));
        assert!(matcher_matches(Some("Edit"), &candidates));
        assert!(matcher_matches(Some("Write"), &candidates));
    }

    #[test]
    fn empty_matcher_matches_anything() {
        assert!(matcher_matches(None, &["Read".into()]));
        assert!(matcher_matches(Some(""), &["Read".into()]));
        assert!(matcher_matches(Some("   "), &["Read".into()]));
    }

    #[test]
    fn matcher_is_fullmatch_anchored() {
        let candidates = candidate_tool_names("WebSearch");
        assert!(!matcher_matches(Some("Web"), &candidates));
        assert!(matcher_matches(Some("WebSearch"), &candidates));
    }

    #[test]
    fn mcp_prefix_translates_between_cursor_and_claude() {
        let cursor = candidate_tool_names("MCP:codesight__search");
        assert!(cursor.iter().any(|c| c == "mcp__codesight__search"));
        let claude = candidate_tool_names("mcp__codesight__search");
        assert!(claude.iter().any(|c| c == "MCP:codesight__search"));
    }

    #[test]
    fn extract_tool_name_from_cursor_stdin() {
        let v = serde_json::json!({"tool_name":"Shell","tool_input":{}});
        assert_eq!(extract_tool_name(&v).as_deref(), Some("Shell"));
    }

    #[test]
    fn extract_tool_name_from_claude_stdin() {
        let v = serde_json::json!({"tool_name":"Read","tool_input":{}});
        assert_eq!(extract_tool_name(&v).as_deref(), Some("Read"));
    }

    #[test]
    fn merge_deny_beats_allow() {
        let mut acc = NormalizedHookResult::default();
        merge_results(
            &mut acc,
            NormalizedHookResult {
                decision: HookDecision::Allow,
                message: Some("ok".into()),
                ..Default::default()
            },
        );
        merge_results(
            &mut acc,
            NormalizedHookResult {
                decision: HookDecision::Deny,
                message: Some("no".into()),
                ..Default::default()
            },
        );
        assert_eq!(acc.decision, HookDecision::Deny);
        assert_eq!(acc.message.as_deref(), Some("ok\nno"));
    }
}
