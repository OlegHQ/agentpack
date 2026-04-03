use std::ffi::OsStr;
use std::path::{Path, PathBuf};

use serde_yaml::{Mapping, Value};

use crate::error::{AgentpackError, Result};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum HarnessTarget {
    Claude,
    OpenCode,
    Codex,
    Cursor,
}

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
    tail_path: PathBuf,
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

impl MarkdownArtifact {
    pub fn render(&self, target: HarnessTarget) -> RenderedArtifact {
        match self.render_kind(target) {
            ArtifactKind::Skill => self.render_skill(target),
            ArtifactKind::Command => self.render_command(target),
            ArtifactKind::Agent => self.render_agent(target),
            ArtifactKind::Rule => self.render_rule(),
        }
    }

    fn render_kind(&self, target: HarnessTarget) -> ArtifactKind {
        match (self.kind, target) {
            (ArtifactKind::Skill, _) => ArtifactKind::Skill,
            (ArtifactKind::Command, HarnessTarget::Codex) => ArtifactKind::Skill,
            (ArtifactKind::Agent, HarnessTarget::Codex) => ArtifactKind::Skill,
            (ArtifactKind::Rule, HarnessTarget::Cursor) => ArtifactKind::Rule,
            (ArtifactKind::Rule, _) => ArtifactKind::Skill,
            (kind, _) => kind,
        }
    }

    fn render_skill(&self, target: HarnessTarget) -> RenderedArtifact {
        let mut frontmatter = Mapping::new();
        insert_string(&mut frontmatter, "name", &self.name);
        insert_string(&mut frontmatter, "description", &self.description);
        if self.should_disable_model_invocation(target) {
            insert_bool(&mut frontmatter, "disable-model-invocation", true);
        }
        merge_allowed_frontmatter(
            &mut frontmatter,
            &self.extra_frontmatter,
            &[
                "allowed-tools",
                "agent",
                "compatibility",
                "context",
                "disallowedTools",
                "license",
                "mcpServers",
                "metadata",
                "mode",
                "model",
                "permission",
                "subtask",
                "tools",
            ],
        );

        RenderedArtifact {
            relative_path: PathBuf::from("skills").join(&self.name).join("SKILL.md"),
            contents: render_markdown(&frontmatter, &self.skill_body(target)),
        }
    }

    fn render_command(&self, target: HarnessTarget) -> RenderedArtifact {
        let relative_path = PathBuf::from("commands").join(&self.tail_path);
        if target == HarnessTarget::Cursor {
            let mut frontmatter = Mapping::new();
            insert_string(&mut frontmatter, "name", &self.name);
            insert_string(&mut frontmatter, "description", &self.description);
            merge_allowed_frontmatter(
                &mut frontmatter,
                &self.extra_frontmatter,
                &[
                    "agent",
                    "allowed-tools",
                    "context",
                    "disable-model-invocation",
                    "model",
                    "permission",
                    "subtask",
                ],
            );
            return RenderedArtifact {
                relative_path,
                contents: render_markdown(&frontmatter, &self.body),
            };
        }

        let mut frontmatter = Mapping::new();
        insert_string(&mut frontmatter, "description", &self.description);
        if target == HarnessTarget::Claude {
            insert_string(&mut frontmatter, "name", &self.name);
        }
        merge_allowed_frontmatter(
            &mut frontmatter,
            &self.extra_frontmatter,
            &[
                "agent",
                "allowed-tools",
                "context",
                "disable-model-invocation",
                "model",
                "subtask",
            ],
        );

        RenderedArtifact {
            relative_path,
            contents: render_markdown(&frontmatter, &self.body),
        }
    }

    fn render_agent(&self, _target: HarnessTarget) -> RenderedArtifact {
        let mut frontmatter = Mapping::new();
        insert_string(&mut frontmatter, "name", &self.name);
        insert_string(&mut frontmatter, "description", &self.description);
        merge_allowed_frontmatter(
            &mut frontmatter,
            &self.extra_frontmatter,
            &[
                "color",
                "disallowedTools",
                "hidden",
                "hooks",
                "mcpServers",
                "mode",
                "model",
                "permission",
                "subtask",
                "tools",
            ],
        );

        RenderedArtifact {
            relative_path: PathBuf::from("agents").join(&self.tail_path),
            contents: render_markdown(&frontmatter, &self.body),
        }
    }

    fn render_rule(&self) -> RenderedArtifact {
        let mut frontmatter = Mapping::new();
        insert_string(&mut frontmatter, "description", &self.description);
        if !self.globs.is_empty() {
            insert_globs(&mut frontmatter, &self.globs);
        }
        if self.always_apply {
            insert_bool(&mut frontmatter, "alwaysApply", true);
        }

        RenderedArtifact {
            relative_path: PathBuf::from("rules").join(&self.tail_path),
            contents: render_markdown(&frontmatter, &self.body),
        }
    }

