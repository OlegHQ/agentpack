package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePlugin(t *testing.T, root, harness, name string) {
	t.Helper()
	directory := map[string]string{"claude": ".claude-plugin", "cursor": ".cursor-plugin", "codex": ".codex-plugin"}[harness]
	if err := writeJSON(filepath.Join(root, directory, "plugin.json"), map[string]any{"name": name, "version": "1.0.0"}); err != nil {
		t.Fatal(err)
	}
}

func TestMarketplaceMergesProvidersInDeterministicPriority(t *testing.T) {
	root := filepath.Join(t.TempDir(), "marketplace")
	claude := filepath.Join(root, "providers", "claude", "plugin")
	cursor := filepath.Join(root, "providers", "cursor", "plugin")
	codex := filepath.Join(root, "providers", "codex", "plugin")
	writePlugin(t, claude, "claude", "paddle")
	writePlugin(t, cursor, "cursor", "paddle")
	writePlugin(t, codex, "codex", "paddle")
	for path, body := range map[string]string{
		filepath.Join(claude, "skills", "billing", "SKILL.md"): "# Billing\n",
		filepath.Join(cursor, "skills", "billing", "SKILL.md"): "# Cursor billing\n",
		filepath.Join(codex, "skills", "billing", "SKILL.md"):  "# Codex billing\n",
		filepath.Join(cursor, "rules", "paddle.mdc"):           "# Paddle\n",
		filepath.Join(codex, "assets", "logo.svg"):             "<svg/>\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeJSON(filepath.Join(root, ".claude-plugin", "marketplace.json"), map[string]any{"plugins": []any{map[string]any{"name": "paddle", "source": "./providers/claude/plugin"}}}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, ".cursor-plugin", "marketplace.json"), map[string]any{"plugins": []any{map[string]any{"name": "paddle", "source": "./providers/cursor/plugin"}}}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, ".agents", "plugins", "marketplace.json"), map[string]any{"plugins": []any{map[string]any{"name": "paddle", "source": map[string]any{"source": "local", "path": "./providers/codex/plugin"}}}}); err != nil {
		t.Fatal(err)
	}
	materialized, err := materializeSinglePluginMarketplace(root)
	if err != nil || !materialized {
		t.Fatalf("materializeSinglePluginMarketplace() = %v, %v", materialized, err)
	}
	body, err := os.ReadFile(filepath.Join(root, "skills", "billing", "SKILL.md"))
	if err != nil || string(body) != "# Billing\n" {
		t.Fatalf("portable conflict winner = %q, %v", body, err)
	}
	for _, path := range []string{".claude-plugin/plugin.json", ".cursor-plugin/plugin.json", ".codex-plugin/plugin.json", "rules/paddle.mdc", "assets/logo.svg"} {
		if !regularFile(filepath.Join(root, filepath.FromSlash(path))) {
			t.Fatalf("merged marketplace missing %s", path)
		}
	}
	if hasMarketplaceManifest(root) {
		t.Fatal("marketplace manifests remained after materialization")
	}
}

func TestMarketplaceSupportsPluginRootAndRejectsTraversal(t *testing.T) {
	t.Run("plugin root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "marketplace")
		writePlugin(t, filepath.Join(root, "plugins", "paddle"), "claude", "paddle")
		value := map[string]any{"metadata": map[string]any{"pluginRoot": "./plugins"}, "plugins": []any{map[string]any{"name": "paddle", "source": "paddle"}}}
		if err := writeJSON(filepath.Join(root, ".claude-plugin", "marketplace.json"), value); err != nil {
			t.Fatal(err)
		}
		if ok, err := materializeSinglePluginMarketplace(root); err != nil || !ok {
			t.Fatalf("materialize = %v, %v", ok, err)
		}
	})
	t.Run("traversal", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "marketplace")
		value := map[string]any{"plugins": []any{map[string]any{"name": "escape", "source": "./../escape"}}}
		if err := writeJSON(filepath.Join(root, ".claude-plugin", "marketplace.json"), value); err != nil {
			t.Fatal(err)
		}
		_, err := materializeSinglePluginMarketplace(root)
		if err == nil || !strings.Contains(err.Error(), "unsafe marketplace source") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestMarketplaceRejectsMultiplePluginNames(t *testing.T) {
	root := filepath.Join(t.TempDir(), "marketplace")
	for _, name := range []string{"one", "two"} {
		writePlugin(t, filepath.Join(root, "plugins", name), "claude", name)
	}
	value := map[string]any{"plugins": []any{map[string]any{"name": "one", "source": "./plugins/one"}, map[string]any{"name": "two", "source": "./plugins/two"}}}
	if err := writeJSON(filepath.Join(root, ".claude-plugin", "marketplace.json"), value); err != nil {
		t.Fatal(err)
	}
	_, err := materializeSinglePluginMarketplace(root)
	if err == nil || !strings.Contains(err.Error(), "multiple plugins (one, two)") {
		t.Fatalf("error = %v", err)
	}
	if !hasMarketplaceManifest(root) {
		t.Fatal("rejected marketplace was mutated")
	}
}

func TestNormalizeCodexOnlyPluginSynthesizesAllManifestsAndMCP(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "codex", "native-codex")
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"url":"https://example.com"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NormalizePluginLayout(root); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ClaudePluginManifestPath(root), CursorPluginManifestPath(root), CodexPluginManifestPath(root), filepath.Join(root, "mcp.json")} {
		if !regularFile(path) {
			t.Fatalf("normalization missing %s", path)
		}
	}
}

func TestNormalizeAgentpackManifestOnlySynthesizesAllPluginManifests(t *testing.T) {
	root := t.TempDir()
	manifest := "name = \"pkg-a\"\nversion = \"2.0.0\"\ndescription = \"manifest plugin\"\n\n[dependencies]\n"
	if err := os.WriteFile(filepath.Join(root, "agentpack.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NormalizePluginLayout(root); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ClaudePluginManifestPath(root), CursorPluginManifestPath(root), CodexPluginManifestPath(root)} {
		if !regularFile(path) {
			t.Fatalf("normalization missing %s", path)
		}
	}
}

func TestCopyPackageDirToCacheUsesContentAddressedDestination(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	key, commit, destination, err := CopyPackageDirToCache(source, "path:"+source)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 64 || len(commit) != 40 || filepath.Base(destination) != key {
		t.Fatalf("copy result = %q, %q, %q", key, commit, destination)
	}
	if !regularFile(filepath.Join(destination, "SKILL.md")) {
		t.Fatal("cached SKILL.md missing")
	}
}
