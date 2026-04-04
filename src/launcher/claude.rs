use std::path::PathBuf;
use std::process::Command;

use anyhow::Context;

use crate::staging;
use crate::sync::sync_for_launch;
use crate::ui::Ui;

pub fn run_claude(
    project_root: &std::path::Path,
    passthrough: Vec<String>,
    ui: &Ui,
) -> anyhow::Result<()> {
    sync_for_launch(project_root, ui)?;

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
        ui.message(format!("Claude plugin dirs:\n{rendered}"));
    }

    let claude = std::env::var("CLAUDE_CODE_PATH").unwrap_or_else(|_| "claude".to_string());

    let mut cmd = Command::new(&claude);
    for d in &plugin_dirs {
        cmd.arg("--plugin-dir").arg(d);
    }
    cmd.args(passthrough);

    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        let err = cmd.exec();
        Err(anyhow::Error::new(err)).with_context(|| format!("failed to exec {claude}"))
    }

    #[cfg(not(unix))]
    {
        let status = cmd
            .status()
            .with_context(|| format!("failed to run {claude}"))?;
        std::process::exit(status.code().unwrap_or(1));
    }
}
