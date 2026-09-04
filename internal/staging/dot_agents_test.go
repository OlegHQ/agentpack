package staging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func TestStageDotAgentsLeavesNativeCodexSkillsInProject(t *testing.T) {
	project, stage := t.TempDir(), t.TempDir()
	t.Setenv("AGENTPACK_STAGING_ROOT", stage)
	dot := filepath.Join(project, ".agents")
	files := map[string]string{"skills/local/SKILL.md": "---\nname: local\ndescription: Local skill\n---\n", "commands/review.md": "---\ndescription: Review code\n---\nReview it.\n", "rules/team.mdc": "---\nalwaysApply: true\n---\nTeam rule.\n", "claude/settings.json": "{}", "codex/theme.toml": "x = 1", "AGENTS.md": "project agents", "CLAUDE.md": "project claude"}
	for relative, contents := range files {
		path := filepath.Join(dot, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := StageDotAgents(project, "default", mode.ImplicitEffective()); err != nil {
		t.Fatal(err)
	}
	plugins, _ := paths.StagingPluginsDirForMode(project, "default")
	bundle := filepath.Join(plugins, paths.StagedAgentpackBundleName)
	codex, _ := paths.StagingCodexHomeDirForMode(project, "default")
	for _, path := range []string{filepath.Join(bundle, "settings.json"), filepath.Join(bundle, "commands/review.md"), filepath.Join(bundle, "skills/local/SKILL.md"), filepath.Join(bundle, "rules/dot-agents--team.mdc"), filepath.Join(bundle, "CLAUDE.md"), filepath.Join(codex, "theme.toml"), filepath.Join(codex, "skills/review/SKILL.md"), filepath.Join(codex, "AGENTS.md")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(codex, "skills/local/SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("project-local skill copied into Codex home: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(codex, "skills/review/SKILL.md"))
	if !strings.Contains(string(data), "disable-model-invocation: true") {
		t.Fatalf("codex command fallback = %s", data)
	}
}
