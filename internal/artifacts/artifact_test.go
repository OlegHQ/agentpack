package artifacts

import (
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/harness"
)

func TestCursorCommandRendersForOpenCodeAndCursor(t *testing.T) {
	t.Parallel()
	artifact, err := Parse(".cursor/commands/review-code.md", "# Review code\n\nCheck the diff carefully.", "")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Kind != Command || artifact.SourceVariant != CursorPlainCommand {
		t.Fatalf("artifact = %#v", artifact)
	}
	openCode := artifact.Render(harness.OpenCode)
	if openCode.RelativePath != "commands/review-code.md" || !strings.Contains(openCode.Contents, "description: Review code") {
		t.Fatalf("rendered = %#v", openCode)
	}
	cursor := artifact.Render(harness.Cursor)
	if !strings.Contains(cursor.Contents, "name: review-code") {
		t.Fatalf("cursor = %s", cursor.Contents)
	}
}

func TestRuleFallsBackToScopedSkillExceptNativeTargets(t *testing.T) {
	t.Parallel()
	artifact, err := Parse(".cursor/rules/typescript.mdc", "---\ndescription: TypeScript standards\nglobs: **/*.ts\nalwaysApply: true\n---\n\nUse strict types.\n", "")
	if err != nil {
		t.Fatal(err)
	}
	codex := artifact.Render(harness.Codex)
	if codex.RelativePath != "skills/typescript/SKILL.md" || !strings.Contains(codex.Contents, "Original Cursor globs") || !strings.Contains(codex.Contents, "Applies in every session") {
		t.Fatalf("codex = %#v", codex)
	}
	for _, target := range []harness.Target{harness.Cursor, harness.Agy} {
		rendered := artifact.Render(target)
		if rendered.RelativePath != "rules/typescript.mdc" || !strings.Contains(rendered.Contents, "alwaysApply: true") {
			t.Fatalf("%s = %#v", target, rendered)
		}
	}
}

func TestSkillStorageNameAndAllowedExtraFrontmatter(t *testing.T) {
	t.Parallel()
	artifact, err := Parse("skills/react-best-practices/SKILL.md", "---\nname: vercel-react-best-practices\ndescription: React guidance\nlicense: MIT\nunknown: drop\n---\n\n# React\n", "")
	if err != nil {
		t.Fatal(err)
	}
	rendered := artifact.Render(harness.Claude)
	if rendered.RelativePath != "skills/react-best-practices/SKILL.md" || !strings.Contains(rendered.Contents, "name: vercel-react-best-practices") || !strings.Contains(rendered.Contents, "license: MIT") || strings.Contains(rendered.Contents, "unknown:") {
		t.Fatalf("rendered = %#v", rendered)
	}
}

func TestAgyDropsClaudeOnlyFrontmatter(t *testing.T) {
	t.Parallel()
	artifact, err := Parse("agents/review.md", "---\ndescription: Review code\nmodel: opus\ncolor: blue\n---\n\nReview carefully.\n", "")
	if err != nil {
		t.Fatal(err)
	}
	rendered := artifact.Render(harness.Agy)
	if strings.Contains(rendered.Contents, "model:") || strings.Contains(rendered.Contents, "color:") || !strings.Contains(rendered.Contents, "description: Review code") {
		t.Fatalf("rendered = %s", rendered.Contents)
	}
}

func TestFrontmatterBOMCRLFGlobsAndSupportPaths(t *testing.T) {
	t.Parallel()
	artifact, err := Parse(".claude/rules/notes.md", "\ufeff---\r\ndescription: Notes\r\nglobs:\r\n  - '**/*.go'\r\n  - '**/*.mod'\r\n---\r\n\r\n# Notes", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.Globs) != 2 || artifact.Body != "\r\n# Notes\n" {
		t.Fatalf("artifact = %#v", artifact)
	}
	if got, ok := StagedSkillSupportPath("skills/demo/references/info.md", ""); !ok || got != "skills/demo/references/info.md" {
		t.Fatalf("support path = %q, %v", got, ok)
	}
	if _, ok := StagedSkillSupportPath("skills/demo/SKILL.md", ""); ok {
		t.Fatal("SKILL.md must not be support content")
	}
}

func TestCodexFoldsCommandsAndAgentsIntoSkills(t *testing.T) {
	t.Parallel()
	for _, relative := range []string{"commands/test.mdc", "agents/review.md"} {
		artifact, err := Parse(relative, "---\ndescription: Demo\n---\n\nDo it.\n", "")
		if err != nil {
			t.Fatal(err)
		}
		if rendered := artifact.Render(harness.Codex); !strings.HasPrefix(rendered.RelativePath, "skills/") {
			t.Fatalf("%s rendered to %s", relative, rendered.RelativePath)
		}
	}
}
