pub mod dispatch;
pub mod hook_exec;

use std::path::PathBuf;

use clap::{Args, Parser, Subcommand, ValueEnum};

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

    /// Forward each harness's "skip permission prompts" / full-access mode (Claude: `--dangerously-skip-permissions`; OpenCode: stage `opencode.json` with `"permission": "allow"`; Codex: `--dangerously-bypass-approvals-and-sandbox`; Cursor `agent`: `--force`; Grok: `--always-approve`; Antigravity: `--dangerously-skip-permissions`)
    #[arg(long, global = true)]
    pub yolo: bool,

    /// Select a project-local mode from `agentpack.toml [modes]` (defaults to the reserved `default` mode)
    #[arg(long, global = true)]
    pub mode: Option<String>,

    /// Print launcher diagnostics (workspace paths, env overrides, fast-sync skip reason)
    #[arg(long, global = true)]
    pub debug: bool,

    /// For `agentpack claude`, start a supervised Anthropic-compatible proxy backed by OpenAI/Codex auth
    #[arg(long, global = true)]
    pub proxy: bool,

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
    /// Resolve a package spec and append its module id under **[dependencies]** in `agentpack.toml`; lazily initializes project files when missing, then resolves and syncs unless `--no-sync`
    Add {
        spec: String,
        #[arg(long)]
        no_sync: bool,
    },
    /// Drop a direct dependency from **agentpack.toml**, refresh **pack.lock**, then **sync** unless `--no-sync`
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
    /// Run `grok` with `GROK_HOME` pointing at the staged agentpack home
    Grok {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// Run Antigravity CLI (`agy`) with the project added as a workspace directory
    Agy {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// Run Cursor Agent (`cursor-agent`) with `HOME` set to a staged tree that symlinks pack `.cursor` assets and your real Cursor login/session files
    #[command(visible_alias = "cursor-agent")]
    Agent {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// Manage MCP server definitions in agentpack.toml
    Mcp {
        #[command(subcommand)]
        action: McpAction,
    },
    /// Manage project-local modes stored in `agentpack.toml`
    Mode {
        #[command(subcommand)]
        action: ModeAction,
    },
    /// Internal bridge used by rendered hook configs across harnesses
    HookExec(HookExecArgs),
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, ValueEnum)]
pub enum ModeBaseArg {
    All,
    None,
}

impl From<ModeBaseArg> for crate::mode::ModeBase {
    fn from(value: ModeBaseArg) -> Self {
        match value {
            ModeBaseArg::All => crate::mode::ModeBase::All,
            ModeBaseArg::None => crate::mode::ModeBase::None,
        }
    }
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
        /// Arguments for the command (values may start with `-`, e.g. `--args -y pkg`)
        #[arg(long, num_args = 0.., allow_hyphen_values = true)]
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

#[derive(Subcommand)]
pub enum ModeAction {
    /// List all modes declared in `agentpack.toml`
    List,
    /// Show one mode's base, enable list, and disable list
    Show { name: String },
    /// Create a new mode
    Create { name: String },
    /// Delete a mode (`default` is reserved and cannot be deleted)
    Delete { name: String },
    /// Enable one or more selectors within a mode
    Enable {
        name: String,
        selectors: Vec<String>,
    },
    /// Disable one or more selectors within a mode
    Disable {
        name: String,
        selectors: Vec<String>,
    },
    /// Set a mode's base to `all` or `none`
    Base { name: String, base: ModeBaseArg },
    /// Open the interactive mode editor
    Tui { name: Option<String> },
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
}

#[derive(Subcommand, Clone)]
pub enum HookExecKind {
    Command(HookExecSpecArgs),
    Http(HookExecSpecArgs),
    Prompt(HookExecSpecArgs),
    Agent(HookExecSpecArgs),
    /// Host-side matcher router: reads harness stdin, matches stored specs, fires handlers.
    /// Used by Cursor so a single blanket hook entry per event can emulate Claude's fine-grained matchers.
    Dispatch(HookDispatchArgs),
    /// Emit a plugin-provided guidance blob as the target harness's `additionalContext` JSON.
    /// Invoked from a `SessionStart` hook in the Claude bundle so the model always sees the blob.
    InjectGuidance(HookInjectGuidanceArgs),
}

#[derive(Args, Clone)]
pub struct HookExecSpecArgs {
    #[arg(long)]
    pub target: crate::harness::HarnessTarget,
    #[arg(long)]
    pub spec: PathBuf,
}

#[derive(Args, Clone)]
pub struct HookDispatchArgs {
    #[arg(long)]
    pub target: crate::harness::HarnessTarget,
    #[arg(long)]
    pub event: String,
    #[arg(long)]
    pub specs_dir: PathBuf,
}

#[derive(Args, Clone)]
pub struct HookInjectGuidanceArgs {
    #[arg(long)]
    pub target: crate::harness::HarnessTarget,
    #[arg(long)]
    pub event: String,
    #[arg(long)]
    pub file: PathBuf,
}