    fn should_disable_model_invocation(&self, target: HarnessTarget) -> bool {
        if self.disable_model_invocation {
            return true;
        }
        matches!(
            (self.kind, target),
            (ArtifactKind::Command, _)
                | (ArtifactKind::Rule, HarnessTarget::Claude)
                | (ArtifactKind::Rule, HarnessTarget::OpenCode)
                | (ArtifactKind::Rule, HarnessTarget::Codex)
        )
    }

    fn skill_body(&self, target: HarnessTarget) -> String {
        if self.kind != ArtifactKind::Rule || target == HarnessTarget::Cursor {
            return self.body.clone();
        }
        if self.globs.is_empty() && !self.always_apply {
            return self.body.clone();
        }

        let mut rendered = String::new();
        rendered.push_str("## Original rule scope\n");
        if self.always_apply {
            rendered.push_str("- Applies in every session.\n");
        }
        if !self.globs.is_empty() {
            rendered.push_str("- Original Cursor globs: ");
            rendered.push_str(
                &self
                    .globs
                    .iter()
                    .map(|glob| format!("`{glob}`"))
                    .collect::<Vec<_>>()
                    .join(", "),
            );
            rendered.push('\n');
        }
        rendered.push('\n');
        rendered.push_str(self.body.trim_start_matches('\n'));
        ensure_trailing_newline(&rendered)
    }
}

fn parse_skill_file(
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

fn parse_command_file(
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
        name: frontmatter.name.unwrap_or_else(|| name),
        description,
        body,
        disable_model_invocation: true,
        always_apply: false,
        globs: Vec::new(),
        extra_frontmatter,
        tail_path: normalized_tail(rel_path, "commands")?,
    }))
}

fn parse_agent_file(
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
        name: frontmatter.name.unwrap_or_else(|| name),
        description,
        body,
        disable_model_invocation: false,
        always_apply: false,
        globs: Vec::new(),
        extra_frontmatter,
        tail_path: normalized_tail(rel_path, "agents")?,
    }))
}

