use std::ffi::OsString;
use std::path::Path;
use std::process::Command;

use anyhow::Context;

pub fn exec_with_env(
    executable_env: &str,
    default_executable: &str,
    envs: &[(&str, OsString)],
    passthrough: Vec<String>,
) -> anyhow::Result<()> {
    let executable =
        std::env::var(executable_env).unwrap_or_else(|_| default_executable.to_string());
    let mut cmd = Command::new(&executable);
    for (key, value) in envs {
        cmd.env(key, value);
    }
    cmd.args(passthrough);

    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        let err = cmd.exec();
        Err(anyhow::Error::new(err)).with_context(|| format!("failed to exec {executable}"))
    }

    #[cfg(not(unix))]
    {
        let status = cmd
            .status()
            .with_context(|| format!("failed to run {executable}"))?;
        std::process::exit(status.code().unwrap_or(1));
    }
}

pub fn single_dir_override(env_key: &str, staged_path: &Path) -> OsString {
    std::env::var_os(env_key).unwrap_or_else(|| staged_path.as_os_str().to_os_string())
}
