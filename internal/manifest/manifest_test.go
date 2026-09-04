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
	manifest, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	design, ok := manifest.ModeDefinition("design")
	if !ok || design.Base != mode.BaseNone || !slices.Equal(design.Enable, []string{".agents:rules/backend.mdc", "package:github.com/acme/repo"}) {
		t.Fatalf("design = %+v", design)
	}
	if err := DeleteMode(root, "design"); err != nil {
		t.Fatal(err)
	}
	manifest, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.ModeDefinition("design"); ok {
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

func TestDependencyAppendPreservesEveryUnrelatedByte(t *testing.T) {
	root := t.TempDir()
	original := "# top comment\nname  =  \"proj\" # spacing stays\n\n[dependencies] # dependency note\n# insertion anchor\n\n[custom]\nvalue = \"untouched\"\n"
	if err := os.WriteFile(filepath.Join(root, "agentpack.toml"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendDependencyPin(root, "github.com/Acme/repo/Skill", "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	want := "# top comment\nname  =  \"proj\" # spacing stays\n\n[dependencies] # dependency note\n\"github.com/Acme/repo/Skill\" = \"v1.2.3\"\n# insertion anchor\n\n[custom]\nvalue = \"untouched\"\n"
	raw, err := os.ReadFile(filepath.Join(root, "agentpack.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Fatalf("manifest bytes changed outside insertion\n--- got ---\n%s--- want ---\n%s", raw, want)
	}
}

func TestMCPReplacementPreservesEveryUnrelatedByte(t *testing.T) {
	root := t.TempDir()
	original := "name = \"proj\"\n\n[mcp.servers]\n# keep before\nserver = { command = \"old\" } # replaced\n# keep after\n\n[other]\nodd   =   true # exact\n"
	if err := os.WriteFile(filepath.Join(root, "agentpack.toml"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	command := "new"
	if err := AddMCPServer(root, "server", mcp.Server{Command: &command}); err != nil {
		t.Fatal(err)
	}
	want := "name = \"proj\"\n\n[mcp.servers]\n# keep before\nserver = { command = \"new\" }\n# keep after\n\n[other]\nodd   =   true # exact\n"
	raw, _ := os.ReadFile(filepath.Join(root, "agentpack.toml"))
	if string(raw) != want {
		t.Fatalf("manifest bytes changed outside replacement\n--- got ---\n%s--- want ---\n%s", raw, want)
	}
}

func TestModeReplacementLeavesNonModeSectionsByteExact(t *testing.T) {
	root := t.TempDir()
	original := "# top\nname  =  \"proj\"\n\n[modes.old]\nbase = \"all\"\n\n[unrelated]\nvalue   =   \"exact\" # keep\n"
	if err := os.WriteFile(filepath.Join(root, "agentpack.toml"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceModes(root, map[string]mode.Definition{"review": {Base: mode.BaseNone}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "agentpack.toml"))
	if !strings.HasPrefix(string(raw), "# top\nname  =  \"proj\"\n\n[unrelated]\nvalue   =   \"exact\" # keep") {
		t.Fatalf("non-mode content changed:\n%s", raw)
	}
	if !strings.Contains(string(raw), "[modes.review]\nbase = \"none\"") || strings.Contains(string(raw), "modes.old") {
		t.Fatalf("mode replacement failed:\n%s", raw)
	}
}
