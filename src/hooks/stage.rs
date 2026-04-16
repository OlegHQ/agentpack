use crate::error::Result;
use crate::lockfile::PackLock;
use crate::manifest::AgentpackManifest;

use super::collect::collect_hooks;
use super::paths::stage_origin_packages;
use super::render::{
    write_rendered_files, ClaudeHookRenderer, CodexHookRenderer, CursorHookRenderer, HookRenderer,
    OpenCodeHookRenderer, RenderContext,
};

pub struct HookHarnessRoots<'a> {
    pub claude_bundle: &'a std::path::Path,
    pub opencode_root: &'a std::path::Path,
    pub codex_home: &'a std::path::Path,
    pub cursor_pack: &'a std::path::Path,
}

pub fn stage_hooks_all_harnesses(
    project_root: &std::path::Path,
    lock: &PackLock,
    manifest: Option<&AgentpackManifest>,
    roots: &HookHarnessRoots<'_>,
) -> Result<()> {
    let bundle = collect_hooks(
        project_root,
        lock,
        manifest,
        Some(&roots.codex_home.join("hooks.json")),
    )?;
    if bundle.hooks.is_empty() {
        return Ok(());
    }

    let renderers: Vec<(&dyn HookRenderer, &std::path::Path)> = vec![
        (&ClaudeHookRenderer, roots.claude_bundle),
        (&OpenCodeHookRenderer, roots.opencode_root),
        (&CodexHookRenderer, roots.codex_home),
        (&CursorHookRenderer, roots.cursor_pack),
    ];

    for (renderer, target_root) in renderers {
        let staged_packages = stage_origin_packages(&bundle, renderer.target(), target_root)?;
        let ctx = RenderContext {
            project_root,
            target_root,
            staged_packages: &staged_packages,
        };
        let rendered = renderer.render(&bundle, &ctx)?;
        write_rendered_files(&rendered)?;
        tracing::info!(
            target = ?renderer.target(),
            native = rendered.summary.native,
            emulated = rendered.summary.emulated,
            degraded = rendered.summary.degraded,
            omitted = rendered.summary.omitted,
            "staged hooks"
        );
        for diagnostic in rendered.diagnostics {
            match diagnostic.level {
                "omitted" | "degraded" => tracing::warn!(
                    target = ?renderer.target(),
                    source = %diagnostic.source,
                    message = %diagnostic.message,
                    "hook diagnostic"
                ),
                _ => tracing::debug!(
                    target = ?renderer.target(),
                    source = %diagnostic.source,
                    message = %diagnostic.message,
                    "hook diagnostic"
                ),
            }
        }
    }
    Ok(())
}
