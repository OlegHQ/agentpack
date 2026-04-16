pub mod dispatch;
pub mod hook_exec;

use std::path::PathBuf;

use clap::{Args, Parser, Subcommand};

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
    /// Manage MCP server definitions in agentpack.toml
    Mcp {
        #[command(subcommand)]
        action: McpAction,
    },
    /// Internal bridge used by rendered hook configs across harnesses
    HookExec(HookExecArgs),
}

#[derive(Subcommand)]
pub enum McpAction {
    /// Add an MCP server to [mcp.servers] in agentpack.toml
    Add {
        /// Server name (e.g. "filesystem", "retrieval")
        name: String,
        /// Command to run the MCP server
        #[arg(long)]
        command: String,
        /// Arguments for the command
        #[arg(long, num_args = 0..)]
        args: Vec<String>,
        /// Environment variables (KEY=VALUE pairs)
        #[arg(long, value_parser = parse_env_pair, num_args = 0..)]
        env: Vec<(String, String)>,
        /// Skip sync after adding
        #[arg(long)]
        no_sync: bool,
    },
    /// Remove an MCP server from [mcp.servers] in agentpack.toml
    Remove {
        /// Server name to remove
        name: String,
        /// Skip sync after removing
        #[arg(long)]
        no_sync: bool,
    },
    /// List all MCP servers (from manifest, plugins, and .agents)
    List,
}

fn parse_env_pair(s: &str) -> std::result::Result<(String, String), String> {
    let (k, v) = s
        .split_once('=')
        .ok_or_else(|| format!("expected KEY=VALUE, got {s:?}"))?;
    Ok((k.to_string(), v.to_string()))
}

#[derive(Args)]
pub struct HookExecArgs {
    #[command(subcommand)]
    pub kind: HookExecKind,
    #[arg(long, global = true)]
    pub target: crate::hooks::ir::HookOutputTarget,
    #[arg(long, global = true)]
    pub spec: PathBuf,
}

#[derive(Subcommand, Clone, Copy)]
pub enum HookExecKind {
    Command,
    Http,
    Prompt,
    Agent,
}
