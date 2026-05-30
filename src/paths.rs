use std::env;
use std::fs;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};
use sha2::{Digest, Sha256};

pub const LOCKFILE_NAME: &str = "pack.lock";

pub const MANIFEST_NAME: &str = "agentpack.toml";

/// Merged Claude `--plugin-dir` folder name; same id is used for the staged Cursor plugin tree.
pub const STAGED_AGENTPACK_BUNDLE_NAME: &str = "agentpack-bundle";

/// Project-local [dot-agents](https://github.com/dot-agents/dot-agents)-style config tree merged into
/// harness staging trees on **`sync`** (rules, skills, per-tool overlays; see `staging::stage_dot_agents_overlay`).
pub const DOT_AGENTS_DIR: &str = ".agents";

pub fn project_dot_agents_dir(project_root: &Path) -> PathBuf {
    project_root.join(DOT_AGENTS_DIR)
}

pub fn manifest_path(project_root: &Path) -> PathBuf {
    project_root.join(MANIFEST_NAME)
}

/// User-level store root: **`AGENTPACK_HOME`**, else XDG data dir (Unix) or **`LOCALAPPDATA\\agentpack`** (Windows).
pub fn user_agentpack_home() -> Result<PathBuf> {
    if let Ok(p) = env::var("AGENTPACK_HOME") {
        let p = p.trim();
        if !p.is_empty() {
            return Ok(PathBuf::from(p));
        }
    }
    #[cfg(windows)]
    {
        dirs::data_local_dir()
            .map(|d| d.join("agentpack"))
            .ok_or_else(|| AgentpackError::Cache("cannot resolve LOCALAPPDATA".into()))
    }
    #[cfg(not(windows))]
    {
        let data_home = env::var_os("XDG_DATA_HOME")
            .filter(|s| !s.is_empty())
            .map(PathBuf::from)
            .or_else(|| dirs::home_dir().map(|h| h.join(".local/share")))
            .ok_or_else(|| AgentpackError::Cache("cannot resolve XDG data home".into()))?;
        Ok(data_home.join("agentpack"))
    }
}

/// Create **`cache/`**, **`local/`**, **`projects/`** under the user agentpack home.
pub fn ensure_user_agentpack_layout() -> Result<PathBuf> {
    let root = user_agentpack_home()?;
    for sub in ["cache", "local", "projects"] {
        let p = root.join(sub);
        fs::create_dir_all(&p).map_err(|e| AgentpackError::io(&p, e))?;
    }
    Ok(root)
}

pub fn cache_dir() -> Result<PathBuf> {
    Ok(user_agentpack_home()?.join("cache"))
}

pub fn cache_db_path() -> Result<PathBuf> {
    Ok(cache_dir()?.join("db.reddb"))
}

/// `local/anthropics/skills/foo` layout for golden-spec mirrors.
pub fn local_registry_root() -> Result<PathBuf> {
    Ok(user_agentpack_home()?.join("local"))
}

pub fn local_mirror_path_from_shorthand(spec: &str) -> Result<PathBuf> {
    Ok(local_registry_root()?.join(spec))
}

pub fn project_state_dir(project_root: &Path) -> Result<PathBuf> {
    let hash = project_path_hash(project_root)?;
    Ok(user_agentpack_home()?.join("projects").join(hash))
}

pub fn cursor_overlay_manifest_path(project_root: &Path) -> Result<PathBuf> {
    Ok(project_state_dir(project_root)?.join("cursor-overlay.manifest"))
}

pub fn agy_overlay_manifest_path(project_root: &Path) -> Result<PathBuf> {
    Ok(project_state_dir(project_root)?.join("agy-overlay.manifest"))
}

/// Resolve project root by walking ancestors until **`agentpack.toml`** or **`pack.lock`** is found.
pub fn find_project_root(start: &Path) -> Result<PathBuf> {
    for dir in start.ancestors() {
        if dir.join(MANIFEST_NAME).is_file() || dir.join(LOCKFILE_NAME).is_file() {
            return Ok(dir.to_path_buf());
        }
    }
    Err(AgentpackError::NoPackLock(start.to_path_buf()))
}

