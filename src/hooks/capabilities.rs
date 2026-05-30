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

pub(crate) fn cursor_support(event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel {
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
