//! The `Harness` trait: one implementor per coding-agent integration, plus a registry.
//!
//! Centralizes the per-harness divergence (artifact rendering knobs, staging paths, prepare,
//! MCP, hooks, verify, launch) that used to be smeared across the `staging`, `hooks`, and
//! `launcher` trees as parallel `match` arms, lists, and near-duplicate files. Adding a 7th
//! harness should mean *one new file here + one line in [`all`]*, not editing ~15 call sites.
//!
//! The existing `HookRenderer` trait proved this shape works for one concern; this generalizes
//! it. Cross-harness passes that already take "all roots at once" (the pack/skill overlay loop,
//! `stage_guidance_all_harnesses`, `resolve_user_claude_bundle_collisions`) deliberately stay as
//! shared loops — the trait owns *per-harness* divergence, not those.

mod agy;
mod claude;
mod codex;
mod cursor;
mod grok;
mod opencode;

use agy::Agy;
use claude::Claude;
use codex::Codex;
use cursor::Cursor;
use grok::Grok;
use opencode::OpenCode;

pub use crate::artifacts::HarnessTarget;

/// One coding-agent integration. Each impl owns all of that harness's quirks. Zero-field unit
/// structs: every per-invocation input flows through borrowed context, so the registry can hand
/// out `&'static dyn Harness`.
pub trait Harness: Sync {
    fn id(&self) -> HarnessTarget;
}

/// The single source of truth for "what harnesses exist". Unit structs are const-constructible,
/// so rvalue static promotion makes these `&'static`.
pub fn all() -> &'static [&'static dyn Harness] {
    &[&Claude, &Cursor, &Codex, &OpenCode, &Grok, &Agy]
}

/// Resolve a harness identity to its implementor.
pub fn get(id: HarnessTarget) -> &'static dyn Harness {
    all()
        .iter()
        .copied()
        .find(|h| h.id() == id)
        .expect("every HarnessTarget has a Harness impl")
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashSet;

    #[test]
    fn registry_is_consistent_with_target_enum() {
        // Every registry entry has a unique id.
        let ids: Vec<HarnessTarget> = all().iter().map(|h| h.id()).collect();
        let id_set: HashSet<HarnessTarget> = ids.iter().copied().collect();
        assert_eq!(id_set.len(), all().len(), "duplicate harness ids in registry");

        // The registry and `HarnessTarget::all()` cover the same set (orders differ by design).
        let target_set: HashSet<HarnessTarget> = HarnessTarget::all().into_iter().collect();
        assert_eq!(
            id_set, target_set,
            "registry ids must equal HarnessTarget::all() as a set"
        );

        // Every target resolves through `get()` back to itself.
        for target in HarnessTarget::all() {
            assert_eq!(get(target).id(), target);
        }
    }
}
