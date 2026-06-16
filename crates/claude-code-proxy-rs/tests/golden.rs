use claude_code_proxy::anthropic::AnthropicRequest;
use claude_code_proxy::codex::{
    accumulate_codex_response, codex_stream_to_anthropic_sse, reduce_codex_sse,
    translate_anthropic_to_codex, TranslateOptions,
};
use pretty_assertions::assert_eq;
use serde_json::Value;
use std::path::{Path, PathBuf};
use std::process::Command;

fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
}

fn fixture(path: &str) -> PathBuf {
    repo_root().join("tests/golden/fixtures").join(path)
}

fn ts_golden(mode: &str, fixture: &Path) -> String {
    let output = Command::new("bun")
        .arg("scripts/golden.ts")
        .arg(mode)
        .arg(fixture)
        .current_dir(repo_root())
        .output()
        .expect("failed to run bun golden runner");
    assert!(
        output.status.success(),
        "golden runner failed\nstdout:\n{}\nstderr:\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    String::from_utf8(output.stdout)
        .expect("golden stdout is utf-8")
        .trim()
        .to_string()
}

fn canonical_json(value: Value) -> Value {
    match value {
        Value::Array(items) => Value::Array(items.into_iter().map(canonical_json).collect()),
        Value::Object(map) => {
            let mut out = serde_json::Map::new();
            for (key, value) in map {
                out.insert(key, canonical_json(value));
            }
            Value::Object(out)
        }
        other => other,
    }
}

fn simple_request() -> AnthropicRequest {
    serde_json::from_value(serde_json::json!({
        "model": "claude-sonnet-4-6",
        "max_tokens": 32,
        "messages": [{"role": "user", "content": "hi"}]
    }))
    .unwrap()
}

#[test]
fn request_translation_matches_typescript_reference() {
    let path = fixture("request_complex.json");
    let request: AnthropicRequest =
        serde_json::from_slice(&std::fs::read(&path).unwrap()).expect("fixture request");
    let rust = translate_anthropic_to_codex(
        &request,
        TranslateOptions {
            session_id: Some("sess_golden".to_string()),
            service_tier: Some("priority".to_string()),
            ..TranslateOptions::default()
        },
    )
    .expect("rust translation");
    let rust = canonical_json(serde_json::to_value(rust).unwrap());
    let ts = canonical_json(serde_json::from_str(&ts_golden("request", &path)).unwrap());
    assert_eq!(rust, ts);
}

#[test]
fn request_translation_keeps_required_empty_instructions_without_system_prompt() {
    let translated =
        translate_anthropic_to_codex(&simple_request(), TranslateOptions::default()).unwrap();

    assert_eq!(translated.instructions.as_deref(), Some(""));
}

#[test]
fn request_translation_accepts_claude_xhigh_effort() {
    let mut request = simple_request();
    request.output_config = Some(claude_code_proxy::anthropic::AnthropicOutputConfig {
        effort: Some("xhigh".to_string()),
        format: None,
    });

    let translated = translate_anthropic_to_codex(&request, TranslateOptions::default()).unwrap();

    assert_eq!(
        translated.reasoning,
        Some(serde_json::json!({"effort": "xhigh"}))
    );
}

#[test]
fn reducer_matches_typescript_reference_for_text_stream() {
    assert_reducer_fixture("codex_text.sse");
}

#[test]
fn reducer_matches_typescript_reference_for_buffered_tool_stream() {
    assert_reducer_fixture("codex_tool.sse");
}

#[test]
fn accumulation_matches_typescript_reference_for_text_stream() {
    assert_accumulate_fixture("codex_text.sse");
}

#[test]
fn accumulation_matches_typescript_reference_for_buffered_tool_stream() {
    assert_accumulate_fixture("codex_tool.sse");
}

#[test]
fn anthropic_sse_matches_typescript_reference_for_text_stream() {
    let path = fixture("codex_text.sse");
    let input = std::fs::read(&path).unwrap();
    let rust = codex_stream_to_anthropic_sse(&input, "msg_golden", "gpt-5.4")
        .expect("rust sse")
        .into_iter()
        .map(|bytes| String::from_utf8(bytes.to_vec()).unwrap())
        .collect::<String>();
    let ts: String = serde_json::from_str(&ts_golden("sse", &path)).unwrap();
    assert_eq!(rust, ts);
}

fn assert_reducer_fixture(name: &str) {
    let path = fixture(name);
    let input = std::fs::read(&path).unwrap();
    let rust = canonical_json(serde_json::to_value(reduce_codex_sse(&input).unwrap()).unwrap());
    let ts = canonical_json(serde_json::from_str(&ts_golden("reduce", &path)).unwrap());
    assert_eq!(rust, ts);
}

fn assert_accumulate_fixture(name: &str) {
    let path = fixture(name);
    let input = std::fs::read(&path).unwrap();
    let rust = canonical_json(
        serde_json::to_value(
            accumulate_codex_response(&input, "msg_golden", "gpt-5.4")
                .unwrap()
                .response,
        )
        .unwrap(),
    );
    let ts = canonical_json(serde_json::from_str(&ts_golden("accumulate", &path)).unwrap());
    assert_eq!(rust, ts);
}
