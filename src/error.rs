use std::io;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum AgentpackError {
    #[error("Failed to parse URL: {0}")]
    UrlParse(String),

    #[error("Github API error: {0}")]
    GitHubApi(String),

    #[error("Cache error: {0}")]
    Cache(String),

    #[error("Database error: {0}")]
    Database(String),

    #[error("Install error: {0}")]
    Install(String),

    #[error(transparent)]
    Io(#[from] io::Error),

    #[error(transparent)]
    Http(#[from] reqwest::Error),
}

pub type Result<T> = std::result::Result<T, AgentpackError>;
