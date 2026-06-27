use std::fs::{self, File};
use std::io::{BufWriter, Write};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Instant;

use anyhow::Context;
use serde_json::{json, Map, Value};

#[derive(Clone, Debug, Default)]
pub struct ProxyDiagnosticsConfig {
    pub log_dir: Option<PathBuf>,
    pub log_payloads: bool,
    pub max_body_bytes: usize,
}

#[derive(Clone)]
pub struct ProxyDiagnostics {
    inner: Option<Arc<ProxyDiagnosticsInner>>,
}

struct ProxyDiagnosticsInner {
    writer: Mutex<BufWriter<File>>,
    started_at: Instant,
    path: PathBuf,
    log_payloads: bool,
    max_body_bytes: usize,
}

impl ProxyDiagnostics {
    pub fn new(config: &ProxyDiagnosticsConfig) -> anyhow::Result<Self> {
        let Some(log_dir) = &config.log_dir else {
            return Ok(Self::noop());
        };
        fs::create_dir_all(log_dir)
            .with_context(|| format!("create proxy log dir {}", log_dir.display()))?;
        let filename = format!(
            "proxy-{}-{}.jsonl",
            chrono::Utc::now().format("%Y%m%dT%H%M%SZ"),
            std::process::id()
        );
        let path = log_dir.join(filename);
        let file = File::create(&path)
            .with_context(|| format!("create proxy log file {}", path.display()))?;
        let latest = log_dir.join("latest.json");
        let latest_body = serde_json::to_vec_pretty(&json!({
            "path": path,
            "started_at": chrono::Utc::now().to_rfc3339(),
            "pid": std::process::id(),
        }))?;
        let _ = fs::write(&latest, latest_body);
        Ok(Self {
            inner: Some(Arc::new(ProxyDiagnosticsInner {
                writer: Mutex::new(BufWriter::new(file)),
                started_at: Instant::now(),
                path,
                log_payloads: config.log_payloads,
                max_body_bytes: config.max_body_bytes.max(256),
            })),
        })
    }

    pub fn noop() -> Self {
        Self { inner: None }
    }

    pub fn path(&self) -> Option<&Path> {
        self.inner.as_ref().map(|inner| inner.path.as_path())
    }

    pub fn log_payloads(&self) -> bool {
        self.inner.as_ref().is_some_and(|inner| inner.log_payloads)
    }

    pub fn max_body_bytes(&self) -> usize {
        self.inner
            .as_ref()
            .map(|inner| inner.max_body_bytes)
            .unwrap_or(4096)
    }

    pub fn event(&self, kind: &str, fields: Value) {
        let Some(inner) = &self.inner else {
            return;
        };
        let mut event = Map::new();
        event.insert("ts".into(), Value::String(chrono::Utc::now().to_rfc3339()));
        event.insert(
            "elapsed_ms".into(),
            json!(inner.started_at.elapsed().as_millis()),
        );
        event.insert("kind".into(), Value::String(kind.to_string()));
        if let Value::Object(fields) = fields {
            for (key, value) in fields {
                event.insert(key, value);
            }
        }

        let Ok(mut writer) = inner.writer.lock() else {
            return;
        };
        if serde_json::to_writer(&mut *writer, &Value::Object(event)).is_ok() {
            let _ = writer.write_all(b"\n");
            let _ = writer.flush();
        }
    }
}

pub fn snippet(value: &str, max_bytes: usize) -> String {
    if value.len() <= max_bytes {
        return value.to_string();
    }
    let mut end = max_bytes;
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    format!("{}...", &value[..end])
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn snippet_is_utf8_safe() {
        assert_eq!(snippet("abčdef", 3), "ab...");
    }

    #[test]
    fn writes_jsonl_and_latest_pointer() {
        let dir = tempfile::tempdir().unwrap();
        let diagnostics = ProxyDiagnostics::new(&ProxyDiagnosticsConfig {
            log_dir: Some(dir.path().to_path_buf()),
            log_payloads: false,
            max_body_bytes: 1024,
        })
        .unwrap();
        diagnostics.event("test", json!({"request_id": 1}));
        let path = diagnostics.path().unwrap();
        let text = fs::read_to_string(path).unwrap();
        assert!(text.contains("\"kind\":\"test\""));
        assert!(dir.path().join("latest.json").is_file());
    }
}
