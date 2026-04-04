use std::path::PathBuf;
use std::process::Command;

use crate::launcher::common::exec_inherit;
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

    let plugin_dirs: Vec<PathBuf> = if let Ok(env) = std::env::var("AGENTPACK_PLUGIN_DIRS") {
        env.split(':')
            .filter(|s| !s.is_empty())
            .map(PathBuf::from)
            .collect()
    } else {
        staging::list_plugin_dirs(project_root)?
    };
    if !plugin_dirs.is_empty() {
        let rendered = plugin_dirs
            .iter()
            .map(|dir| format!("  {}", dir.display()))
            .collect::<Vec<_>>()
            .join("\n");
        ui.debug_message(format!("Claude plugin dirs:\n{rendered}"));
    }

    let claude = std::env::var("CLAUDE_CODE_PATH").unwrap_or_else(|_| "claude".to_string());

    let mut cmd = Command::new(&claude);
    for d in &plugin_dirs {
        cmd.arg("--plugin-dir").arg(d);
    }
    cmd.args(passthrough);
    exec_inherit(cmd)
}
