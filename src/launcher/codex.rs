use anyhow::Context;

use crate::launcher::common::{apply_yolo_codex, exec_with_env, resolve_harness_binary};
use crate::paths;
use crate::staging::HarnessTarget;
use crate::sync::sync_for_launch;
use crate::ui::Ui;

pub fn run_codex(
    project_root: &std::path::Path,
    mut passthrough: Vec<String>,
    selected_mode: Option<&str>,
    yolo: bool,
    ui: &Ui,
) -> anyhow::Result<()> {
    let mode = sync_for_launch(project_root, selected_mode, HarnessTarget::Codex, ui)?;

    if yolo {
        apply_yolo_codex(&mut passthrough);
    }

    let codex_home = paths::staging_codex_home_dir_for_mode(project_root, mode.name())?;
    ui.debug_message(format!("Codex home: {}", codex_home.display()));

    let codex = resolve_harness_binary("CODEX_PATH", "codex").with_context(|| {
        "Codex CLI (`codex`) not found.\n\
         Install the Codex CLI and ensure `codex` is on your PATH, or set CODEX_PATH to the executable."
    })?;
    exec_with_env(&codex, &[("CODEX_HOME", codex_home.into())], passthrough)
}
