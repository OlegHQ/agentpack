package cache

import (
	"os"
	"path/filepath"
	"testing"

	githubsource "github.com/OlegHQ/agentpack/internal/github"
	"github.com/OlegHQ/agentpack/internal/lockfile"
)

func TestClassifyMaterializedDistinguishesSkillAndPlugin(t *testing.T) {
	t.Parallel()
	source := githubsource.Source{Owner: "Owner", Repo: "Repo", Path: "packs/demo"}
	skillRoot := t.TempDir()
	writeTestFile(t, filepath.Join(skillRoot, "SKILL.md"), "# Demo")
	skill, err := ClassifyMaterialized(skillRoot, "https://example.test/skill", source, "commit", "key")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Kind != lockfile.PackageSkill || skill.Module != "github.com/owner/repo/packs/demo" || skill.Direct {
		t.Fatalf("skill package = %#v", skill)
	}

	pluginRoot := t.TempDir()
	writeTestFile(t, filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), `{"name":"demo"}`)
	plugin, err := ClassifyMaterialized(pluginRoot, "https://example.test/plugin", source, "commit", "key")
	if err != nil {
		t.Fatal(err)
	}
	if plugin.Kind != lockfile.PackagePlugin || !HasPluginManifest(pluginRoot) {
		t.Fatalf("plugin package = %#v", plugin)
	}
}

func TestClassifyMaterializedRejectsInvalidTree(t *testing.T) {
	t.Parallel()
	if _, err := ClassifyMaterialized(t.TempDir(), "url", githubsource.Source{}, "commit", "key"); err == nil {
		t.Fatal("expected invalid layout error")
	}
}

func TestDependencyKey(t *testing.T) {
	t.Parallel()
	if got := DependencyKey("explicit", "x", "y", "z"); got != "explicit" {
		t.Fatalf("explicit key = %q", got)
	}
	if got := DependencyKey("", "path", "local-name", ""); got != "local-name" {
		t.Fatalf("path key = %q", got)
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
