pub(crate) mod common;

use std::path::Path;

use crate::harness::{HarnessTarget, LaunchCtx};
use crate::sync::sync_for_launch;
use crate::ui::Ui;

/// Sync staging for `id`, then build and exec that harness's configured `Command`. Replaces the
/// six per-harness `run_*` entry points: each harness owns its own `launch_command`.
pub fn launch(
    id: HarnessTarget,
    project_root: &Path,
    passthrough: Vec<String>,
    selected_mode: Option<&str>,
    yolo: bool,
    ui: &Ui,
) -> anyhow::Result<()> {
    let mode = sync_for_launch(project_root, selected_mode, id, ui)?;
    let ctx = LaunchCtx {
        project_root,
        passthrough,
        mode: &mode,
        yolo,
        ui,
    };
    let cmd = id.harness().launch_command(ctx)?;
    common::exec_inherit(cmd)
}
