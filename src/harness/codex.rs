use std::path::{Path, PathBuf};

use super::{require, Harness, HarnessTarget, StageCtx};
use crate::artifacts::ArtifactKind;
use crate::error::Result;
use crate::paths::staging_codex_home_dir_for_mode;

/// Codex: launched with a redirected `CODEX_HOME`; pack content rendered as portable skills.
pub(super) struct Codex;

impl Harness for Codex {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::Codex
    }

    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf> {
        staging_codex_home_dir_for_mode(project_root, mode)
    }

    fn rendered_artifact_kind(&self, _source: ArtifactKind) -> ArtifactKind {
        // Codex only has a skills surface: commands, agents, and rules all fold into skills.
        ArtifactKind::Skill
    }

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let root = staging_codex_home_dir_for_mode(ctx.project_root, ctx.mode.name())?;
        require(root.is_dir(), || {
            format!("codex home staging missing {}", root.display())
        })
    }
}
