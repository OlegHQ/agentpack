use std::path::PathBuf;

use clap::{Parser, Subcommand};

#[derive(Parser)]
#[command(
    name = "agentpack",
    version,
    about = "Pin skills/plugins via agentpack.toml and pack.lock"
)]
pub struct Cli {
    /// Project root containing agentpack.toml or pack.lock (default: search upward from cwd)
    #[arg(long, global = true)]
    pub project_root: Option<PathBuf>,

    /// Only print warnings and errors (also sets RUST_LOG-style filter to warn for tracing)
    #[arg(long, global = true, short = 'q')]
    pub quiet: bool,

    /// Disable spinners and progress bars (plain output)
    #[arg(long, global = true)]
    pub no_progress: bool,

    /// Forward each harness's "skip permission prompts" / full-access mode (Claude/OpenCode: `--dangerously-skip-permissions`; Codex: `--dangerously-bypass-approvals-and-sandbox`; Cursor `agent`: `--force`)
    #[arg(long, global = true)]
    pub yolo: bool,

    /// Print launcher diagnostics (workspace paths, env overrides, fast-sync skip reason)
    #[arg(long, global = true)]
    pub debug: bool,

    #[command(subcommand)]
    pub command: Command,
}

#[derive(Subcommand)]
pub enum Command {
    /// Create **agentpack.toml** + **pack.lock** (v2) and ensure **`AGENTPACK_HOME`**
    Init {
        #[arg(long)]
        name: Option<String>,
        #[arg(long)]
        version: Option<String>,
    },
    /// Resolve **agentpack.toml** and refresh **pack.lock** (direct + transitive packages)
    Lock {
        /// Re-resolve floating pins from GitHub instead of keeping commits already in **pack.lock**
        #[arg(long)]
        update: bool,
    },
    /// Resolve a package spec and append its module id under **[dependencies]** in `agentpack.toml` (requires manifest); then resolve and sync unless `--no-sync`
    Add {
        spec: String,
        #[arg(long)]
        no_sync: bool,
    },
    /// Drop a direct dependency from **agentpack.toml** (and its **[overrides]**), refresh **pack.lock**, then **sync** unless `--no-sync`
    Remove {
        spec: String,
        #[arg(long)]
        no_sync: bool,
    },
    /// Ensure cache + staging for all skills in pack.lock
    Sync {
        #[arg(long)]
        dry_run: bool,
        #[arg(long)]
        verify_only: bool,
        /// When refreshing **pack.lock**, re-resolve floating pins from GitHub (same as **`lock --update`**)
        #[arg(long)]
        update_lock: bool,
    },
    /// Run `claude` with `--plugin-dir` for each staged skill plugin
    Claude {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// Run `opencode` with `OPENCODE_CONFIG_DIR` pointing at the staged agentpack root
    Opencode {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// Run `codex` with `CODEX_HOME` pointing at the staged agentpack home
    Codex {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// Run Cursor Agent (`agent`) with `HOME` set to a staged tree that symlinks pack `.cursor` assets and your real Cursor login/session files
    Agent {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
}
