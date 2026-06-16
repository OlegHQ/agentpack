use serde_json::{json, Value};

use crate::codex::{resolve_model_request, ALLOWED_MODELS};

const DEFAULT_BIG_MODEL: &str = "gpt-5.5";
const DEFAULT_MIDDLE_MODEL: &str = "gpt-5.4";
const DEFAULT_SMALL_MODEL: &str = "gpt-5.4-mini";

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModelMap {
    pub big: String,
    pub middle: String,
    pub small: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ProxyModel {
    pub requested: String,
    pub upstream: String,
    pub service_tier: Option<String>,
}

impl Default for ModelMap {
    fn default() -> Self {
        Self {
            big: DEFAULT_BIG_MODEL.to_string(),
            middle: DEFAULT_MIDDLE_MODEL.to_string(),
            small: DEFAULT_SMALL_MODEL.to_string(),
        }
    }
}

impl ModelMap {
    pub fn from_env() -> Self {
        Self {
            big: env_or("AGENTPACK_PROXY_BIG_MODEL", DEFAULT_BIG_MODEL),
            middle: env_or("AGENTPACK_PROXY_MIDDLE_MODEL", DEFAULT_MIDDLE_MODEL),
            small: env_or("AGENTPACK_PROXY_SMALL_MODEL", DEFAULT_SMALL_MODEL),
        }
    }

    pub fn upstream_model_for(&self, requested: &str) -> ProxyModel {
        let resolved = resolve_model_request(requested, None);
        let lower = requested.to_ascii_lowercase();
        let upstream = if is_explicit_openai_model(&lower) {
            resolved.model
        } else if lower.contains("haiku") {
            self.small.clone()
        } else if lower.contains("sonnet") {
            self.middle.clone()
        } else {
            self.big.clone()
        };
        ProxyModel {
            requested: requested.to_string(),
            upstream,
            service_tier: resolved.service_tier,
        }
    }

    pub fn claude_models_json(&self) -> Value {
        let data = self
            .claude_model_entries()
            .into_iter()
            .chain(self.codex_model_entries())
            .collect::<Vec<_>>();
        json!({ "data": data, "has_more": false })
    }

    fn claude_model_entries(&self) -> Vec<Value> {
        vec![
            self.model_json("claude-opus-4-7", "Claude Opus via Codex", &self.big),
            self.model_json("claude-opus-4-8", "Claude Opus via Codex", &self.big),
            self.model_json("claude-sonnet-4-6", "Claude Sonnet via Codex", &self.middle),
            self.model_json("claude-haiku-4-5", "Claude Haiku via Codex", &self.small),
        ]
    }

    fn codex_model_entries(&self) -> Vec<Value> {
        ALLOWED_MODELS
            .iter()
            .flat_map(|model| [model.to_string(), format!("{model}-fast")])
            .map(|model| self.model_json(&model, "Codex", &model))
            .collect()
    }

    fn model_json(&self, id: &str, display: &str, mapped_to: &str) -> Value {
        json!({
            "id": id,
            "type": "model",
            "display_name": format!("{display} ({mapped_to})"),
            "created_at": "2026-01-01T00:00:00Z"
        })
    }
}

fn env_or(key: &str, default: &str) -> String {
    std::env::var(key)
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| default.to_string())
}

fn is_explicit_openai_model(lower: &str) -> bool {
    ["gpt-", "o1-", "o3-", "o4-", "o5-"]
        .iter()
        .any(|prefix| lower.starts_with(prefix))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn maps_claude_model_families() {
        let models = ModelMap {
            big: "big".into(),
            middle: "mid".into(),
            small: "small".into(),
        };

        assert_eq!(models.upstream_model_for("claude-opus-4-7").upstream, "big");
        assert_eq!(
            models.upstream_model_for("claude-sonnet-4-6").upstream,
            "mid"
        );
        assert_eq!(
            models.upstream_model_for("claude-haiku-4-5").upstream,
            "small"
        );
        assert_eq!(models.upstream_model_for("gpt-5.5").upstream, "gpt-5.5");
        assert_eq!(
            models.upstream_model_for("gpt-5.5-fast"),
            ProxyModel {
                requested: "gpt-5.5-fast".into(),
                upstream: "gpt-5.5".into(),
                service_tier: Some("priority".into())
            }
        );
    }

    #[test]
    fn model_list_includes_claude_aliases_and_codex_models() {
        let data = ModelMap::default()
            .claude_models_json()
            .get("data")
            .and_then(Value::as_array)
            .cloned()
            .expect("models response has data array");
        let ids = data
            .iter()
            .filter_map(|model| model.get("id").and_then(Value::as_str))
            .collect::<Vec<_>>();

        assert!(ids.contains(&"claude-sonnet-4-6"));
        assert!(ids.contains(&"gpt-5.5"));
        assert!(ids.contains(&"gpt-5.5-fast"));
        assert!(ids.contains(&"gpt-5.3-codex"));
        assert!(ids.contains(&"gpt-5.3-codex-fast"));
    }
}
