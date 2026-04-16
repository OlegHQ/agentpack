use anyhow::Context;

use crate::launcher::common::{
    apply_yolo_claude_opencode, exec_with_env, resolve_harness_binary,
};
use crate::paths;
use crate::sync::sync_for_launch;
use crate::ui::Ui;

pub fn run_opencode(
    project_root: &std::path::Path,
    mut passthrough: Vec<String>,
    yolo: bool,
    ui: &Ui,
) -> anyhow::Result<()> {
    sync_for_launch(project_root, ui)?;

    if yolo {
        apply_yolo_claude_opencode(&mut passthrough);
    }

    let config_dir = paths::staging_opencode_dir(project_root)?;
    ui.debug_message(format!(
        "OpenCode config dir: {}",
        config_dir.display()
    ));

    let opencode = resolve_harness_binary("OPENCODE_PATH", "opencode").with_context(|| {
        "OpenCode CLI (`opencode`) not found.\n\
         Install OpenCode and ensure `opencode` is on your PATH, or set OPENCODE_PATH to the executable."
    })?;
    exec_with_env(
        &opencode,
        &[("OPENCODE_CONFIG_DIR", config_dir.into())],
        passthrough,
    )
}