/// Discover root: explicit `--project-root`, or walk from cwd.
pub fn resolve_project_root(explicit: Option<&Path>) -> Result<PathBuf> {
    if let Some(p) = explicit {
        let p = p.canonicalize().map_err(|e| AgentpackError::io(p, e))?;
        if p.join(MANIFEST_NAME).is_file() || p.join(LOCKFILE_NAME).is_file() {
            return Ok(p);
        }
        return Err(AgentpackError::NoPackLock(p));
    }
    let cwd = env::current_dir().map_err(|e| AgentpackError::io(PathBuf::from("."), e))?;
    find_project_root(&cwd)
}

pub fn lock_path(project_root: &Path) -> PathBuf {
    project_root.join(LOCKFILE_NAME)
}

/// Stable short hash of canonical project root for staging directory name.
pub fn project_path_hash(project_root: &Path) -> Result<String> {
    let canon = project_root
        .canonicalize()
        .map_err(|e| AgentpackError::io(project_root, e))?;
    let mut h = Sha256::new();
    h.update(canon.as_os_str().as_encoded_bytes());
    let full = h.finalize();
    Ok(hex::encode(&full[..8]))
}

/// Staging root: `std::env::temp_dir()/agentpack-<hash>` unless `AGENTPACK_STAGING_ROOT` is set.
fn staging_root_base(project_root: &Path) -> Result<PathBuf> {
    if let Ok(override_path) = env::var("AGENTPACK_STAGING_ROOT") {
        return Ok(PathBuf::from(override_path));
    }
    let hash = project_path_hash(project_root)?;
    Ok(env::temp_dir().join(format!("agentpack-{hash}")))
}

pub fn mode_path_component(mode_name: &str) -> String {
    if mode_name == "default" {
        return "default".into();
    }

    let mut slug = crate::slug::dashed_lower(mode_name);
    if slug.is_empty() {
        slug = "mode".into();
    }
    let mut hasher = Sha256::new();
    hasher.update(mode_name.as_bytes());
    let digest = hex::encode(&hasher.finalize()[..4]);
    format!("{slug}-{digest}")
}

pub fn launch_sync_state_path(project_root: &Path, mode_name: &str) -> Result<PathBuf> {
    Ok(project_state_dir(project_root)?.join(format!(
        "launch-sync-{}.state",
        mode_path_component(mode_name)
    )))
}

pub fn staging_root_for_mode(project_root: &Path, mode_name: &str) -> Result<PathBuf> {
    Ok(staging_root_base(project_root)?
        .join("modes")
        .join(mode_path_component(mode_name)))
}

pub fn staging_root(project_root: &Path) -> Result<PathBuf> {
    staging_root_for_mode(project_root, "default")
}

fn staging_subdir(project_root: &Path, mode_name: &str, segment: &str) -> Result<PathBuf> {
    Ok(staging_root_for_mode(project_root, mode_name)?.join(segment))
}

