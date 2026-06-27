use std::io::{self, Cursor, Read};
use std::sync::mpsc::{self, Receiver};
use std::thread;

use crate::codex::{accumulate_codex_response, codex_stream_to_anthropic_sse};

use super::diagnostics::ProxyDiagnostics;

pub struct AnthropicSseReader {
    rx: Receiver<Vec<u8>>,
    current: Cursor<Vec<u8>>,
    finished: bool,
}

impl AnthropicSseReader {
    pub fn spawn_with_diagnostics<F>(
        upstream: F,
        message_id: String,
        model: String,
        diagnostics: ProxyDiagnostics,
        request_id: u64,
    ) -> Self
    where
        F: FnOnce() -> anyhow::Result<Vec<u8>> + Send + 'static,
    {
        let (tx, rx) = mpsc::channel();
        thread::Builder::new()
            .name("agentpack-codex-sse-bridge".into())
            .spawn(move || {
                let result = upstream()
                    .and_then(|bytes| {
                        codex_stream_to_anthropic_sse(&bytes, &message_id, &model)
                            .map_err(Into::into)
                    })
                    .map(|chunks| {
                        chunks
                            .into_iter()
                            .map(|bytes| bytes.to_vec())
                            .collect::<Vec<_>>()
                    });
                match result {
                    Ok(chunks) => {
                        for chunk in chunks {
                            if tx.send(chunk).is_err() {
                                return;
                            }
                        }
                    }
                    Err(err) => {
                        diagnostics.event(
                            "stream_bridge_error",
                            serde_json::json!({"request_id": request_id, "error": format!("{err:#}")}),
                        );
                        let _ = tx.send(error_event(&err.to_string()).into_bytes());
                    }
                }
                let _ = tx.send(b"data: [DONE]\n\n".to_vec());
            })
            .expect("start proxy SSE bridge thread");
        Self {
            rx,
            current: Cursor::new(Vec::new()),
            finished: false,
        }
    }
}

impl Read for AnthropicSseReader {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        loop {
            let read = self.current.read(buf)?;
            if read > 0 {
                return Ok(read);
            }
            if self.finished {
                return Ok(0);
            }
            match self.rx.recv() {
                Ok(bytes) => self.current = Cursor::new(bytes),
                Err(_) => self.finished = true,
            }
        }
    }
}

pub fn accumulate_anthropic_response(
    bytes: &[u8],
    message_id: &str,
    model: &str,
) -> anyhow::Result<serde_json::Value> {
    let accumulated = accumulate_codex_response(bytes, message_id, model)?;
    serde_json::to_value(accumulated.response).map_err(Into::into)
}

fn error_event(message: &str) -> String {
    let data = serde_json::json!({
        "type": "error",
        "error": {
            "type": "api_error",
            "message": message,
        }
    });
    format!(
        "event: error\ndata: {}\n\n",
        serde_json::to_string(&data).unwrap_or_else(|_| "{}".into())
    )
}
