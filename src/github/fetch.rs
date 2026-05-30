//! Shared REST → git protocol → stale-cache orchestration for GitHub metadata fetches.

use crate::error::{AgentpackError, Result};

/// Run git protocol after REST failure. Logs and returns `Ok` on success, `Err` with the REST
/// error when git also fails.
pub(crate) fn try_git_protocol<T>(
    owner: &str,
    repo: &str,
    context: &str,
    rest_error: AgentpackError,
    git: impl FnOnce() -> Result<T>,
) -> std::result::Result<T, AgentpackError> {
    tracing::warn!(
        owner,
        repo,
        context,
        error = %rest_error,
        "GitHub REST request failed; trying git protocol fallback"
    );
    match git() {
        Ok(value) => Ok(value),
        Err(gix_error) => {
            tracing::warn!(
                owner,
                repo,
                context,
                error = %gix_error,
                "Git protocol fallback failed; considering stale cache"
            );
            Err(rest_error)
        }
    }
}
