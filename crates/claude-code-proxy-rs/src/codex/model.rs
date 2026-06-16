use thiserror::Error;

pub const ALLOWED_MODELS: &[&str] = &[
    "gpt-5.2",
    "gpt-5.3-codex",
    "gpt-5.3-codex-spark",
    "gpt-5.4",
    "gpt-5.4-mini",
    "gpt-5.5",
];

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ResolvedModel {
    pub model: String,
    pub service_tier: Option<String>,
}

#[derive(Debug, Error)]
pub enum ModelError {
    #[error("Model not allowed: {0}")]
    NotAllowed(String),
}

pub fn is_allowed_model(model: &str) -> bool {
    ALLOWED_MODELS.contains(&model)
}

pub fn assert_allowed_model(model: &str) -> Result<(), ModelError> {
    if is_allowed_model(model) {
        Ok(())
    } else {
        Err(ModelError::NotAllowed(model.to_string()))
    }
}

pub fn resolve_model_request(model: &str, override_model: Option<&str>) -> ResolvedModel {
    let alias = match model {
        "haiku" | "claude-haiku-4-5" | "claude-haiku-4-5-20251001" => "gpt-5.4-mini",
        "sonnet" | "claude-sonnet-4-6" => "gpt-5.4",
        "opus" | "claude-opus-4-7" => "gpt-5.5",
        other => other,
    };
    let is_fast_alias =
        alias.ends_with("-fast") && is_allowed_model(alias.trim_end_matches("-fast"));
    let without_tier = if is_fast_alias {
        alias.trim_end_matches("-fast")
    } else {
        alias
    };
    ResolvedModel {
        model: override_model.unwrap_or(without_tier).to_string(),
        service_tier: is_fast_alias.then(|| "priority".to_string()),
    }
}
