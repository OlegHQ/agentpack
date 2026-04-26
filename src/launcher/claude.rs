use std::process::Command;

use anyhow::Context;

use crate::launcher::common::{exec_inherit, resolve_harness_binary};
use crate::paths::staging_claude_config_dir_for_mode;
use crate::staging;
use crate::sync::sync_for_launch;
use crate::ui::Ui;

pub fn run_claude(
    project_root: &std::path::Path,
    mut passthrough: Vec<String>,
    selected_mode: Option<&str>,
    yolo: bool,
    ui: &Ui,
) -> anyhow::Result<()> {
    let mode = sync_for_launch(project_root, selected_mode, ui)?;

    if yolo {
        crate::launcher::common::apply_yolo_claude(&mut passthrough);
    }

    let plugin_dirs = staging::list_plugin_dirs(project_root, mode.name())?;
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

    let claude_cfg = staging_claude_config_dir_for_mode(project_root, mode.name())?;
    ui.debug_message(format!("CLAUDE_CONFIG_DIR={}", claude_cfg.display()));

    let mut cmd = Command::new(&claude);
    cmd.env("CLAUDE_CONFIG_DIR", &claude_cfg);
    for d in &plugin_dirs {
        cmd.arg("--plugin-dir").arg(d);
    }
    cmd.args(passthrough);
    exec_inherit(cmd)
}