fn parse_rule_file(
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
        name: frontmatter.name.unwrap_or_else(|| name),
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

fn detect_skill_file(path: &Path) -> Option<(String, PathBuf)> {
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

fn detect_named_markdown(path: &Path, dir_name: &str, ext: &str) -> Option<PathBuf> {
    let mut comps = path.components();
    if comps.next()?.as_os_str() != OsStr::new(dir_name) {
        return None;
    }
    let remainder = comps.as_path();
    if remainder.as_os_str().is_empty() || remainder.extension() != Some(OsStr::new(ext)) {
        return None;
    }
    Some(remainder.to_path_buf())
}

fn strip_harness_prefix(path: &Path) -> &Path {
    for prefix in [".claude", ".cursor", ".opencode", ".agents"] {
        if let Ok(stripped) = path.strip_prefix(prefix) {
            return stripped;
        }
    }
    path
}

#[derive(Default)]
struct CommonFrontmatter {
    name: Option<String>,
    description: Option<String>,
    disable_model_invocation: Option<bool>,
    globs: Vec<String>,
    always_apply: Option<bool>,
}

fn split_common_frontmatter(frontmatter: Option<Mapping>) -> (CommonFrontmatter, Mapping) {
    let mut raw = frontmatter.unwrap_or_default();
    let name = remove_string(&mut raw, "name");
    let description = remove_string(&mut raw, "description");
    let disable_model_invocation = remove_bool(&mut raw, "disable-model-invocation");
    let globs = remove_globs(&mut raw, "globs");
    let always_apply = remove_bool(&mut raw, "alwaysApply");

    (
        CommonFrontmatter {
            name,
            description,
            disable_model_invocation,
            globs,
            always_apply,
        },
        raw,
    )
}

fn split_frontmatter(contents: &str) -> Result<(Option<Mapping>, String)> {
    let Some((yaml, body)) = extract_frontmatter_sections(contents) else {
        return Ok((None, ensure_trailing_newline(contents)));
    };

    let normalized_yaml = normalize_frontmatter_yaml(yaml);
    let mapping = serde_yaml::from_str::<Mapping>(&normalized_yaml)
        .map_err(|err| AgentpackError::Staging(format!("invalid YAML frontmatter: {err}")))?;
    Ok((
        Some(mapping),
        ensure_trailing_newline(body.trim_start_matches('\n')),
    ))
}

fn extract_frontmatter_sections(contents: &str) -> Option<(&str, &str)> {
    let (first, mut offset) = read_line(contents, 0)?;
    if first != "---" {
        return None;
    }
    let yaml_start = offset;
    while let Some((line, next_offset)) = read_line(contents, offset) {
        if line == "---" {
            return Some((&contents[yaml_start..offset], &contents[next_offset..]));
        }
        offset = next_offset;
    }
    None
}

fn read_line(contents: &str, start: usize) -> Option<(&str, usize)> {
    if start >= contents.len() {
        return None;
    }
    let remainder = &contents[start..];
    if let Some(pos) = remainder.find('\n') {
        let end = start + pos;
        let line = contents[start..end].trim_end_matches('\r');
        Some((line, end + 1))
    } else {
        let line = remainder.trim_end_matches('\r');
        Some((line, contents.len()))
    }
}

fn stem_name(path: &Path) -> Result<String> {
    path.file_stem()
        .map(|stem| stem.to_string_lossy().into_owned())
        .ok_or_else(|| AgentpackError::Staging(format!("missing file stem for {}", path.display())))
}

fn infer_description(body: &str, name: &str, kind: ArtifactKind) -> String {
    for line in body.lines() {
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }
        let candidate = trimmed.trim_start_matches('#').trim();
        let candidate = candidate.strip_prefix("- ").unwrap_or(candidate).trim();
        if !candidate.is_empty() {
            return truncate(candidate, 160);
        }
    }
    match kind {
        ArtifactKind::Command => format!("Run {name}"),
        ArtifactKind::Agent => format!("Use the {name} agent"),
        ArtifactKind::Rule => format!("Apply the {name} rule"),
        ArtifactKind::Skill => format!("Use the {name} skill"),
    }
}

fn truncate(value: &str, max_chars: usize) -> String {
    let mut out = String::new();
    for ch in value.chars().take(max_chars) {
        out.push(ch);
    }
    out
}

fn ensure_trailing_newline(contents: &str) -> String {
    if contents.ends_with('\n') {
        contents.to_string()
    } else {
        format!("{contents}\n")
    }
}

fn render_markdown(frontmatter: &Mapping, body: &str) -> String {
    let mut rendered = String::new();
    rendered.push_str("---\n");
    rendered.push_str(
        &serde_yaml::to_string(frontmatter)
            .expect("frontmatter serialization should not fail for scalar mappings"),
    );
    rendered.push_str("---\n\n");
    rendered.push_str(body.trim_start_matches('\n'));
    ensure_trailing_newline(&rendered)
}

fn merge_allowed_frontmatter(dst: &mut Mapping, src: &Mapping, allowed: &[&str]) {
    for key in allowed {
        let yaml_key = Value::String((*key).to_string());
        if dst.contains_key(&yaml_key) {
            continue;
        }
        if let Some(value) = src.get(&yaml_key) {
            dst.insert(yaml_key, value.clone());
        }
    }
}

fn remove_string(map: &mut Mapping, key: &str) -> Option<String> {
    let yaml_key = Value::String(key.to_string());
    match map.remove(&yaml_key) {
        Some(Value::String(value)) => Some(value),
        Some(Value::Number(value)) => Some(value.to_string()),
        Some(Value::Bool(value)) => Some(value.to_string()),
        _ => None,
    }
}

fn remove_bool(map: &mut Mapping, key: &str) -> Option<bool> {
    let yaml_key = Value::String(key.to_string());
    match map.remove(&yaml_key) {
        Some(Value::Bool(value)) => Some(value),
        _ => None,
    }
}

fn remove_globs(map: &mut Mapping, key: &str) -> Vec<String> {
    let yaml_key = Value::String(key.to_string());
    match map.remove(&yaml_key) {
        Some(Value::String(value)) => vec![value],
        Some(Value::Sequence(values)) => values
            .into_iter()
            .filter_map(|value| match value {
                Value::String(item) => Some(item),
                _ => None,
            })
            .collect(),
        _ => Vec::new(),
    }
}

fn insert_string(map: &mut Mapping, key: &str, value: &str) {
    map.insert(
        Value::String(key.to_string()),
        Value::String(value.to_string()),
    );
}

fn insert_bool(map: &mut Mapping, key: &str, value: bool) {
    map.insert(Value::String(key.to_string()), Value::Bool(value));
}

fn insert_globs(map: &mut Mapping, globs: &[String]) {
    let key = Value::String("globs".to_string());
    if globs.len() == 1 {
        map.insert(key, Value::String(globs[0].clone()));
        return;
    }
    map.insert(
        key,
        Value::Sequence(
            globs
                .iter()
                .map(|glob| Value::String(glob.clone()))
                .collect(),
        ),
    );
}

fn normalize_frontmatter_yaml(yaml: &str) -> String {
    let mut out = String::new();
    for line in yaml.lines() {
        let trimmed = line.trim();
        if let Some(value) = trimmed.strip_prefix("globs:") {
            let value = value.trim();
            if !value.is_empty()
                && !value.starts_with('"')
                && !value.starts_with('\'')
                && !value.starts_with('[')
            {
                out.push_str("globs: \"");
                out.push_str(&value.replace('"', "\\\""));
                out.push_str("\"\n");
                continue;
            }
        }
        out.push_str(line);
        out.push('\n');
    }
    out
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
