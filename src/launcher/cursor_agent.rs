use std::ffi::OsString;
use std::path::{Path, PathBuf};

use anyhow::Context;

use crate::launcher::common::{
    apply_yolo_cursor_agent, exec_with_env, resolve_harness_binary, single_dir_override,
};
use crate::paths;
use crate::sync::sync_for_launch;
use crate::ui::Ui;

fn explicit_workspace_arg(args: &[String]) -> Option<PathBuf> {
    for (idx, arg) in args.iter().enumerate() {
        if arg == "--workspace" {
            return args.get(idx + 1).map(PathBuf::from);
        }
        if let Some(value) = arg.strip_prefix("--workspace=") {
            return Some(PathBuf::from(value));
        }
    }
    None
}

fn args_contain_trust_flag(args: &[String]) -> bool {
    args.iter().any(|a| a == "--trust")
}

/// Cursor **`agent`** only allows **`--trust`** together with **`--print`** / stream output (`--trust can only be used with --print/headless mode`).
fn args_allow_trust_with_print(args: &[String]) -> bool {
    for (i, a) in args.iter().enumerate() {
        if a == "-p" || a == "--print" {
            return true;
        }
        if a.starts_with("--output-format=") {
            return true;
        }
        if a == "--output-format" {
            return args.get(i + 1).is_some();
        }
    }
    false
}

/// Prepends **`--trust`** only in headless mode (when **`--print`** / **`-p`** / **`--output-format`** is present). Set **`AGENTPACK_CURSOR_AGENT_TRUST=0`** to skip even then.
fn prepend_trust_if_configured(args: &mut Vec<String>) {
    let raw = std::env::var("AGENTPACK_CURSOR_AGENT_TRUST").unwrap_or_default();
    let lower = raw.to_ascii_lowercase();
    if matches!(lower.as_str(), "0" | "false" | "no" | "off") {
        return;
    }
    if !args_allow_trust_with_print(args) {
        return;
    }
    if args_contain_trust_flag(args) {
        return;
    }
    args.insert(0, "--trust".into());
}

fn normalize_path(path: &Path) -> PathBuf {
    path.canonicalize().unwrap_or_else(|_| path.to_path_buf())
}

fn push_env_if_absent(envs: &mut Vec<(&'static str, OsString)>, key: &'static str, value: PathBuf) {
    if std::env::var_os(key).is_none() {
        envs.push((key, value.into_os_string()));
    }
}

pub fn run_agent(
    project_root: &std::path::Path,
    passthrough: Vec<String>,
    yolo: bool,
    ui: &Ui,
) -> anyhow::Result<()> {
    sync_for_launch(project_root, ui)?;

    let fake_home = single_dir_override(
        "AGENTPACK_CURSOR_HOME",
        &paths::staging_cursor_home_dir(project_root)?,
    );

    let project_norm = normalize_path(project_root);

    let mut args = passthrough;
    let workspace = match explicit_workspace_arg(&args) {
        Some(p) => normalize_path(&p),
        None => {
            args.splice(
                0..0,
                [
                    "--workspace".to_string(),
                    project_norm.display().to_string(),
                ],
            );
            project_norm.clone()
        }
    };

    let mut envs: Vec<(&str, OsString)> = vec![("HOME", fake_home.clone())];
    #[cfg(windows)]
    {
        envs.push(("USERPROFILE", fake_home.clone()));
        let roaming = Path::new(&fake_home).join("AppData").join("Roaming");
        let local = Path::new(&fake_home).join("AppData").join("Local");
        envs.push(("APPDATA", roaming.into_os_string()));
        envs.push(("LOCALAPPDATA", local.into_os_string()));
    }
    #[cfg(target_os = "linux")]
    {
        let cfg = Path::new(&fake_home).join(".config");
        envs.push(("XDG_CONFIG_HOME", cfg.into_os_string()));
        let data = Path::new(&fake_home).join(".local/share");
        envs.push(("XDG_DATA_HOME", data.into_os_string()));
    }

    // Cursor skill / command / agent discovery still appears tied to the HOME-backed `.cursor`
    // tree, so keep the fake HOME layout that Cursor already knows how to scan.
    let fake_cursor = Path::new(&fake_home).join(".cursor");
    let cursor_config_dir = if let Some(dir) = std::env::var_os("AGENTPACK_CURSOR_CONFIG_DIR") {
        dir
    } else {
        fake_cursor.into_os_string()
    };
    envs.push(("CURSOR_CONFIG_DIR", cursor_config_dir));

    if let Some(real_home) = dirs::home_dir() {
        push_env_if_absent(&mut envs, "CARGO_HOME", real_home.join(".cargo"));
        push_env_if_absent(&mut envs, "RUSTUP_HOME", real_home.join(".rustup"));
        push_env_if_absent(&mut envs, "DOCKER_CONFIG", real_home.join(".docker"));
    }

    // Cursor **`agent`** (bundled `cursor-config` + `workspace/approval.tsx`) stores **workspace trust** at
    // **`$CURSOR_DATA_DIR/projects/<slug>/.workspace-trusted`**, defaulting **`CURSOR_DATA_DIR`** to
    // **`os.homedir()/.cursor`**. With **`HOME`** redirected to staging, that would land under ephemeral
    // **`$STAGING/cursor-home/.cursor`** and be deleted every **`sync`**. Keep project/trust state on the
    // real profile unless the environment already sets **`CURSOR_DATA_DIR`**.
    let mut injected_cursor_data_dir = false;
    if std::env::var_os("CURSOR_DATA_DIR").is_none() {
        if let Some(h) = dirs::home_dir() {
            envs.push(("CURSOR_DATA_DIR", h.join(".cursor").into_os_string()));
            injected_cursor_data_dir = true;
        }
    }

    let mut msg = format!(
        "Cursor Agent workspace (--workspace): {}\nCursor fake HOME (agentpack): {}",
        workspace.display(),
        Path::new(&fake_home).display()
    );
    if std::env::var_os("AGENTPACK_CURSOR_CONFIG_DIR").is_some() {
        msg.push_str("\nCURSOR_CONFIG_DIR: from AGENTPACK_CURSOR_CONFIG_DIR");
    } else {
        msg.push_str("\nCURSOR_CONFIG_DIR: fake HOME .cursor (pack agents/commands)");
    }
    if std::env::var_os("CARGO_HOME").is_none() {
        msg.push_str("\nCARGO_HOME: real ~/.cargo");
    }
    if std::env::var_os("RUSTUP_HOME").is_none() {
        msg.push_str("\nRUSTUP_HOME: real ~/.rustup");
    }
    if std::env::var_os("DOCKER_CONFIG").is_none() {
        msg.push_str("\nDOCKER_CONFIG: real ~/.docker");
    }
    if injected_cursor_data_dir {
        msg.push_str("\nCURSOR_DATA_DIR: real ~/.cursor (workspace trust + projects; avoids ephemeral staging)");
    }
    if yolo {
        apply_yolo_cursor_agent(&mut args);
    }
    prepend_trust_if_configured(&mut args);
    ui.debug_message(msg);

    let agent = resolve_harness_binary("CURSOR_AGENT_PATH", "agent").with_context(|| {
        "Cursor Agent CLI (`agent`) not found.\n\
         Install Cursor with the Agent CLI available on your PATH, or set CURSOR_AGENT_PATH to the `agent` executable."
    })?;
    exec_with_env(&agent, &envs, args)
}
