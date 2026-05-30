use crate::error::Result;
use crate::harness::HarnessTarget;
use crate::lockfile::PackLock;
use crate::mode::filter::EffectiveMode;

use super::collect::collect_hooks;
use super::paths::stage_origin_packages;
use super::render::{write_rendered_files, RenderContext};

/// Collect hooks once, then render for every harness whose [`Harness::hook_renderer`] is `Some`.
/// Each harness's target root is its [`Harness::staged_root`]; Grok and Antigravity have no
/// renderer today, so they are skipped (with a warning) pending CLI smoke-test coverage.
///
/// [`Harness::hook_renderer`]: crate::harness::Harness::hook_renderer
/// [`Harness::staged_root`]: crate::harness::Harness::staged_root
pub fn stage_hooks_all_harnesses(
    project_root: &std::path::Path,
    lock: &PackLock,
    mode: &EffectiveMode,
) -> Result<()> {
    let mode_name = mode.name();
    // Native Codex hooks are seeded into `<codex_home>/hooks.json` and folded into the bundle.
    let codex_home = HarnessTarget::Codex
        .harness()
        .staged_root(project_root, mode_name)?;
    let bundle = collect_hooks(
        project_root,
        lock,
        Some(&codex_home.join("hooks.json")),
        mode,
    )?;
    if bundle.hooks.is_empty() {
        return Ok(());
    }
    tracing::warn!("Grok and Antigravity hook staging is disabled pending CLI smoke-test coverage");

    for harness in crate::harness::all() {
        let Some(renderer) = harness.hook_renderer() else {
            continue;
        };
        let target_root = harness.staged_root(project_root, mode_name)?;
        let staged_packages = stage_origin_packages(&bundle, harness.id(), &target_root, mode)?;
        let ctx = RenderContext {
            project_root,
            target_root: &target_root,
            staged_packages: &staged_packages,
        };
        let rendered = renderer.render(&bundle, &ctx)?;
        write_rendered_files(&rendered)?;
        tracing::info!(
            target = ?harness.id(),
            native = rendered.summary.native,
            emulated = rendered.summary.emulated,
            degraded = rendered.summary.degraded,
            omitted = rendered.summary.omitted,
            "staged hooks"
        );
        for diagnostic in rendered.diagnostics {
            match diagnostic.level {
                "omitted" | "degraded" => tracing::warn!(
                    target = ?harness.id(),
                    source = %diagnostic.source,
                    message = %diagnostic.message,
                    "hook diagnostic"
                ),
                _ => tracing::debug!(
                    target = ?harness.id(),
                    source = %diagnostic.source,
                    message = %diagnostic.message,
                    "hook diagnostic"
                ),
            }
        }
    }
    Ok(())
}
