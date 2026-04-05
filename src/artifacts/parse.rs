use std::ffi::OsStr;
use std::path::{Path, PathBuf};

use crate::error::{AgentpackError, Result};

use super::yaml::{infer_description, split_common_frontmatter, split_frontmatter};
use super::{ArtifactKind, MarkdownArtifact, SourceVariant};

pub(super) fn parse_skill_file(
    name: &str,
    contents: &str,
    tail_path: PathBuf,
) -> Result<Option<MarkdownArtifact>> {
    let (frontmatter, body) = split_frontmatter(contents)?;
    let (frontmatter, extra_frontmatter) = split_common_frontmatter(frontmatter);
    let description = frontmatter
        .description
        .unwrap_or_else(|| infer_description(&body, name, ArtifactKind::Skill));

    Ok(Some(MarkdownArtifact {
        kind: ArtifactKind::Skill,
        source_variant: SourceVariant::SkillFrontmatter,
        name: frontmatter.name.unwrap_or_else(|| name.to_string()),
        description,
        body,
        disable_model_invocation: frontmatter.disable_model_invocation.unwrap_or(false),
        always_apply: false,
        globs: Vec::new(),
        extra_frontmatter,
        tail_path,
    }))
}

pub(super) fn parse_command_file(
    rel_path: &Path,
    contents: &str,
    tail_path: PathBuf,
) -> Result<Option<MarkdownArtifact>> {
    let name = stem_name(&tail_path)?;
    let (frontmatter, body) = split_frontmatter(contents)?;
    let source_variant = if frontmatter.is_some() {
        SourceVariant::CommandFrontmatter
    } else {
        SourceVariant::CursorPlainCommand
    };
    let (frontmatter, extra_frontmatter) = split_common_frontmatter(frontmatter);
    let description = frontmatter
        .description
        .unwrap_or_else(|| infer_description(&body, &name, ArtifactKind::Command));

    Ok(Some(MarkdownArtifact {
        kind: ArtifactKind::Command,
        source_variant,
        name: frontmatter.name.unwrap_or(name),
        description,
        body,
        disable_model_invocation: true,
        always_apply: false,
        globs: Vec::new(),
        extra_frontmatter,
        tail_path: normalized_tail(rel_path, "commands")?,
    }))
}

pub(super) fn parse_agent_file(
    rel_path: &Path,
    contents: &str,
    tail_path: PathBuf,
) -> Result<Option<MarkdownArtifact>> {
    let name = stem_name(&tail_path)?;
    let (frontmatter, body) = split_frontmatter(contents)?;
    let (frontmatter, extra_frontmatter) = split_common_frontmatter(frontmatter);
    let description = frontmatter
        .description
        .unwrap_or_else(|| infer_description(&body, &name, ArtifactKind::Agent));

    Ok(Some(MarkdownArtifact {
        kind: ArtifactKind::Agent,
        source_variant: SourceVariant::AgentFrontmatter,
        name: frontmatter.name.unwrap_or(name),
        description,
        body,
        disable_model_invocation: false,
        always_apply: false,
        globs: Vec::new(),
        extra_frontmatter,
        tail_path: normalized_tail(rel_path, "agents")?,
    }))
}

pub(super) fn parse_rule_file(
    rel_path: &Path,
    contents: &str,
    tail_path: PathBuf,
) -> Result<Option<MarkdownArtifact>> {
    let name = stem_name(&tail_path)?;
    let (frontmatter, body) = split_frontmatter(contents)?;
    let (frontmatter, extra_frontmatter) = split_common_frontmatter(frontmatter);
    let description = frontmatter
        .description
        .unwrap_or_else(|| infer_description(&body, &name, ArtifactKind::Rule));

    Ok(Some(MarkdownArtifact {
        kind: ArtifactKind::Rule,
        source_variant: SourceVariant::CursorRule,
        name: frontmatter.name.unwrap_or(name),
        description,
        body,
        disable_model_invocation: false,
        always_apply: frontmatter.always_apply.unwrap_or(false),
        globs: frontmatter.globs,
        extra_frontmatter,
        tail_path: normalized_tail(rel_path, "rules")?,
    }))
}

fn normalized_tail(rel_path: &Path, dir_name: &str) -> Result<PathBuf> {
    let stripped = strip_harness_prefix(rel_path);
    stripped
        .strip_prefix(dir_name)
        .map(|path| path.to_path_buf())
        .map_err(|_| {
            AgentpackError::Staging(format!(
                "unexpected artifact path outside {dir_name}: {}",
                rel_path.display()
            ))
        })
}

pub(super) fn detect_skill_file(path: &Path) -> Option<(String, PathBuf)> {
    let mut comps = path.components();
    if comps.next()?.as_os_str() != OsStr::new("skills") {
        return None;
    }
    let name = comps.next()?.as_os_str().to_string_lossy().into_owned();
    let remainder = comps.as_path();
    if remainder != Path::new("SKILL.md") {
        return None;
    }
    Some((name, PathBuf::from("SKILL.md")))
}

/// Detects **`dir_name/<path>.{md,mdc}`** under a package (after harness prefix strip). Extensions are case-insensitive.
pub(super) fn detect_named_markdown_extensions(
    path: &Path,
    dir_name: &str,
    exts: &[&str],
) -> Option<PathBuf> {
    let mut comps = path.components();
    if comps.next()?.as_os_str() != OsStr::new(dir_name) {
        return None;
    }
    let remainder = comps.as_path();
    if remainder.as_os_str().is_empty() {
        return None;
    }
    let got = remainder.extension()?.to_str()?;
    if !exts
        .iter()
        .any(|e| got.eq_ignore_ascii_case(e.trim_start_matches('.')))
    {
        return None;
    }
    Some(remainder.to_path_buf())
}

pub(super) fn strip_harness_prefix(path: &Path) -> &Path {
    for prefix in [".claude", ".cursor", ".opencode", ".agents"] {
        if let Ok(stripped) = path.strip_prefix(prefix) {
            return stripped;
        }
    }
    path
}

fn stem_name(path: &Path) -> Result<String> {
    path.file_stem()
        .map(|stem| stem.to_string_lossy().into_owned())
        .ok_or_else(|| AgentpackError::Staging(format!("missing file stem for {}", path.display())))
}
