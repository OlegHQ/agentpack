use crate::sse::encode_sse_event;
use bytes::Bytes;
use serde_json::json;

pub fn wants_downstream_stream(stream: Option<bool>) -> bool {
    stream == Some(true)
}

pub fn emit_message_start(message_id: &str, model: &str) -> Bytes {
    encode_sse_event(
        "message_start",
        &json!({
            "type": "message_start",
            "message": {
                "id": message_id,
                "type": "message",
                "role": "assistant",
                "model": model,
                "content": [],
                "stop_reason": null,
                "stop_sequence": null,
                "usage": {
                    "input_tokens": 0,
                    "output_tokens": 0,
                    "cache_creation_input_tokens": 0,
                    "cache_read_input_tokens": 0
                }
            }
        }),
    )
}

pub fn emit_ping() -> Bytes {
    encode_sse_event("ping", &json!({ "type": "ping" }))
}
