use std::process::Command;

use anyhow::Context;

use crate::launcher::common::{exec_inherit, resolve_harness_binary};
use crate::staging;
use crate::sync::sync_for_launch;
use crate::ui::Ui;

pub fn run_claude(
    project_root: &std::path::Path,
    mut passthrough: Vec<String>,
    yolo: bool,
    ui: &Ui,
) -> anyhow::Result<()> {
    sync_for_launch(project_root, ui)?;

    if yolo {
        crate::launcher::common::apply_yolo_claude_opencode(&mut passthrough);
    }

    let plugin_dirs = staging::list_plugin_dirs(project_root)?;
    if !plugin_dirs.is_empty() {
        let rendered = plugin_dirs
            .iter()
            .map(|dir| format!("  {}", dir.display()))
            .collect::<Vec<_>>()
            .join("\n");
        ui.debug_message(format!("Claude plugin dirs:\n{rendered}"));
    }

    let claude = resolve_harness_binary("CLAUDE_CODE_PATH", "claude").with_context(|| {
        "Claude Code CLI (`claude`) not found.\n\
         Install Claude Code and ensure `claude` is on your PATH, or set CLAUDE_CODE_PATH to the executable."
    })?;

    let mut cmd = Command::new(&claude);
    for d in &plugin_dirs {
        cmd.arg("--plugin-dir").arg(d);
    }
    cmd.args(passthrough);
    exec_inherit(cmd)
}
