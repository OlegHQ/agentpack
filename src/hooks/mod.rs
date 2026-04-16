pub mod capabilities;
pub mod collect;
pub mod ir;
pub mod merge;
pub mod parse;
pub mod paths;
pub mod render;
pub mod runtime;
pub mod stage;

pub use ir::{
    ClaudeEvent, ClaudeHandler, CommandHandler, HookBundle, HookDecision, HookLayer, HookOrigin,
    HookOutputTarget, HttpHandler, NormalizedHook, NormalizedHookResult, PromptHandler,
};
