use url::Url;

use crate::error::{AgentpackError, Result};

pub struct GitHubSkillUrl {
    pub owner: String,
    pub repo: String,
    pub branch: String,
    pub skill_path: String,
    pub skill_name: String,
}

pub fn parse(raw_url: &str) -> Result<GitHubSkillUrl> {
    let parsed = Url::parse(raw_url).map_err(|e| AgentpackError::UrlParse(e.to_string()))?;
    let host = parsed
        .host_str()
        .ok_or_else(|| AgentpackError::UrlParse("no host in uRL".into()))?;

    Ok(GitHubSkillUrl {
        owner: todo!(),
        repo: todo!(),
        branch: todo!(),
        skill_path: todo!(),
        skill_name: todo!(),
    })
}
