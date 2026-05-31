use std::borrow::Cow;

use serde_norway::{Mapping, Value};

use crate::error::{AgentpackError, Result};

use super::ArtifactKind;

pub(super) fn split_frontmatter(contents: &str) -> Result<(Option<Mapping>, String)> {
    // Strip a leading UTF-8 BOM so a `---` frontmatter delimiter saved with a BOM still parses
    // (otherwise the whole file is treated as body and declared name/description are dropped).
    let contents = contents.strip_prefix('\u{feff}').unwrap_or(contents);
    let Some((yaml, body)) = extract_frontmatter_sections(contents) else {
        return Ok((None, ensure_trailing_newline(contents).into_owned()));
    };

    let normalized_yaml = normalize_frontmatter_yaml(yaml);
    let mapping = serde_norway::from_str::<Mapping>(&normalized_yaml)
        .map_err(|err| AgentpackError::Staging(format!("invalid YAML frontmatter: {err}")))?;
    Ok((
        Some(mapping),
        ensure_trailing_newline(body.trim_start_matches('\n')).into_owned(),
    ))
}

fn extract_frontmatter_sections(contents: &str) -> Option<(&str, &str)> {
    // First line must be exactly `---`
    let after_open = contents
        .strip_prefix("---\n")
        .or_else(|| contents.strip_prefix("---\r\n"))?;
    // Scan lines for the closing `---`
    let mut offset = 0;
    for line in after_open.split('\n') {
        let trimmed = line.trim_end_matches('\r');
        if trimmed == "---" {
            let yaml = &after_open[..offset];
            let body_start = offset + line.len() + 1; // +1 for '\n'
            let body = after_open.get(body_start..).unwrap_or("");
            return Some((yaml, body));
        }
        offset += line.len() + 1; // +1 for '\n'
    }
    None
}

pub(super) fn infer_description(body: &str, name: &str, kind: ArtifactKind) -> String {
    for line in body.lines() {
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }
        let candidate = trimmed.trim_start_matches('#').trim();
        let candidate = candidate.strip_prefix("- ").unwrap_or(candidate).trim();
        if !candidate.is_empty() {
            return crate::fs_util::truncate_str(candidate, 160);
        }
    }
    match kind {
        ArtifactKind::Command => format!("Run {name}"),
        ArtifactKind::Agent => format!("Use the {name} agent"),
        ArtifactKind::Rule => format!("Apply the {name} rule"),
        ArtifactKind::Skill => format!("Use the {name} skill"),
    }
}

pub(super) fn ensure_trailing_newline(contents: &str) -> Cow<'_, str> {
    if contents.ends_with('\n') {
        Cow::Borrowed(contents)
    } else {
        Cow::Owned(format!("{contents}\n"))
    }
}

pub(super) fn render_markdown(frontmatter: &Mapping, body: &str) -> String {
    let yaml = serde_norway::to_string(frontmatter)
        .expect("frontmatter serialization should not fail for scalar mappings");
    let body = body.trim_start_matches('\n');
    let mut rendered = String::with_capacity(6 + yaml.len() + 5 + body.len() + 1);
    rendered.push_str("---\n");
    rendered.push_str(&yaml);
    rendered.push_str("---\n\n");
    rendered.push_str(body);
    if !rendered.ends_with('\n') {
        rendered.push('\n');
    }
    rendered
}

pub(super) fn merge_allowed_frontmatter(dst: &mut Mapping, src: &Mapping, allowed: &[&str]) {
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

pub(super) fn remove_string(map: &mut Mapping, key: &str) -> Option<String> {
    let yaml_key = Value::String(key.to_string());
    match map.remove(&yaml_key) {
        Some(Value::String(value)) => Some(value),
        Some(Value::Number(value)) => Some(value.to_string()),
        Some(Value::Bool(value)) => Some(value.to_string()),
        _ => None,
    }
}

pub(super) fn remove_bool(map: &mut Mapping, key: &str) -> Option<bool> {
    let yaml_key = Value::String(key.to_string());
    match map.remove(&yaml_key) {
        Some(Value::Bool(value)) => Some(value),
        _ => None,
    }
}

pub(super) fn remove_globs(map: &mut Mapping, key: &str) -> Vec<String> {
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

pub(crate) fn insert_string(map: &mut Mapping, key: &str, value: &str) {
    map.insert(
        Value::String(key.to_string()),
        Value::String(value.to_string()),
    );
}

pub(super) fn insert_bool(map: &mut Mapping, key: &str, value: bool) {
    map.insert(Value::String(key.to_string()), Value::Bool(value));
}

pub(super) fn insert_globs(map: &mut Mapping, globs: &[String]) {
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

#[derive(Default)]
pub(super) struct CommonFrontmatter {
    pub name: Option<String>,
    pub description: Option<String>,
    pub disable_model_invocation: Option<bool>,
    pub globs: Vec<String>,
    pub always_apply: Option<bool>,
}

pub(super) fn split_common_frontmatter(
    frontmatter: Option<Mapping>,
) -> (CommonFrontmatter, Mapping) {
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
