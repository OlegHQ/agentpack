package staging

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveCollisionsRemovesAllStagedCopies(t *testing.T) {
	root := t.TempDir()
	home, bundle, other := filepath.Join(root, "home"), filepath.Join(root, "bundle"), filepath.Join(root, "other")
	for _, path := range []string{filepath.Join(home, ".claude/skills/Code-Tutor/SKILL.md"), filepath.Join(home, ".grok/commands/deploy.md"), filepath.Join(bundle, "skills/code-tutor/SKILL.md"), filepath.Join(other, "skills/CODE-TUTOR/SKILL.md"), filepath.Join(bundle, "commands/deploy.md"), filepath.Join(other, "commands/nested/Deploy.md")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := ResolveCollisionsWithHome(bundle, []string{bundle, other}, []string{bundle, other}, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := removed.SkillSlugs["code-tutor"]; !exists {
		t.Fatalf("removed = %#v", removed)
	}
	for _, path := range []string{filepath.Join(bundle, "skills/code-tutor"), filepath.Join(other, "skills/CODE-TUTOR"), filepath.Join(bundle, "commands/deploy.md"), filepath.Join(other, "commands/nested/Deploy.md")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("still exists: %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/skills/Code-Tutor/SKILL.md")); err != nil {
		t.Fatal("user copy removed")
	}
}
