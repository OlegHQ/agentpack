use std::path::PathBuf;

use thiserror::Error;

#[derive(Error, Debug)]
pub enum AgentpackError {
    #[error("no pack.lock found searching upward from {0}")]
    NoPackLock(PathBuf),

    #[error("pack.lock already exists at {0}")]
    PackLockExists(PathBuf),

    #[error("agentpack.toml already exists at {0}")]
    ManifestExists(PathBuf),

    #[error("agentpack.toml required but missing at {0}")]
    ManifestMissing(PathBuf),

    #[error("no direct dependency matching {0} in agentpack.toml [dependencies]")]
    DependencyNotFound(String),

    #[error("I/O error at {path}: {source}")]
    Io {
        path: PathBuf,
        #[source]
        source: std::io::Error,
    },

    #[error("failed to parse pack.lock: {0}")]
    LockfileParse(String),

    #[error("GitHub URL error: {0}")]
    GitHubUrl(String),

    #[error("GitHub API error: {0}")]
    GitHubApi(String),

    #[error("download/archive error: {0}")]
    Archive(String),

    #[error("cache error: {0}")]
    Cache(String),

    #[error("database error: {0}")]
    Database(String),

    #[error("sync/staging error: {0}")]
    Staging(String),

    #[error("skill at {0} has no SKILL.md")]
    MissingSkillMd(PathBuf),

    #[error("plugin at {0} has no .claude-plugin/plugin.json")]
    MissingPluginManifest(PathBuf),

    #[error(
        "cache at {0} is not a package root (expected SKILL.md, agentpack.toml, or .claude-plugin / .cursor-plugin)"
    )]
    InvalidCacheLayout(PathBuf),
}

impl AgentpackError {
    pub fn io(path: impl Into<PathBuf>, source: std::io::Error) -> Self {
        Self::Io {
            path: path.into(),
            source,
        }
    }
}

pub type Result<T> = std::result::Result<T, AgentpackError>;
