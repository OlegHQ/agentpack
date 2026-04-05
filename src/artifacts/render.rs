use std::path::PathBuf;

use serde_yaml::Mapping;

use super::harness::HarnessTarget;
use super::yaml::{
    ensure_trailing_newline, insert_bool, insert_globs, insert_string, merge_allowed_frontmatter,
    render_markdown,
};
use super::{ArtifactKind, MarkdownArtifact, RenderedArtifact};

impl MarkdownArtifact {
    pub fn render(&self, target: HarnessTarget) -> RenderedArtifact {
        match target.rendered_artifact_kind(self.kind) {
            ArtifactKind::Skill => self.render_skill(target),
            ArtifactKind::Command => self.render_command(target),
            ArtifactKind::Agent => self.render_agent(target),
            ArtifactKind::Rule => self.render_rule(),
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
        let mut frontmatter = Mapping::new();
        target.seed_command_frontmatter(&mut frontmatter, &self.name, &self.description);
        merge_allowed_frontmatter(
            &mut frontmatter,
            &self.extra_frontmatter,
            target.command_allowed_extra_frontmatter_keys(),
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
        target.disables_model_invocation_for_kind(self.kind)
    }

    fn skill_body(&self, target: HarnessTarget) -> String {
        if self.kind != ArtifactKind::Rule || !target.folds_cursor_rules_into_skills() {
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
        ensure_trailing_newline(&rendered).into_owned()
    }
}
