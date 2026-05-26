use anyhow::Context;

use crate::launcher::common::{apply_yolo_agy, exec_inherit, resolve_harness_binary};
use crate::sync::sync_for_launch;
use crate::ui::Ui;

fn args_have_add_dir(args: &[String]) -> bool {
    args.iter().enumerate().any(|(idx, arg)| {
        (arg == "--add-dir" && args.get(idx + 1).is_some()) || arg.starts_with("--add-dir=")
    })
}

pub fn run_agy(
    project_root: &std::path::Path,
    mut passthrough: Vec<String>,
    selected_mode: Option<&str>,
    yolo: bool,
    ui: &Ui,
) -> anyhow::Result<()> {
    let _mode = sync_for_launch(project_root, selected_mode, ui)?;

    if !args_have_add_dir(&passthrough) {
        passthrough.splice(
            0..0,
            ["--add-dir".to_string(), project_root.display().to_string()],
        );
    }
    if yolo {
        apply_yolo_agy(&mut passthrough);
    }

    ui.debug_message(format!(
        "Antigravity workspace (--add-dir): {}",
        project_root.display()
    ));

    let agy = resolve_harness_binary("AGY_PATH", "agy").with_context(|| {
        "Antigravity CLI (`agy`) not found.\n\
         Install Antigravity and ensure `agy` is on your PATH, or set AGY_PATH to the executable."
    })?;
    let mut cmd = std::process::Command::new(&agy);
    cmd.args(passthrough);
    exec_inherit(cmd)
}
