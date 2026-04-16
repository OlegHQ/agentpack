/// OpenCode config root entries we preserve before overlaying pack content.
pub(super) const OPENCODE_USER_ROOT_ENTRIES: &[&str] = &[
    "opencode.json",
    "agents",
    "commands",
    "modes",
    "plugins",
    "skills",
];

/// Codex home entries we preserve before overlaying pack content.
/// `auth.json` is linked separately so every staged `CODEX_HOME` shares the same refresh state.
pub(super) const CODEX_HOME_ENTRIES: &[&str] = &["config.toml", "hooks.json", "skills", "themes"];

/// Cursor files copied from `~/.cursor` into **`$STAGING/cursor/`** before pack overlay.
/// Omit **`agents` / `commands` / `skills` / `rules`**: those come from **`pack.lock`** under
/// **`agentpack-bundle/`**; copying from the real profile pulls dangling symlinks and duplicates UX.
pub(super) const CURSOR_USER_ROOT_ENTRIES: &[&str] = &["cli-config.json", "mcp.json"];

/// Top-level **`~/.cursor`** paths symlinked into **`$STAGING/cursor-home/.cursor`** for Cursor Agent auth/session.
pub(super) const CURSOR_FAKE_HOME_CREDENTIAL_FILES: &[&str] = &[
    "cli-config.json",
    "machineid",
    "agent-cli-state.json",
    "argv.json",
    "ide_state.json",
];

/// Symlinked into **`$FAKE_HOME/.cursor/User/`** so they resolve to the **same on-disk trees** Cursor’s GUI + CLI use for
/// workspace trust (`state.vscdb` under **`workspaceStorage`**) and global state — usually under **`Library/Application Support/Cursor/User`**
/// (macOS), not only **`~/.cursor/User`**.
pub(super) const CURSOR_USER_SUBDIRS_IN_FAKE_HOME: &[&str] = &["globalStorage", "workspaceStorage"];

/// Pack plugin dirs symlinked from **`agentpack-bundle/`** into **`$STAGING/cursor-home/.cursor`**.
pub(super) const CURSOR_FAKE_HOME_PACK_SUBDIRS: &[&str] = &[
    "commands", "agents", "skills", "rules", "hooks", "assets", "scripts",
];

/// Relative to **`./.cursor/`** — symlink **`./.cursor/agents`** → staged pack agents for Cursor **`agent`** (`computeAgentsDirs`).
pub(super) const CURSOR_WORKSPACE_AGENTS_OVERLAY: &str = "agents";


