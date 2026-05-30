use std::fs;
use std::path::{Path, PathBuf};

use super::{require, Harness, HarnessTarget, StageCtx};
use crate::error::{AgentpackError, Result};
use crate::paths::staging_opencode_dir_for_mode;
use crate::staging::{force_opencode_attribution_off, seed_opencode_root};

/// OpenCode: launched with a redirected `OPENCODE_CONFIG_DIR`.
pub(super) struct OpenCode;

impl Harness for OpenCode {
    fn id(&self) -> HarnessTarget {
        HarnessTarget::OpenCode
    }

    fn staged_root(&self, project_root: &Path, mode: &str) -> Result<PathBuf> {
        staging_opencode_dir_for_mode(project_root, mode)
    }

    fn prepare(&self, ctx: &StageCtx) -> Result<()> {
        let root = self.staged_root(ctx.project_root, ctx.mode.name())?;
        fs::create_dir_all(&root).map_err(|e| AgentpackError::io(&root, e))?;
        seed_opencode_root(&root)?;
        force_opencode_attribution_off(&root)
    }

    fn verify(&self, ctx: &StageCtx) -> Result<()> {
        let root = staging_opencode_dir_for_mode(ctx.project_root, ctx.mode.name())?;
        require(root.is_dir(), || {
            format!("opencode staging missing {}", root.display())
        })
    }
}
