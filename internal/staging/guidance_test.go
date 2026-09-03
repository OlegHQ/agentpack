package staging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	base "github.com/OlegHQ/agentpack/internal/harness"
)

func TestWriteGuidancePreservesUserContentAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(path, []byte("# User\n\nKeep me.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := base.WriteGuidance(path, "first"); err != nil {
		t.Fatal(err)
	}
	if err := base.WriteGuidance(path, "second"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	output := string(data)
	if !strings.Contains(output, "Keep me.") || !strings.Contains(output, "second") || strings.Contains(output, "first") {
		t.Fatalf("output = %q", output)
	}
	if strings.Count(output, "<!-- agentpack:guidance:begin -->") != 1 {
		t.Fatalf("markers = %q", output)
	}
}

func TestCollectRulesSelectsAlwaysApply(t *testing.T) {
	root := t.TempDir()
	rules := filepath.Join(root, "rules")
	if err := os.Mkdir(rules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rules, "always.mdc"), []byte("---\nalwaysApply: true\ndescription: Stay focused\n---\nDo it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rules, "conditional.mdc"), []byte("---\nalwaysApply: false\n---\nSkip.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := collectRules(root, func(string) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "always" {
		t.Fatalf("rules = %#v", got)
	}
}
