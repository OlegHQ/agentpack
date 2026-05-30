use anyhow::Context;

use crate::launcher::common::{apply_yolo_grok, exec_with_env, resolve_harness_binary};
use crate::paths;
use crate::staging::LaunchTarget;
use crate::sync::sync_for_launch;
use crate::ui::Ui;

pub fn run_grok(
    project_root: &std::path::Path,
    mut passthrough: Vec<String>,
    selected_mode: Option<&str>,
    yolo: bool,
    ui: &Ui,
) -> anyhow::Result<()> {
    let mode = sync_for_launch(project_root, selected_mode, LaunchTarget::Grok, ui)?;

    if yolo {
        apply_yolo_grok(&mut passthrough);
    }

    let grok_home = paths::staging_grok_home_dir_for_mode(project_root, mode.name())?;
    ui.debug_message(format!("Grok home: {}", grok_home.display()));

    let grok = resolve_harness_binary("GROK_PATH", "grok").with_context(|| {
        "Grok CLI (`grok`) not found.\n\
         Install Grok and ensure `grok` is on your PATH, or set GROK_PATH to the executable."
    })?;
    exec_with_env(
        &grok,
        &[("GROK_HOME", grok_home.into_os_string())],
        passthrough,
    )
}
