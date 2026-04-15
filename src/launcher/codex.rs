use anyhow::Context;

use crate::launcher::common::{
    apply_yolo_codex, exec_with_env, resolve_harness_binary, single_dir_override,
};
use crate::paths;
use crate::sync::sync_for_launch;
use crate::ui::Ui;

pub fn run_codex(
    project_root: &std::path::Path,
    mut passthrough: Vec<String>,
    yolo: bool,
    ui: &Ui,
) -> anyhow::Result<()> {
    sync_for_launch(project_root, ui)?;

    if yolo {
        apply_yolo_codex(&mut passthrough);
    }

    let codex_home = single_dir_override(
        "AGENTPACK_CODEX_HOME",
        &paths::staging_codex_home_dir(project_root)?,
    );
    ui.debug_message(format!(
        "Codex home: {}",
        std::path::Path::new(&codex_home).display()
    ));

    let codex = resolve_harness_binary("CODEX_PATH", "codex").with_context(|| {
        "Codex CLI (`codex`) not found.\n\
         Install the Codex CLI and ensure `codex` is on your PATH, or set CODEX_PATH to the executable."
    })?;
    exec_with_env(&codex, &[("CODEX_HOME", codex_home)], passthrough)
}
