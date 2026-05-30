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
mod launch;
mod opencode;
mod target;

pub(crate) use launch::launch;
pub use target::HarnessTarget;

use std::path::{Path, PathBuf};
use std::process::Command;

use serde_json::Value;
use serde_norway::Mapping;

use agy::Agy;
use claude::Claude;
use codex::Codex;
use cursor::Cursor;
use grok::Grok;
use opencode::OpenCode;

use crate::artifacts::yaml::insert_string;
use crate::artifacts::ArtifactKind;
use crate::error::{AgentpackError, Result};
use crate::hooks::capabilities::SupportLevel;
use crate::hooks::ir::{ClaudeEvent, ClaudeHandler, NormalizedHookResult};
use crate::hooks::render::HookRenderer;
use crate::mode::filter::EffectiveMode;
use crate::staging::mcp::StagedMcpEntries;
use crate::ui::Ui;

/// Read-only context threaded into every staging step. Pure borrows — the staging pipeline already
/// owns these, so the trait methods derive their own per-harness destination paths from
/// `project_root` + `mode` rather than receiving them.
pub(crate) struct StageCtx<'a> {
    pub project_root: &'a Path,
    pub mode: &'a EffectiveMode,
    /// The harness being launched, if any. Drives workspace-overlay materialization and the
    /// presence checks that are only meaningful for the launching harness (Cursor / Agy).
    pub launch_target: Option<HarnessTarget>,
}

/// Per-launch context, built by `harness::launch` after `sync_for_launch` resolves the mode and
/// consumed by [`Harness::launch_command`]. `passthrough` is owned because launchers default and
/// reorder args before building the `Command`.
pub(crate) struct LaunchCtx<'a> {
    pub project_root: &'a Path,
    pub passthrough: Vec<String>,
    pub mode: &'a EffectiveMode,
    pub yolo: bool,
    pub ui: &'a Ui,
}

/// Staging-side assertion helper: `Err(Staging(msg()))` when `cond` is false.
fn require(cond: bool, msg: impl FnOnce() -> String) -> Result<()> {
    if cond {
        Ok(())
    } else {
        Err(AgentpackError::Staging(msg()))
    }
}

/// One coding-agent integration. Each impl owns all of that harness's quirks. Zero-field unit
/// structs: every per-invocation input flows through borrowed context, so the registry can hand
/// out `&'static dyn Harness`.
pub(crate) trait Harness: Sync {
    fn id(&self) -> HarnessTarget;

    // ---- staging paths (was: 9 StagingPipeline accessors + path selection in reset_all) ----

    /// The directory pack content is staged into for this harness — the "pack-content root" the
    /// shared overlay loop writes to (Claude→bundle, Grok→grok bundle, Cursor→pack plugin, …).
    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf>;

    /// The full set of directories to wipe before a rebuild. Defaults to just [`staged_root`];
    /// harnesses whose wipe set is broader than the pack-content root (Claude's plugins parent,
    /// Cursor's fake home, Grok's home, Agy's parent dir) override this.
    fn reset_paths(&self, project_root: &Path, mode: &str) -> Result<Vec<PathBuf>> {
        Ok(vec![self.staged_root(project_root, mode)?])
    }

    // ---- prepare: mkdir + seed user config + force attribution off ----

    /// Create this harness's staging dirs, seed user config where applicable, write any bundle
    /// manifest, and force AI attribution off. Runs before the shared pack/skill overlay loop.
    fn prepare(&self, ctx: &StageCtx) -> Result<()>;

    // ---- artifact rendering knobs (was: 5 `match self` tables in artifacts/harness.rs) ----

