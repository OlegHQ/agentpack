pub mod harness;
mod parse;
mod render;
pub(crate) mod yaml;

use std::ffi::OsStr;
use std::path::{Path, PathBuf};

use serde_yaml::Mapping;

use crate::error::Result;

pub use harness::HarnessTarget;
use parse::{
    detect_named_markdown, detect_skill_file, parse_agent_file, parse_command_file,
    parse_rule_file, parse_skill_file, strip_harness_prefix,
};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ArtifactKind {
    Skill,
    Command,
    Agent,
    Rule,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SourceVariant {
    SkillFrontmatter,
    CommandFrontmatter,
    CursorPlainCommand,
    AgentFrontmatter,
    CursorRule,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RenderedArtifact {
    pub relative_path: PathBuf,
    pub contents: String,
}

#[derive(Clone, Debug)]
pub struct MarkdownArtifact {
    pub kind: ArtifactKind,
    pub source_variant: SourceVariant,
    pub name: String,
    pub description: String,
    pub body: String,
    pub disable_model_invocation: bool,
    pub always_apply: bool,
    pub globs: Vec<String>,
    pub extra_frontmatter: Mapping,
    pub(crate) tail_path: PathBuf,
}

pub fn parse_markdown_artifact(
    rel_path: &Path,
    contents: &str,
    bare_skill_name: Option<&str>,
) -> Result<Option<MarkdownArtifact>> {
    if let Some(skill_name) = bare_skill_name {
        if rel_path == Path::new("SKILL.md") {
            return parse_skill_file(skill_name, contents, PathBuf::from("SKILL.md"));
        }
        return Ok(None);
    }

    let stripped = strip_harness_prefix(rel_path);
    if let Some((name, tail_path)) = detect_skill_file(stripped) {
        return parse_skill_file(&name, contents, tail_path);
    }
    if let Some(tail_path) = detect_named_markdown(stripped, "commands", "md") {
        return parse_command_file(stripped, contents, tail_path);
    }
    if let Some(tail_path) = detect_named_markdown(stripped, "agents", "md") {
        return parse_agent_file(stripped, contents, tail_path);
    }
    if let Some(tail_path) = detect_named_markdown(stripped, "rules", "mdc") {
        return parse_rule_file(stripped, contents, tail_path);
    }

    Ok(None)
}

pub fn staged_skill_support_path(
    rel_path: &Path,
    bare_skill_name: Option<&str>,
) -> Option<PathBuf> {
    if let Some(skill_name) = bare_skill_name {
        if rel_path == Path::new("SKILL.md") {
            return None;
        }
        return Some(PathBuf::from("skills").join(skill_name).join(rel_path));
    }

    let stripped = strip_harness_prefix(rel_path);
    let mut comps = stripped.components();
    let first = comps.next()?.as_os_str();
    if first != OsStr::new("skills") {
        return None;
    }
    let skill_name = comps.next()?.as_os_str();
    let remainder = comps.as_path();
    if remainder.as_os_str().is_empty() || remainder == Path::new("SKILL.md") {
        return None;
    }

    Some(PathBuf::from("skills").join(skill_name).join(remainder))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_cursor_command_and_renders_opencode_frontmatter() {
        let artifact = parse_markdown_artifact(
            Path::new(".cursor/commands/review-code.md"),
            "# Review code\n\nCheck the diff carefully.",
            None,
        )
        .unwrap()
        .unwrap();

        assert_eq!(artifact.kind, ArtifactKind::Command);
        assert_eq!(artifact.source_variant, SourceVariant::CursorPlainCommand);

        let rendered = artifact.render(HarnessTarget::OpenCode);
        assert_eq!(
            rendered.relative_path,
            PathBuf::from("commands/review-code.md")
        );
        assert!(rendered.contents.contains("description: Review code"));
        assert!(rendered.contents.contains("Check the diff carefully."));
    }

    #[test]
    fn renders_cursor_plugin_command_with_name_and_description_frontmatter() {
        let artifact = parse_markdown_artifact(
            Path::new(".opencode/commands/test.md"),
            "---\ndescription: Run tests\nagent: build\n---\n\nRun the full suite.\n",
            None,
        )
        .unwrap()
        .unwrap();

        let rendered = artifact.render(HarnessTarget::Cursor);
        assert_eq!(rendered.relative_path, PathBuf::from("commands/test.md"));
        assert!(rendered.contents.contains("name: test"));
        assert!(rendered.contents.contains("description: Run tests"));
        assert!(rendered.contents.contains("Run the full suite."));
    }

    #[test]
    fn falls_back_to_skill_for_non_cursor_rule_targets() {
        let artifact = parse_markdown_artifact(
            Path::new(".cursor/rules/typescript.mdc"),
            "---\ndescription: TypeScript standards\nglobs: **/*.ts\nalwaysApply: false\n---\n\n# Rule\n\nUse strict types.\n",
            None,
        )
        .unwrap()
        .unwrap();

        let rendered = artifact.render(HarnessTarget::Codex);
        assert_eq!(
            rendered.relative_path,
            PathBuf::from("skills/typescript/SKILL.md")
        );
        assert!(rendered.contents.contains("name: typescript"));
        assert!(rendered.contents.contains("Original Cursor globs"));
    }
}
