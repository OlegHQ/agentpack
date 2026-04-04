use crate::launcher::common::{exec_with_env, single_dir_override};
use crate::paths;
use crate::sync::sync_for_launch;
use crate::ui::Ui;

pub fn run_opencode(
    project_root: &std::path::Path,
    passthrough: Vec<String>,
    ui: &Ui,
) -> anyhow::Result<()> {
    sync_for_launch(project_root, ui)?;

    let config_dir = single_dir_override(
        "AGENTPACK_OPENCODE_CONFIG_DIR",
        &paths::staging_opencode_dir(project_root)?,
    );
    ui.message(format!(
        "OpenCode config dir: {}",
        std::path::Path::new(&config_dir).display()
    ));

    exec_with_env(
        "OPENCODE_PATH",
        "opencode",
        &[("OPENCODE_CONFIG_DIR", config_dir)],
        passthrough,
    )
}