macro_rules! staging_dir_pair {
    ($(#[$doc:meta])* $for_mode:ident, $default:ident, $segment:literal) => {
        $(#[$doc])*
        pub fn $for_mode(project_root: &Path, mode_name: &str) -> Result<PathBuf> {
            staging_subdir(project_root, mode_name, $segment)
        }

        pub fn $default(project_root: &Path) -> Result<PathBuf> {
            staging_subdir(project_root, "default", $segment)
        }
    };
}

staging_dir_pair!(
    #[doc = "Per-project plugin staging: each skill becomes a minimal plugin tree here."]
    staging_plugins_dir_for_mode,
    staging_plugins_dir,
    "plugins"
);
staging_dir_pair!(
    staging_opencode_dir_for_mode,
    staging_opencode_dir,
    "opencode"
);
staging_dir_pair!(
    staging_codex_home_dir_for_mode,
    staging_codex_home_dir,
    "codex-home"
);
staging_dir_pair!(
    staging_grok_home_dir_for_mode,
    staging_grok_home_dir,
    "grok-home"
);
staging_dir_pair!(staging_grok_dir_for_mode, staging_grok_dir, "grok");

pub fn staging_grok_bundle_dir_for_mode(project_root: &Path, mode_name: &str) -> Result<PathBuf> {
    Ok(staging_grok_dir_for_mode(project_root, mode_name)?.join(STAGED_AGENTPACK_BUNDLE_NAME))
}

pub fn staging_grok_bundle_dir(project_root: &Path) -> Result<PathBuf> {
    staging_grok_bundle_dir_for_mode(project_root, "default")
}

staging_dir_pair!(staging_agy_dir_for_mode, staging_agy_dir, "agy");

pub fn staging_agy_bundle_dir_for_mode(project_root: &Path, mode_name: &str) -> Result<PathBuf> {
    Ok(staging_agy_dir_for_mode(project_root, mode_name)?.join(STAGED_AGENTPACK_BUNDLE_NAME))
}

pub fn staging_agy_bundle_dir(project_root: &Path) -> Result<PathBuf> {
    staging_agy_bundle_dir_for_mode(project_root, "default")
}

/// Shared Codex credential cache for staged homes when the real user config uses keyring-backed
/// auth and therefore has no reusable `~/.codex/auth.json`.
pub fn shared_codex_auth_path() -> Result<PathBuf> {
    Ok(user_agentpack_home()?
        .join("shared")
        .join("codex")
        .join("auth.json"))
}

staging_dir_pair!(
    staging_cursor_bundle_dir_for_mode,
    staging_cursor_bundle_dir,
    "cursor"
);

/// Staged Cursor plugin root: **`$STAGING/cursor/<bundle>/`** with **`.cursor-plugin/plugin.json`**, sibling to **`$STAGING/cursor/.cursor-plugin/marketplace.json`** (Cursor multi-plugin repo layout).
pub fn staging_cursor_pack_plugin_dir_for_mode(
    project_root: &Path,
    mode_name: &str,
) -> Result<PathBuf> {
    Ok(staging_cursor_bundle_dir_for_mode(project_root, mode_name)?
        .join(STAGED_AGENTPACK_BUNDLE_NAME))
}

pub fn staging_cursor_pack_plugin_dir(project_root: &Path) -> Result<PathBuf> {
    staging_cursor_pack_plugin_dir_for_mode(project_root, "default")
}

staging_dir_pair!(
    #[doc = "Fake **`$HOME`** for **`agentpack agent`**: contains **`.cursor/`** with symlinks to pack content and to your real Cursor auth/session files."]
    staging_cursor_home_dir_for_mode,
    staging_cursor_home_dir,
    "cursor-home"
);

/// Stable settings overlay file passed to `claude --settings <path>`. Lives under
/// `$AGENTPACK_HOME` (not staging) so its location does not depend on the project, mode, or temp
/// dir. This is load-bearing: Claude Code (verified against the v2.1.119 bundle) namespaces both
/// the macOS keychain service name (`Claude Code-credentials-<sha256(CLAUDE_CONFIG_DIR)[:8]>`) and
/// the file fallback path (`$CLAUDE_CONFIG_DIR/.credentials.json`) by `CLAUDE_CONFIG_DIR`. Putting
/// our overrides under per-project staging would forget login on every project switch and macOS
/// reboot. So we don't redirect `CLAUDE_CONFIG_DIR` at all — we pass `--settings` instead, which
/// loads as `flagSettings` (precedence above user/project/local) without touching the keychain
/// namespace.
pub fn agentpack_claude_settings_path() -> Result<PathBuf> {
    Ok(user_agentpack_home()?.join("claude-settings.json"))
}

pub fn cursor_workspace_dir(project_root: &Path) -> PathBuf {
    project_root.join(".cursor")
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use tempfile::tempdir;

    #[test]
    fn walk_up_finds_pack_lock() {
        let dir = tempdir().unwrap();
        let root = dir.path();
        fs::write(root.join(MANIFEST_NAME), "name = \"t\"\nversion = \"1\"\n").unwrap();
        fs::write(
            root.join(LOCKFILE_NAME),
            "lockfile-version = 2\n[meta]\nname = \"t\"\nversion = \"1\"\n",
        )
        .unwrap();
        let nested = root.join("deep/nested");
        fs::create_dir_all(&nested).unwrap();
        let found = find_project_root(&nested).unwrap();
        assert_eq!(found.canonicalize().unwrap(), root.canonicalize().unwrap());
    }

    #[test]
    fn staging_roots_are_mode_specific() {
        let dir = tempdir().unwrap();
        let root = dir.path();
        fs::write(root.join(MANIFEST_NAME), "name = \"t\"\nversion = \"1\"\n").unwrap();
        fs::write(
            root.join(LOCKFILE_NAME),
            "lockfile-version = 2\n[meta]\nname = \"t\"\nversion = \"1\"\n",
        )
        .unwrap();
        let default_root = staging_root_for_mode(root, "default").unwrap();
        let design_root = staging_root_for_mode(root, "design").unwrap();
        assert_ne!(default_root, design_root);
        assert!(design_root.to_string_lossy().contains("design"));
    }
}
