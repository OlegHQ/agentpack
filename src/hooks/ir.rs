use std::collections::BTreeMap;
use std::path::PathBuf;

use serde::{Deserialize, Serialize};
use serde_json::Value;

// Re-exported so existing `crate::hooks::ir::HarnessTarget` paths (CLI args, spec field) resolve
// after `HookOutputTarget` was merged into the canonical `HarnessTarget`.
pub(crate) use crate::artifacts::HarnessTarget;

#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
pub enum ClaudeEvent {
    PreToolUse,
    PostToolUse,
    UserPromptSubmit,
    Stop,
    SubagentStop,
    SessionStart,
    SessionEnd,
    PreCompact,
    Notification,
    PermissionRequest,
}

impl ClaudeEvent {
    pub fn as_claude_str(self) -> &'static str {
        match self {
            Self::PreToolUse => "PreToolUse",
            Self::PostToolUse => "PostToolUse",
            Self::UserPromptSubmit => "UserPromptSubmit",
            Self::Stop => "Stop",
            Self::SubagentStop => "SubagentStop",
            Self::SessionStart => "SessionStart",
            Self::SessionEnd => "SessionEnd",
            Self::PreCompact => "PreCompact",
            Self::Notification => "Notification",
            Self::PermissionRequest => "PermissionRequest",
        }
    }

    /// Codex's hook schema (codex-rs/hooks) rejects unknown keys silently via serde,
    /// so names must match its PascalCase enum variants exactly. Codex currently only
    /// handles `PreToolUse`, `PostToolUse`, `SessionStart`, `UserPromptSubmit`, `Stop`;
    /// other events emit here but Codex will ignore them, which is fine.
    pub fn as_codex_str(self) -> &'static str {
        self.as_claude_str()
    }

    pub fn from_any_str(value: &str) -> Option<Self> {
        match value {
            "PreToolUse" | "pre-tool-use" => Some(Self::PreToolUse),
            "PostToolUse" | "post-tool-use" => Some(Self::PostToolUse),
            "UserPromptSubmit" | "user-prompt-submit" => Some(Self::UserPromptSubmit),
            "Stop" | "stop" => Some(Self::Stop),
            "SubagentStop" | "subagent-stop" => Some(Self::SubagentStop),
            "SessionStart" | "session-start" => Some(Self::SessionStart),
            "SessionEnd" | "session-end" => Some(Self::SessionEnd),
            "PreCompact" | "pre-compact" => Some(Self::PreCompact),
            "Notification" | "notification" => Some(Self::Notification),
            "PermissionRequest" | "permission-request" => Some(Self::PermissionRequest),
            _ => None,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
pub enum HookLayer {
    SeededNative,
    PackPlugin,
    BareSkill,
    DotAgents,
}

impl HookLayer {
    pub fn sort_rank(self) -> u8 {
        match self {
            Self::SeededNative => 0,
            Self::PackPlugin => 1,
            Self::BareSkill => 2,
            Self::DotAgents => 3,
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct CommandHandler {
    pub command: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub timeout_secs: Option<u64>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct HttpHandler {
    pub url: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub method: Option<String>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub headers: BTreeMap<String, String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub body: Option<Value>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct PromptHandler {
    pub prompt: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct AgentHandler {
    pub prompt: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub agent: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub enum ClaudeHandler {
    Command(CommandHandler),
    Http(HttpHandler),
    Prompt(PromptHandler),
    Agent(AgentHandler),
}

impl ClaudeHandler {
    pub fn kind_name(&self) -> &'static str {
        match self {
            Self::Command(_) => "command",
            Self::Http(_) => "http",
            Self::Prompt(_) => "prompt",
            Self::Agent(_) => "agent",
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct HookOrigin {
    pub layer: HookLayer,
    pub module: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cache_key: Option<String>,
    pub source_rel: String,
    pub source_root: PathBuf,
    pub source_file: PathBuf,
    pub package_key: String,
    pub event_index: usize,
    pub matcher_group_index: usize,
    pub hook_index: usize,
}

impl HookOrigin {
    pub fn source_id(&self) -> String {
        format!(
            "{}:{}:{}:{}",
            self.layer.sort_rank(),
            self.module,
            self.package_key,
            self.source_rel
        )
    }
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct NormalizedHook {
    pub event: ClaudeEvent,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub matcher: Option<String>,
    pub handler: ClaudeHandler,
    pub origin: HookOrigin,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub matcher_group_extra: BTreeMap<String, Value>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub raw_extra: BTreeMap<String, Value>,
}

impl NormalizedHook {
    pub fn is_strict(&self) -> bool {
        matches!(
            self.event,
            ClaudeEvent::PreToolUse | ClaudeEvent::PermissionRequest
        )
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
pub struct HookBundle {
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub hooks: Vec<NormalizedHook>,
}

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
pub enum HookDecision {
    #[default]
    Allow,
    Ask,
    Deny,
}

impl HookDecision {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Allow => "allow",
            Self::Ask => "ask",
            Self::Deny => "deny",
        }
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
pub struct NormalizedHookResult {
    pub decision: HookDecision,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub message: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub additional_context: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub updated_input: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub updated_tool_output: Option<Value>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub metadata: BTreeMap<String, Value>,
}

