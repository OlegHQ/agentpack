use anyhow::Context;

use crate::fs_util::{read_json_value_opt, write_json_value};
use crate::launcher::common::{exec_with_env, resolve_harness_binary};
use crate::paths;
use crate::sync::sync_for_launch;
use crate::ui::Ui;

pub fn run_opencode(
    project_root: &std::path::Path,
    passthrough: Vec<String>,
    selected_mode: Option<&str>,
    yolo: bool,
    ui: &Ui,
) -> anyhow::Result<()> {
    let mode = sync_for_launch(project_root, selected_mode, ui)?;

    let config_dir = paths::staging_opencode_dir_for_mode(project_root, mode.name())?;
    ui.debug_message(format!("OpenCode config dir: {}", config_dir.display()));

    if yolo {
        apply_yolo_opencode_config(&config_dir)?;
    }

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

/// OpenCode has no CLI flag for bypassing permissions; it reads `permission` from
/// `opencode.json`. Patch the staged config so `agentpack --yolo opencode` actually
/// skips prompts instead of falling through to OpenCode's help screen.
fn apply_yolo_opencode_config(config_dir: &std::path::Path) -> anyhow::Result<()> {
    let config_path = config_dir.join("opencode.json");
    let mut value = read_json_value_opt(&config_path)?.unwrap_or_else(|| serde_json::json!({}));
    let Some(obj) = value.as_object_mut() else {
        anyhow::bail!(
            "staged {} is not a JSON object; cannot apply --yolo",
            config_path.display()
        );
    };
    obj.insert("permission".into(), serde_json::json!("allow"));
    write_json_value(&config_path, &value)?;
    Ok(())
}
