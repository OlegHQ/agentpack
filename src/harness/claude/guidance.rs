//! Claude always-apply guidance injection: stage the raw blob in the bundle and synthesize a
//! `SessionStart` hook that emits it as `additionalContext` every session.

use std::fs;
use std::path::Path;

use serde_json::{json, Map, Value};

use crate::error::{AgentpackError, Result};

/// File in the bundle holding the raw blob that the SessionStart hook reads.
const BUNDLE_GUIDANCE_REL: &str = "_agentpack/guidance.md";

pub(super) fn inject(bundle: &Path, blob: &str) -> Result<()> {
    let guidance_file = bundle.join(BUNDLE_GUIDANCE_REL);
    if let Some(parent) = guidance_file.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    fs::write(&guidance_file, blob).map_err(|e| AgentpackError::io(&guidance_file, e))?;
    add_session_start_hook(bundle, &guidance_file)
}

/// Add a SessionStart hook into the bundle's `hooks/hooks.json` that emits the guidance blob as
/// `additionalContext`. Works whether or not the hooks pipeline already wrote the file.
fn add_session_start_hook(bundle: &Path, guidance_file: &Path) -> Result<()> {
    let hooks_path = bundle.join("hooks/hooks.json");
    let existing = fs::read_to_string(&hooks_path).unwrap_or_else(|_| "{\"hooks\":{}}".into());
    let mut root: Value = serde_json::from_str(&existing).unwrap_or_else(|_| json!({"hooks": {}}));
    let root_obj = root.as_object_mut().ok_or_else(|| {
        AgentpackError::Staging(format!("{}: not a JSON object", hooks_path.display()))
    })?;
    let hooks = root_obj
        .entry("hooks".to_string())
        .or_insert_with(|| Value::Object(Map::new()));
    let hooks_obj = hooks.as_object_mut().ok_or_else(|| {
        AgentpackError::Staging(format!(
            "{}: `hooks` not a JSON object",
            hooks_path.display()
        ))
    })?;

    let cmd = format!(
        "agentpack hook-exec inject-guidance --target claude --event SessionStart --file {}",
        shell_escape::escape(guidance_file.to_string_lossy())
    );
    let entry = json!({
        "hooks": [{"type": "command", "command": cmd}]
    });

    // Replace any prior agentpack-injected SessionStart entry (identified by the command prefix)
    // so re-staging doesn't stack duplicates.
    let prefix = "agentpack hook-exec inject-guidance";
    let arr = hooks_obj
        .entry("SessionStart".to_string())
        .or_insert_with(|| Value::Array(Vec::new()))
        .as_array_mut()
        .ok_or_else(|| {
            AgentpackError::Staging(format!(
                "{}: SessionStart not an array",
                hooks_path.display()
            ))
        })?;
    arr.retain(|e| {
        let has_ours = e
            .as_object()
            .and_then(|g| g.get("hooks"))
            .and_then(Value::as_array)
            .map(|inner| {
                inner.iter().any(|h| {
                    h.get("command")
                        .and_then(Value::as_str)
                        .is_some_and(|c| c.contains(prefix))
                })
            })
            .unwrap_or(false);
        !has_ours
    });
    arr.push(entry);

    if let Some(parent) = hooks_path.parent() {
        fs::create_dir_all(parent).map_err(|e| AgentpackError::io(parent, e))?;
    }
    let out = serde_json::to_string_pretty(&root)
        .map_err(|e| AgentpackError::Staging(format!("{}: {e}", hooks_path.display())))?;
    fs::write(&hooks_path, out).map_err(|e| AgentpackError::io(&hooks_path, e))
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn session_start_hook_added_when_file_absent() {
        let dir = tempdir().unwrap();
        let bundle = dir.path();
        inject(bundle, "hi").unwrap();
        let parsed: Value =
            serde_json::from_str(&fs::read_to_string(bundle.join("hooks/hooks.json")).unwrap())
                .unwrap();
        let arr = parsed["hooks"]["SessionStart"].as_array().unwrap();
        assert_eq!(arr.len(), 1);
        let cmd = arr[0]["hooks"][0]["command"].as_str().unwrap();
        assert!(cmd.contains("inject-guidance"));
        assert!(cmd.contains("--target claude"));
        assert_eq!(
            fs::read_to_string(bundle.join(BUNDLE_GUIDANCE_REL)).unwrap(),
            "hi"
        );
    }

    #[test]
    fn session_start_hook_replaces_prior_entry_and_preserves_other_events() {
        let dir = tempdir().unwrap();
        let bundle = dir.path();
        fs::create_dir_all(bundle.join("hooks")).unwrap();
        fs::write(
            bundle.join("hooks/hooks.json"),
            r#"{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"echo"}]}]}}"#,
        )
        .unwrap();
        inject(bundle, "hi").unwrap();
        inject(bundle, "hi").unwrap();
        let parsed: Value =
            serde_json::from_str(&fs::read_to_string(bundle.join("hooks/hooks.json")).unwrap())
                .unwrap();
        assert!(!parsed["hooks"]["PreToolUse"].as_array().unwrap().is_empty());
        assert_eq!(parsed["hooks"]["SessionStart"].as_array().unwrap().len(), 1);
    }
}