    /// Portable plugin subtrees copied verbatim from cached plugins before the markdown overlay
    /// renders artifacts on top. Default: none (config-root harnesses get content via rendering).
    fn raw_plugin_subdirs(&self) -> &'static [&'static str] {
        &[]
    }

    /// Seed `commands/*.md` YAML before merging allowed extra keys. Key insertion order differs
    /// per harness, so this is a side-effecting writer rather than a returned list. Default: a
    /// lone `description` (OpenCode / Codex / Agy).
    fn seed_command_frontmatter(&self, m: &mut Mapping, _name: &str, description: &str) {
        insert_string(m, "description", description);
    }

    /// Extra `commands/*.md` frontmatter keys preserved verbatim during rendering.
    fn command_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[
            "agent",
            "allowed-tools",
            "context",
            "disable-model-invocation",
            "model",
            "subtask",
        ]
    }

    /// Extra `SKILL.md` frontmatter keys preserved verbatim during rendering.
    fn skill_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[
            "allowed-tools",
            "agent",
            "compatibility",
            "context",
            "disallowedTools",
            "license",
            "mcpServers",
            "metadata",
            "mode",
            "model",
            "permission",
            "subtask",
            "tools",
        ]
    }

    /// Extra `agents/*.md` frontmatter keys preserved verbatim during rendering.
    fn agent_allowed_extra_frontmatter_keys(&self) -> &'static [&'static str] {
        &[
            "color",
            "disallowedTools",
            "hidden",
            "hooks",
            "mcpServers",
            "mode",
            "model",
            "permission",
            "subtask",
            "tools",
        ]
    }

    /// Staged artifact kind after target-specific folding. Default: skills stay skills, commands
    /// and agents pass through, rules degrade to a skill fallback (no native rule files).
    fn rendered_artifact_kind(&self, source: ArtifactKind) -> ArtifactKind {
        match source {
            ArtifactKind::Rule => ArtifactKind::Skill,
            other => other,
        }
    }

    /// Default `disable-model-invocation` for staged skills when the source artifact did not set
    /// it. Only slash-commands-converted-to-skills are disabled by default; this does not diverge
    /// per harness today, so it is a shared default with no overrides.
    fn disables_model_invocation_for_kind(&self, kind: ArtifactKind) -> bool {
        kind == ArtifactKind::Command
    }

    /// Whether this harness stages first-class `commands/` and `agents/` markdown trees. Codex
    /// folds commands/agents into skills, so it has neither — collision shadowing of `.md` stems
    /// skips it. Drives the `md_roots` subset in `staging::verify_staging`.
    fn stages_command_agent_trees(&self) -> bool {
        true
    }

    // ---- MCP (was: the 4 native format writers fanned out in mcp::stage_merged_mcp) ----

    /// Render the already-merged MCP server set into this harness's native config file. The merge
    /// runs once in `staging`; each impl only writes its own format to its own destination.
    fn write_mcp(&self, merged: &StagedMcpEntries, ctx: &StageCtx) -> Result<()>;

    // ---- hooks (was: support_for / base_asset_root / to_target_output arms) ----

    /// Support level for emulating a given Claude hook `event` + `handler` on this harness.
    fn hook_support(&self, event: ClaudeEvent, handler: &ClaudeHandler) -> SupportLevel;

    /// Root directory staged hook packages/specs live under for this harness. Default:
    /// `hooks/_packages` under the target root.
    fn hook_asset_root(&self, target_root: &Path) -> PathBuf {
        target_root.join("hooks/_packages")
    }

    /// Translate a normalized hook result into this harness's native hook-output JSON. Called by
    /// the hook *runtime* for all six harnesses (not just those that stage renderers).
    fn hook_output(&self, event: ClaudeEvent, result: &NormalizedHookResult) -> Value;

    /// The hook renderer for this harness, or `None` when hook staging is disabled (Grok / Agy
    /// today, pending CLI smoke-test coverage).
    fn hook_renderer(&self) -> Option<Box<dyn HookRenderer>> {
        None
    }

    // ---- verify (was: the 6 `StagingPipeline::verify_*` methods) ----

    /// Assert this harness's staged tree is well-formed. Read-only — cross-harness mutation
    /// (collision shadowing) stays a shared pass in `staging::verify_staging`.
    fn verify(&self, ctx: &StageCtx) -> Result<()>;

    // ---- launch (was: the 6 `launcher::run_*` fns) ----

    /// Build the fully-configured child process: resolve the binary, set env (`CODEX_HOME`,
    /// fake `HOME`, …), inject default and yolo args. `harness::launch` runs `sync_for_launch`
    /// first, then execs the returned `Command`.
    fn launch_command(&self, ctx: LaunchCtx) -> anyhow::Result<Command>;

    // ---- always-run post-staging finalize (Claude MCP allowlist, Cursor fake-home/approvals) ----

    /// Per-harness step run for every harness after the shared MCP merge and pack/guidance staging,
    /// before any launch-scoped workspace overlay. Receives the merged MCP set (may be empty).
    /// Default: nothing.
    fn finalize(&self, _merged: &StagedMcpEntries, _ctx: &StageCtx) -> Result<()> {
        Ok(())
    }

    // ---- optional workspace overlay (only Cursor + Agy) ----

    /// Materialize this harness's project-side workspace overlay symlink (Cursor `.cursor/agents`,
    /// Agy `.agents/plugins/agentpack-bundle`). Only invoked for the harness being launched; stale
    /// overlays from a prior run are cleaned up unconditionally during `prepare`. Default: nothing.
    fn finalize_workspace_overlay(&self, _ctx: &StageCtx) -> Result<()> {
        Ok(())
    }
}

/// The single source of truth for "what harnesses exist". Unit structs are const-constructible,
/// so rvalue static promotion makes these `&'static`.
pub(crate) fn all() -> &'static [&'static dyn Harness] {
    &[&Claude, &Cursor, &Codex, &OpenCode, &Grok, &Agy]
}

/// Resolve a harness identity to its implementor.
pub(crate) fn get(id: HarnessTarget) -> &'static dyn Harness {
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
        assert_eq!(
            id_set.len(),
            all().len(),
            "duplicate harness ids in registry"
        );

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
