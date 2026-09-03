package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/cache"
	"github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/mode"
)

func TestCollectOrdersLayersAndShadowsSkills(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	seed := filepath.Join(project, "codex", "hooks.json")
	writeHookFile(t, seed, `{"Stop":[{"type":"command","command":"seed"}]}`)
	plugin := lockfile.Package{Module: "github.com/acme/repo/plugin", Kind: lockfile.PackagePlugin, Owner: "acme", Repo: "repo", Path: "plugin", Commit: strings.Repeat("a", 40), CacheKey: "plugin-key"}
	skill := lockfile.Package{Module: "github.com/acme/repo/plugin/skill", Kind: lockfile.PackageSkill, Owner: "acme", Repo: "repo", Path: "plugin/skill", Commit: plugin.Commit, CacheKey: "skill-key"}
	pluginRoot, _ := cache.EntryDir(plugin.CacheKey)
	writeHookFile(t, filepath.Join(pluginRoot, "hooks", "hooks.json"), nestedCommand("plugin"))
	skillRoot, _ := cache.EntryDir(skill.CacheKey)
	writeHookFile(t, filepath.Join(skillRoot, "hooks", "hooks.json"), nestedCommand("shadowed"))
	writeHookFile(t, filepath.Join(project, ".agents", "hooks", "hooks.json"), nestedCommand("dot"))
	bundle, err := Collect(project, lockfile.PackLock{Packages: []lockfile.Package{skill, plugin}}, seed, mode.ImplicitEffective())
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Hooks) != 3 {
		t.Fatalf("hooks = %#v", bundle.Hooks)
	}
	want := []Layer{SeededNative, PackPlugin, DotAgents}
	for index, layer := range want {
		if bundle.Hooks[index].Origin.Layer != layer {
			t.Fatalf("layers = %#v", bundle.Hooks)
		}
	}
}

func TestCollectHonorsSpecificModeSelectors(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	pkg := lockfile.Package{Module: "github.com/acme/repo", Kind: lockfile.PackagePlugin, CacheKey: "key"}
	root, _ := cache.EntryDir(pkg.CacheKey)
	writeHookFile(t, filepath.Join(root, "hooks", "hooks.json"), nestedCommand("run"))
	effective, err := mode.NewEffective("minimal", mode.Definition{Base: mode.BaseNone, Enable: []string{"package-path:github.com/acme/repo:hooks/hooks.json"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Collect(project, lockfile.PackLock{Packages: []lockfile.Package{pkg}}, "", effective)
	if err != nil || len(bundle.Hooks) != 1 {
		t.Fatalf("bundle = %#v, %v", bundle, err)
	}
}

func TestStageOriginPackagesFiltersAssetsWithoutFollowingSymlinks(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	writeHookFile(t, filepath.Join(source, "hooks", "hooks.json"), nestedCommand("run"))
	writeHookFile(t, filepath.Join(source, "scripts", "run.sh"), "echo run")
	if err := os.Symlink(filepath.Join(source, "scripts", "run.sh"), filepath.Join(source, "link.sh")); err != nil {
		t.Fatal(err)
	}
	effective, err := mode.NewEffective("minimal", mode.Definition{Base: mode.BaseNone, Enable: []string{"package-path:github.com/acme/repo:hooks/hooks.json"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	origin := Origin{Layer: PackPlugin, Module: "github.com/acme/repo", PackageKey: "pkg", SourceRoot: source, SourceRelative: "hooks/hooks.json"}
	bundle := Bundle{Hooks: []Hook{{Origin: origin}}}
	roots, err := StageOriginPackages(bundle, harness.Cursor, target, effective)
	if err != nil {
		t.Fatal(err)
	}
	root := roots["pkg"]
	if _, err := os.Stat(filepath.Join(root, "hooks", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "run.sh")); !os.IsNotExist(err) {
		t.Fatalf("script should be filtered: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "link.sh")); !os.IsNotExist(err) {
		t.Fatalf("symlink should not be copied: %v", err)
	}
}

func nestedCommand(command string) string {
	return `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + command + `"}]}]}}`
}
func writeHookFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
