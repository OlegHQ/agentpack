package staging

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for relative, body := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &output
}

// alirezarezvani/claude-skills declares "skills": "./.codex/skills/" in
// .codex-plugin/plugin.json, and that directory holds links to the skills.
func TestStageSourceTreeFollowsDeclaredComponentPathsInsideThePackage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	source, outside, claudeRoot, codexRoot := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	writeTree(t, outside, map[string]string{"leak/SKILL.md": "---\nname: leak\ndescription: Outside\n---\nSecret\n"})
	writeTree(t, source, map[string]string{
		".codex-plugin/plugin.json":                `{"name":"skills","skills":"./.codex/skills/"}`,
		".claude-plugin/plugin.json":               `{"name":"skills","commands":["./extra/commands/ship.md"],"agents":"./missing/"}`,
		"engineering/skills/devops/SKILL.md":       "---\nname: devops\ndescription: DevOps\n---\nDeploy things.\n",
		"engineering/skills/devops/scripts/run.py": "print('run')\n",
		"marketing/skills/seo/SKILL.md":            "---\nname: seo\ndescription: SEO\n---\nRank things.\n",
		"extra/commands/ship.md":                   "---\ndescription: Ship it\n---\nShip.\n",
		"skills/local/SKILL.md":                    "---\nname: local\ndescription: Local\n---\nLocal.\n",
	})
	links := map[string]string{
		".codex/skills/devops": "../../engineering/skills/devops",
		".codex/skills/seo":    "../../marketing/skills/seo",
		".codex/skills/local":  "../../skills/local",
		".codex/skills/leak":   filepath.Join(outside, "leak"),
		"skills/linked":        "../marketing/skills/seo",
	}
	for link, target := range links {
		path := filepath.Join(source, filepath.FromSlash(link))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	logged := captureLog(t)
	roots := []HarnessRoot{{harness.Claude, claudeRoot}, {harness.Codex, codexRoot}}
	if err := copyRawSupport(source, roots, "github.com/o/skills", mode.ImplicitEffective()); err != nil {
		t.Fatal(err)
	}
	if err := stageSourceTree(source, roots, "", "github.com/o/skills", mode.ImplicitEffective()); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{claudeRoot, codexRoot} {
		for _, relative := range []string{"skills/devops/SKILL.md", "skills/devops/scripts/run.py", "skills/seo/SKILL.md", "skills/local/SKILL.md", "skills/linked/SKILL.md"} {
			if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
				t.Errorf("%s: %v", root, err)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "skills", "leak")); err == nil {
			t.Fatalf("%s staged a skill from outside the package", root)
		}
	}
	if _, err := os.Stat(filepath.Join(claudeRoot, "commands", "ship.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(codexRoot, "skills", "ship", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"skipping skill leak", `.claude-plugin/plugin.json agents: path "./missing/" does not exist`} {
		if !strings.Contains(logged.String(), part) {
			t.Fatalf("log lacks %q:\n%s", part, logged.String())
		}
	}
}

// The official zoom plugin has commands and skills with the same names.
func TestCodexKeepsACommandWhoseNameIsAlsoASkill(t *testing.T) {
	source, claudeRoot, codexRoot := t.TempDir(), t.TempDir(), t.TempDir()
	writeTree(t, source, map[string]string{
		"skills/build-bot/SKILL.md": "---\nname: build-bot\ndescription: The skill\n---\nSkill body.\n",
		"commands/build-bot.md":     "---\ndescription: The command\n---\nCommand body.\n",
		"agents/build-bot.md":       "---\ndescription: The agent\n---\nAgent body.\n",
		"commands/only-command.md":  "---\ndescription: Alone\n---\nAlone body.\n",
	})
	logged := captureLog(t)
	roots := []HarnessRoot{{harness.Claude, claudeRoot}, {harness.Codex, codexRoot}}
	if err := stageSourceTree(source, roots, "", "github.com/o/zoom", mode.ImplicitEffective()); err != nil {
		t.Fatal(err)
	}
	for relative, body := range map[string]string{
		"skills/build-bot/SKILL.md":         "Skill body.",
		"skills/build-bot-command/SKILL.md": "Command body.",
		"skills/build-bot-agent/SKILL.md":   "Agent body.",
		"skills/only-command/SKILL.md":      "Alone body.",
	} {
		data, err := os.ReadFile(filepath.Join(codexRoot, relative))
		if err != nil || !strings.Contains(string(data), body) {
			t.Fatalf("codex %s = %q, %v", relative, data, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(codexRoot, "skills/build-bot-command/SKILL.md")); !strings.Contains(string(data), "name: build-bot-command") {
		t.Fatalf("converted skill keeps the taken name:\n%s", data)
	}
	if !strings.Contains(logged.String(), "command build-bot is staged as skill build-bot-command") || strings.Contains(logged.String(), "only-command is staged") {
		t.Fatalf("log:\n%s", logged.String())
	}
	// Claude Code has commands and agents, so nothing is renamed there.
	for _, relative := range []string{"commands/build-bot.md", "agents/build-bot.md", "skills/build-bot/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(claudeRoot, relative)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(claudeRoot, "skills", "build-bot-command")); err == nil {
		t.Fatal("claude got a renamed skill")
	}
}
