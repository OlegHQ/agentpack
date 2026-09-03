package manifest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/mode"
)

func writeManifest(t *testing.T, root, extra string) {
	t.Helper()
	body := "name = \"proj\"\nversion = \"0.1.0\"\n\n[dependencies]\n\"github.com/acme/repo\" = {}\n\n" + extra
	if err := os.WriteFile(filepath.Join(root, "agentpack.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSupportsShortAndTableDependencies(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "\"github.com/acme/short\" = \"v1.2.3\"\n\"github.com/acme/path\" = { path = \"../skill\" }\n")
	manifest, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	short := manifest.Dependencies["github.com/acme/short"]
	if short.Short == nil || *short.Short != "v1.2.3" {
		t.Fatalf("short dependency = %+v", short)
	}
	path, ok := manifest.Dependencies["github.com/acme/path"].PathValue()
	if !ok || path != "../skill" {
		t.Fatalf("path dependency = %q, %v", path, ok)
	}
}

func TestImplicitDefaultModeIsAvailable(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "")
	manifest, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	definition, ok := manifest.ModeDefinition("default")
	if !ok || definition.Base != mode.BaseAll {
		t.Fatalf("default mode = %+v, %v", definition, ok)
	}
	if !slices.Contains(manifest.ListModeNames(), "default") {
		t.Fatal("default absent from mode names")
	}
}

func TestModeCRUDRoundTrips(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "")
	if err := CreateMode(root, "design"); err != nil {
		t.Fatal(err)
	}
	if err := SetModeBase(root, "design", mode.BaseNone); err != nil {
		t.Fatal(err)
	}
	if err := AddModeSelectors(root, "design", true, []string{"package:github.com/acme/repo", ".agents:rules/backend.mdc"}); err != nil {
		t.Fatal(err)
	}
	if err := RenameMode(root, "design", "review"); err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	review, ok := manifest.ModeDefinition("review")
	if !ok || review.Base != mode.BaseNone || !slices.Equal(review.Enable, []string{".agents:rules/backend.mdc", "package:github.com/acme/repo"}) {
		t.Fatalf("review = %+v", review)
	}
	if err := DeleteMode(root, "review"); err != nil {
		t.Fatal(err)
	}
	manifest, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.ModeDefinition("review"); ok {
		t.Fatal("deleted mode still exists")
	}
}

func TestRemoveDependencyPrunesModeSelectors(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "[modes.default]\nbase = \"all\"\ndisable = [\"package:github.com/acme/repo\", \"package-path:github.com/acme/repo:hooks\"]\n")
	if err := RemoveDependencyEntry(root, "github.com/acme/repo"); err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := manifest.ModeDefinition("default")
	if len(definition.Disable) != 0 {
		t.Fatalf("selectors not pruned: %v", definition.Disable)
	}
}

func TestMCPMutationRoundTrips(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "# preserve me\n")
	command, url := "npx", "https://example.com/mcp"
	if err := AddMCPServer(root, "stdio", mcp.Server{Command: &command, Args: []string{"-y", "pkg"}, Env: map[string]string{"TOKEN": "secret"}}); err != nil {
		t.Fatal(err)
	}
	if err := AddMCPServer(root, "remote", mcp.Server{URL: &url}); err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MCP.Servers) != 2 || manifest.MCP.Servers["stdio"].Command == nil {
		t.Fatalf("MCP = %+v", manifest.MCP.Servers)
	}
	removed, err := RemoveMCPServer(root, "stdio")
	if err != nil || !removed {
		t.Fatalf("RemoveMCPServer() = %v, %v", removed, err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "agentpack.toml"))
	if !strings.Contains(string(raw), "preserve me") {
		t.Fatalf("comment lost:\n%s", raw)
	}
}
