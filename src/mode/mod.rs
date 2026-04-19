pub mod catalog;
pub mod filter;
pub mod selectors;
pub mod tui;

use serde::{Deserialize, Serialize};

/// The reserved mode name. Always present (implicit if absent) and non-deletable.
pub const DEFAULT_MODE_NAME: &str = "default";

/// Returns `true` for mode names that cannot be deleted or renamed.
pub fn is_reserved_mode(name: &str) -> bool {
    name == DEFAULT_MODE_NAME
}

/// Trim + validate a user-supplied mode name. Returns the trimmed slice on success.
pub fn validate_mode_name(name: &str) -> crate::error::Result<&str> {
    let trimmed = name.trim();
    if trimmed.is_empty() {
        return Err(crate::error::AgentpackError::Mode(
            "mode name cannot be empty".into(),
        ));
    }
    if trimmed.contains(char::is_whitespace) {
        return Err(crate::error::AgentpackError::Mode(format!(
            "mode name cannot contain whitespace: {trimmed:?}"
        )));
    }
    Ok(trimmed)
}

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ModeBase {
    #[default]
    All,
    None,
}

impl ModeBase {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::All => "all",
            Self::None => "none",
        }
    }
}

impl std::fmt::Display for ModeBase {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
pub struct ModeDefinition {
    #[serde(default)]
    pub base: ModeBase,
    #[serde(default)]
    pub enable: Vec<String>,
    #[serde(default)]
    pub disable: Vec<String>,
}

impl ModeDefinition {
    pub fn implicit_default() -> Self {
        Self {
            base: ModeBase::All,
            enable: Vec::new(),
            disable: Vec::new(),
        }
    }

    pub fn sort_and_dedup(&mut self) {
        self.enable.sort();
        self.enable.dedup();
        self.disable.sort();
        self.disable.dedup();
    }
}
