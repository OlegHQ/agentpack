use crate::artifacts::HarnessTarget;

use super::ir::{ClaudeEvent, ClaudeHandler};

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum SupportLevel {
    Native,
    Emulated,
    Degraded { reason: &'static str },
    Unsupported { reason: &'static str },
}

impl SupportLevel {
    pub fn label(&self) -> &'static str {
        match self {
            Self::Native => "native",
            Self::Emulated => "emulated",
            Self::Degraded { .. } => "degraded",
            Self::Unsupported { .. } => "unsupported",
        }
    }
}

pub fn support_for(
    target: HarnessTarget,
    event: ClaudeEvent,
    handler: &ClaudeHandler,
) -> SupportLevel {
    match target {
        HarnessTarget::Claude => SupportLevel::Native,
        HarnessTarget::Cursor => cursor_support(event, handler),
        HarnessTarget::Codex => codex_support(event, handler),
        HarnessTarget::OpenCode => opencode_support(event, handler),
        HarnessTarget::Grok => SupportLevel::Unsupported {
            reason: "Grok hooks are not staged because current Grok only loaded hooks from HOME/project-trusted roots in smoke tests",
        },
        HarnessTarget::Agy => SupportLevel::Unsupported {
            reason: "Antigravity hook rendering is gated until plugin-local hook runtime smoke tests pass",
        },
    }
}

fn cursor_support(event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel {
    match event {
        ClaudeEvent::Notification => SupportLevel::Unsupported {
            reason: "Cursor has no notification hook surface",
        },
        ClaudeEvent::PermissionRequest => match handler {
            ClaudeHandler::Http(_) | ClaudeHandler::Agent(_) => SupportLevel::Degraded {
                reason: "Cursor permission hooks are decomposed into preToolUse bridge commands",
            },
            _ => SupportLevel::Degraded {
                reason: "Cursor models permission requests as preToolUse instead of a dedicated event",
            },
        },
        ClaudeEvent::SessionStart | ClaudeEvent::PreCompact => match handler {
            ClaudeHandler::Http(_) | ClaudeHandler::Agent(_) => SupportLevel::Degraded {
                reason: "Cursor supports the lifecycle but requires bridge execution for this handler type",
            },
            _ => SupportLevel::Degraded {
                reason: "Cursor cannot preserve Claude trigger-specific matchers for this lifecycle event",
            },
        },
        _ => match handler {
            ClaudeHandler::Command(_) | ClaudeHandler::Prompt(_) => SupportLevel::Native,
            ClaudeHandler::Http(_) | ClaudeHandler::Agent(_) => SupportLevel::Emulated,
        },
    }
}

fn codex_support(event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel {
    let event_level = match event {
        ClaudeEvent::PreToolUse
        | ClaudeEvent::PostToolUse
        | ClaudeEvent::UserPromptSubmit
        | ClaudeEvent::SessionStart
        | ClaudeEvent::Stop => None,
        ClaudeEvent::PermissionRequest => Some(SupportLevel::Degraded {
            reason: "Codex permission checks are approximated with pre-tool-use hooks",
        }),
        _ => Some(SupportLevel::Unsupported {
            reason: "Codex does not expose this Claude lifecycle event natively",
        }),
    };
    if let Some(level) = event_level {
        return level;
    }
    match handler {
        ClaudeHandler::Command(_) | ClaudeHandler::Prompt(_) => SupportLevel::Native,
        ClaudeHandler::Http(_) | ClaudeHandler::Agent(_) => SupportLevel::Emulated,
    }
}

fn opencode_support(event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel {
    let event_level = match event {
        ClaudeEvent::PreToolUse
        | ClaudeEvent::PostToolUse
        | ClaudeEvent::PermissionRequest
        | ClaudeEvent::PreCompact => None,
        ClaudeEvent::UserPromptSubmit => Some(SupportLevel::Degraded {
            reason: "OpenCode exposes chat.message after receipt rather than Claude's submit hook",
        }),
        _ => Some(SupportLevel::Unsupported {
            reason: "OpenCode has no direct lifecycle hook for this Claude event",
        }),
    };
    if let Some(level) = event_level {
        return level;
    }
    match handler {
        ClaudeHandler::Command(_) => SupportLevel::Native,
        ClaudeHandler::Http(_) | ClaudeHandler::Prompt(_) | ClaudeHandler::Agent(_) => {
            SupportLevel::Emulated
        }
    }
}
