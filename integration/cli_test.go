package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/paths"
)

var agentpackBinary string

func TestMain(m *testing.M) {
	_, source, _, _ := runtime.Caller(0)
	repositoryRoot := filepath.Dir(filepath.Dir(source))
	buildRoot, err := os.MkdirTemp("", "agentpack-integration-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(buildRoot)
	agentpackBinary = filepath.Join(buildRoot, "agentpack")
	if runtime.GOOS == "windows" {
		agentpackBinary += ".exe"
	}
	command := exec.Command("go", "build", "-o", agentpackBinary, "./cmd/agentpack")
	command.Dir = repositoryRoot
	if output, err := command.CombinedOutput(); err != nil {
		panic(string(output) + err.Error())
	}
	os.Exit(m.Run())
}

func TestCompiledCLIHelpAndVersion(t *testing.T) {
	result := runCLI(t, t.TempDir(), "--version")
	if result.err != nil || strings.TrimSpace(result.stdout) != "agentpack 0.3.27" {
		t.Fatalf("--version: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	result = runCLI(t, t.TempDir(), "mode", "--help")
	if result.err != nil || !strings.Contains(result.stdout, "create") || !strings.Contains(result.stdout, "tui") {
		t.Fatalf("mode --help: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	result = runCLI(t, t.TempDir(), "--help")
	if result.err != nil || !strings.Contains(result.stdout, "COMMANDS") || !strings.Contains(result.stdout, "completion") {
		t.Fatalf("rendered help: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
}

func TestCompiledCLIInitAndRefusesOverwrite(t *testing.T) {
	project := t.TempDir()
	result := runCLI(t, project, "--project-root", project, "init", "--name", "demo", "--version", "1.2.3")
	if result.err != nil {
		t.Fatalf("init: stderr=%q err=%v", result.stderr, result.err)
	}
	manifest := readFile(t, filepath.Join(project, "agentpack.toml"))
	if !strings.Contains(manifest, "name = \"demo\"") || !strings.Contains(manifest, "version = \"1.2.3\"") {
		t.Fatalf("manifest=%q", manifest)
	}
	if _, err := os.Stat(filepath.Join(project, "pack.lock")); err != nil {
		t.Fatal(err)
	}
	result = runCLI(t, project, "--project-root", project, "init")
	if result.err == nil || !strings.Contains(result.stderr, "agentpack.toml") {
		t.Fatalf("second init: stderr=%q err=%v", result.stderr, result.err)
	}
}

func TestCompiledCLIAddLocalDependencyFromWorkingDirectory(t *testing.T) {
	project := t.TempDir()
	skill := filepath.Join(project, "local-skill")
	if err := os.Mkdir(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("# Local skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := runCLI(t, project, "add", "local-skill", "--no-sync")
	if result.err != nil {
		t.Fatalf("add: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	manifest := readFile(t, filepath.Join(project, "agentpack.toml"))
	if !strings.Contains(manifest, "local-skill = { path = \"local-skill\" }") {
		t.Fatalf("manifest=%q", manifest)
	}
	lock := readFile(t, filepath.Join(project, "pack.lock"))
	if !strings.Contains(lock, "lockfile_version = 3") || !strings.Contains(lock, "content_hash = 'sha256-tree-v1:") || !(strings.Contains(lock, "module = \"local-skill\"") || strings.Contains(lock, "module = 'local-skill'")) {
		t.Fatalf("lock=%q", lock)
	}
}

func TestCompiledCLISyncRejectsUnknownModeCapability(t *testing.T) {
	project := t.TempDir()
	result := runCLI(t, project, "init")
	if result.err != nil {
		t.Fatalf("init: stderr=%q err=%v", result.stderr, result.err)
	}
	writeFile(t, filepath.Join(project, "agentpack.toml"), "name = \"demo\"\nversion = \"0.0.1\"\n\n[dependencies]\n\n[modes.default]\nbase = \"all\"\ndisable = [\"mcp:missing\"]\n")
	result = runCLI(t, project, "sync", "--dry-run")
	if result.err == nil || !strings.Contains(result.stderr, "unknown MCP selector target") {
		t.Fatalf("sync: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
}

func TestCompiledCLISyncStagesLocalSkillForEveryHarness(t *testing.T) {
	project := t.TempDir()
	skill := filepath.Join(project, "portable-skill")
	if err := os.Mkdir(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(skill, "SKILL.md"), "---\nname: portable-skill\ndescription: portable fixture\n---\n\n# Portable\n")
	projectSkill := filepath.Join(project, ".agents", "skills", "project-local", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(projectSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, projectSkill, "---\nname: project-local\ndescription: native project fixture\n---\n\n# Project local\n")
	if result := runCLI(t, project, "init"); result.err != nil {
		t.Fatalf("init: stderr=%q err=%v", result.stderr, result.err)
	}
	if result := runCLI(t, project, "add", "portable-skill", "--no-sync"); result.err != nil {
		t.Fatalf("add: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	if result := runCLI(t, project, "sync"); result.err != nil {
		t.Fatalf("sync: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	root := filepath.Join(project, "_staging", "modes", "default")
	codexHome := stagedCodexHome(t, project, project)
	for _, relative := range []string{
		"plugins/agentpack-bundle/skills/portable-skill/SKILL.md",
		"opencode/skills/portable-skill/SKILL.md",
		"cursor/agentpack-bundle/skills/portable-skill/SKILL.md",
		"grok/agentpack-bundle/skills/portable-skill/SKILL.md",
		"agy/agentpack-bundle/skills/portable-skill/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Errorf("%s: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(codexHome, "skills", "portable-skill", "SKILL.md")); err != nil {
		t.Errorf("Codex portable skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "_agentpack", "cache", "db.reddb")); err != nil {
		t.Errorf("documented cache index path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", "agentpack-bundle", "skills", "project-local", "SKILL.md")); err != nil {
		t.Errorf("Claude project skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "skills", "project-local", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("project skill duplicated into Codex home: %v", err)
	}
}

func TestCompiledCLIGrokInspectRegistersBundleSkill(t *testing.T) {
	if _, err := exec.LookPath("grok"); err != nil {
		t.Skip("grok is not installed")
	}
	project := t.TempDir()
	skill := filepath.Join(project, "portable-skill")
	if err := os.Mkdir(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(skill, "SKILL.md"), "---\nname: portable-skill\ndescription: portable fixture\n---\n\n# Portable\n")
	if result := runCLI(t, project, "init"); result.err != nil {
		t.Fatalf("init: stderr=%q err=%v", result.stderr, result.err)
	}
	if result := runCLI(t, project, "add", "portable-skill", "--no-sync"); result.err != nil {
		t.Fatalf("add: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	if result := runCLI(t, project, "sync"); result.err != nil {
		t.Fatalf("sync: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	home, err := filepath.Glob(filepath.Join(project, "_agentpack", "projects", "*", "grok-home"))
	if err != nil || len(home) != 1 {
		t.Fatalf("grok home: %v %v", home, err)
	}
	bundle := filepath.Join(project, "_staging", "modes", "default", "grok", "agentpack-bundle")
	target, err := os.Readlink(filepath.Join(home[0], "plugins", "agentpack-bundle"))
	if err != nil || target != bundle {
		t.Fatalf("plugin link target=%q err=%v", target, err)
	}
	command := exec.Command("grok", "inspect", "--json")
	command.Dir = project
	command.Env = append(os.Environ(), "GROK_HOME="+home[0])
	output, err := command.Output()
	if err != nil {
		t.Fatalf("grok inspect: %v", err)
	}
	var discovered struct {
		Skills []struct {
			Name   string `json:"name"`
			Source struct {
				Type string `json:"type"`
			} `json:"source"`
		} `json:"skills"`
		Plugins []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(output, &discovered); err != nil {
		t.Fatal(err)
	}
	registered := false
	for _, skill := range discovered.Skills {
		if skill.Name == "portable-skill" && skill.Source.Type == "plugin" {
			registered = true
		}
	}
	enabled := false
	for _, plugin := range discovered.Plugins {
		if plugin.Name == "agentpack-bundle" && plugin.Enabled {
			enabled = true
		}
	}
	if !registered || !enabled {
		t.Fatalf("registered=%v enabled=%v", registered, enabled)
	}
}

func TestCompiledCLILaunchesFromNestedRustV2Project(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake POSIX harness executable")
	}
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "agentpack.toml"), "name = \"real-project\"\nversion = \"0.0.1\"\n\n[dependencies]\n")
	writeFile(t, filepath.Join(project, "pack.lock"), "lockfile_version = 2\n\n[meta]\nname = \"real-project\"\nversion = \"0.0.1\"\n")
	nested := filepath.Join(project, "apps", "web", "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeCodex := filepath.Join(project, "fake-codex")
	writeFile(t, fakeCodex, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(fakeCodex, 0o755); err != nil {
		t.Fatal(err)
	}

	result := runCLIWithEnv(t, nested, []string{"codex", "--mode", "default", "--yolo", "--", "--debug", "passthrough"}, "CODEX_PATH="+fakeCodex)
	if result.err != nil {
		t.Fatalf("nested Rust-v2 launch: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	if _, err := os.Stat(filepath.Join(stagedCodexHome(t, project, nested), "config.toml")); err != nil {
		t.Fatalf("staging was not rooted at ancestor project: %v", err)
	}
}

func TestCompiledCLIRefusesTamperedCacheUntilRepaired(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake POSIX harness executable")
	}
	project := t.TempDir()
	skill := filepath.Join(project, "local-skill")
	if err := os.Mkdir(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: local-skill\ndescription: fixture\n---\n\n# Local\n"
	writeFile(t, filepath.Join(skill, "SKILL.md"), body)
	if result := runCLI(t, project, "add", "local-skill", "--no-sync"); result.err != nil {
		t.Fatalf("add: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	// With no dependencies in the manifest the lock is authoritative, so sync
	// uses the cache entry instead of copying the path dependency again.
	writeFile(t, filepath.Join(project, "agentpack.toml"), "name = \"demo\"\nversion = \"0.0.1\"\n\n[dependencies]\n")
	launched := filepath.Join(project, "launched")
	fakeCodex := filepath.Join(project, "fake-codex")
	writeFile(t, fakeCodex, "#!/bin/sh\ntouch \""+launched+"\"\n")
	if err := os.Chmod(fakeCodex, 0o755); err != nil {
		t.Fatal(err)
	}
	if result := runCLIWithEnv(t, project, []string{"codex"}, "CODEX_PATH="+fakeCodex); result.err != nil {
		t.Fatalf("launch: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	if err := os.Remove(launched); err != nil {
		t.Fatalf("harness did not start before tampering: %v", err)
	}
	staged := filepath.Join(project, "_staging", "modes", "default", "plugins", "agentpack-bundle", "skills", "local-skill", "SKILL.md")
	entries, err := filepath.Glob(filepath.Join(project, "_agentpack", "cache", "*", "SKILL.md"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries = %v, %v", entries, err)
	}
	writeFile(t, entries[0], body+"TAMPER\n")

	for _, arguments := range [][]string{{"sync"}, {"sync", "--verify-only"}, {"codex"}} {
		result := runCLIWithEnv(t, project, arguments, "CODEX_PATH="+fakeCodex)
		if result.err == nil || !strings.Contains(result.stderr, "content hash mismatch: local-skill\n  expected  sha256-tree-v1:") || !strings.Contains(result.stderr, "\n  cache     "+filepath.Dir(entries[0])+"\n") || !strings.Contains(result.stderr, "\n  repair    agentpack sync --repair") {
			t.Fatalf("%v: stdout=%q stderr=%q err=%v", arguments, result.stdout, result.stderr, result.err)
		}
		if readFile(t, staged) != body || !strings.Contains(readFile(t, entries[0]), "TAMPER") {
			t.Fatalf("%v changed staging or the cache", arguments)
		}
	}
	if _, err := os.Stat(launched); !os.IsNotExist(err) {
		t.Fatalf("harness was launched with a tampered cache: %v", err)
	}

	result := runCLI(t, project, "sync", "--repair")
	if result.err != nil || !strings.Contains(result.stderr, "warning: repaired local-skill") {
		t.Fatalf("repair: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	if readFile(t, entries[0]) != body {
		t.Fatal("repair left the tampered file in the cache")
	}
	if result := runCLI(t, project, "sync", "--verify-only"); result.err != nil || result.stderr != "" {
		t.Fatalf("verify after repair: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
}

func TestCompiledCLIPinsNPXServerAndRecordsUnpinnedOnlyWhenAllowed(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.EscapedPath() != "/@playwright%2Fmcp/latest" {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = response.Write([]byte(`{"version":"0.0.41","dist":{"integrity":"sha512-fixture"}}`))
	}))
	defer registry.Close()
	project := t.TempDir()
	if result := runCLI(t, project, "init"); result.err != nil {
		t.Fatalf("init: stderr=%q err=%v", result.stderr, result.err)
	}
	add := []string{"mcp", "add", "playwright", "--command", "npx", "--args", "-y", "@playwright/mcp@latest"}
	result := runCLIWithEnv(t, project, add, "NPM_CONFIG_REGISTRY="+registry.URL)
	if result.err != nil {
		t.Fatalf("mcp add: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	lock := readFile(t, filepath.Join(project, "pack.lock"))
	for _, part := range []string{"lockfile_version = 3", "[[mcp_servers]]", "status = 'pinned'", "version = '0.0.41'", "integrity = 'sha512-fixture'", "requested = '@playwright/mcp@latest'"} {
		if !strings.Contains(lock, part) {
			t.Fatalf("pack.lock lacks %q:\n%s", part, lock)
		}
	}
	staged := readFile(t, filepath.Join(project, "_staging", "modes", "default", "plugins", "agentpack-bundle", ".mcp.json"))
	if !strings.Contains(staged, `"@playwright/mcp@0.0.41"`) || strings.Contains(staged, "@latest") {
		t.Fatalf("staged MCP config = %s", staged)
	}
	if result := runCLI(t, project, "mcp", "list"); result.err != nil || !strings.Contains(result.stdout, "[from manifest] (pinned @playwright/mcp@0.0.41)") {
		t.Fatalf("mcp list: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}

	registry.Close()
	offline := t.TempDir()
	if result := runCLI(t, offline, "init"); result.err != nil {
		t.Fatalf("init: stderr=%q err=%v", result.stderr, result.err)
	}
	before := readFile(t, filepath.Join(offline, "pack.lock"))
	result = runCLIWithEnv(t, offline, add, "NPM_CONFIG_REGISTRY="+registry.URL)
	if result.err == nil || !strings.Contains(result.stderr, `annot pin MCP server "playwright"`) || !strings.Contains(result.stderr, "to record this server as unpinned") {
		t.Fatalf("offline mcp add: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	if readFile(t, filepath.Join(offline, "pack.lock")) != before {
		t.Fatal("a failed pin rewrote pack.lock")
	}
	result = runCLIWithEnv(t, offline, []string{"lock", "--allow-unpinned-mcp"}, "NPM_CONFIG_REGISTRY="+registry.URL)
	lock = readFile(t, filepath.Join(offline, "pack.lock"))
	if result.err != nil || !strings.Contains(lock, "status = 'unpinned'") || !strings.Contains(lock, "allow_unpinned = true") {
		t.Fatalf("lock --allow-unpinned-mcp: stderr=%q err=%v\n%s", result.stderr, result.err, lock)
	}
	if result := runCLI(t, offline, "mcp", "list"); result.err != nil || !strings.Contains(result.stdout, "(unpinned)") {
		t.Fatalf("mcp list: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
}

func stagedCodexHome(t *testing.T, projectRoot, workingDirectory string) string {
	t.Helper()
	hash, err := paths.ProjectPathHash(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	pointer := filepath.Join(workingDirectory, "_agentpack", "projects", hash, "codex-generations", "default.current")
	return strings.TrimSpace(readFile(t, pointer))
}

type commandResult struct {
	stdout string
	stderr string
	err    error
}

func runCLI(t *testing.T, workingDirectory string, arguments ...string) commandResult {
	return runCLIWithEnv(t, workingDirectory, arguments)
}

func runCLIWithEnv(t *testing.T, workingDirectory string, arguments []string, extraEnvironment ...string) commandResult {
	t.Helper()
	home := filepath.Join(workingDirectory, "_home")
	agentpackHome := filepath.Join(workingDirectory, "_agentpack")
	stagingRoot := filepath.Join(workingDirectory, "_staging")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(agentpackBinary, arguments...)
	command.Dir = workingDirectory
	overrides := []string{"HOME=" + home, "USERPROFILE=" + home, "AGENTPACK_HOME=" + agentpackHome, "AGENTPACK_STAGING_ROOT=" + stagingRoot}
	command.Env = installerEnvironment(append(overrides, extraEnvironment...)...)
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	return commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
