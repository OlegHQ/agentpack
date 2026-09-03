package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	if result.err != nil || strings.TrimSpace(result.stdout) != "agentpack 0.3.12" {
		t.Fatalf("--version: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	result = runCLI(t, t.TempDir(), "mode", "--help")
	if result.err != nil || !strings.Contains(result.stdout, "create") || !strings.Contains(result.stdout, "tui") {
		t.Fatalf("mode --help: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
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
	if !strings.Contains(lock, "lockfile-version = 2") || !(strings.Contains(lock, "module = \"local-skill\"") || strings.Contains(lock, "module = 'local-skill'")) {
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
	for _, relative := range []string{
		"plugins/agentpack-bundle/skills/portable-skill/SKILL.md",
		"opencode/skills/portable-skill/SKILL.md",
		"codex-home/skills/portable-skill/SKILL.md",
		"cursor/agentpack-bundle/skills/portable-skill/SKILL.md",
		"grok/agentpack-bundle/skills/portable-skill/SKILL.md",
		"agy/agentpack-bundle/skills/portable-skill/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Errorf("%s: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "_agentpack", "cache", "db.reddb")); err != nil {
		t.Errorf("documented cache index path: %v", err)
	}
}

type commandResult struct {
	stdout string
	stderr string
	err    error
}

func runCLI(t *testing.T, workingDirectory string, arguments ...string) commandResult {
	t.Helper()
	home := filepath.Join(workingDirectory, "_home")
	agentpackHome := filepath.Join(workingDirectory, "_agentpack")
	stagingRoot := filepath.Join(workingDirectory, "_staging")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(agentpackBinary, arguments...)
	command.Dir = workingDirectory
	command.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "AGENTPACK_HOME="+agentpackHome, "AGENTPACK_STAGING_ROOT="+stagingRoot)
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
