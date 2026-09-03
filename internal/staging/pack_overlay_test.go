package staging

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/mode"
)

func TestSkillIsShadowedByContainingPlugin(t *testing.T) {
	skill := lockfile.Package{Owner: "o", Repo: "r", Commit: "c", Path: "plugin/skills/x"}
	plugin := lockfile.Package{Kind: lockfile.PackagePlugin, Owner: "o", Repo: "r", Commit: "c", Path: "plugin", CacheKey: "key"}
	if !SkillIsShadowed(skill, []lockfile.Package{plugin}) {
		t.Fatal("skill should be shadowed")
	}
	plugin.Commit = "other"
	if SkillIsShadowed(skill, []lockfile.Package{plugin}) {
		t.Fatal("different commit shadowed")
	}
}

func TestStageSourceTreeRendersOncePerHarnessAndCopiesSupport(t *testing.T) {
	source, claudeRoot, codexRoot := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "references", "data.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	effective := mode.ImplicitEffective()
	roots := []HarnessRoot{{harness.Claude, claudeRoot}, {harness.Codex, codexRoot}}
	if err := stageSourceTree(source, roots, "demo", "github.com/o/r/demo", effective); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{claudeRoot, codexRoot} {
		for _, relative := range []string{"skills/demo/SKILL.md", "skills/demo/references/data.json"} {
			if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
				t.Fatalf("%s: %v", root, err)
			}
		}
	}
}
