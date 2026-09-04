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
	if result.err != nil || strings.TrimSpace(result.stdout) != "agentpack 0.3.17" {
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
	if !strings.Contains(lock, "lockfile_version = 2") || !(strings.Contains(lock, "module = \"local-skill\"") || strings.Contains(lock, "module = 'local-skill'")) {
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
	if _, err := os.Stat(filepath.Join(root, "plugins", "agentpack-bundle", "skills", "project-local", "SKILL.md")); err != nil {
		t.Errorf("Claude project skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "codex-home", "skills", "project-local", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("project skill duplicated into Codex home: %v", err)
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

	result := runCLIWithEnv(t, nested, []string{"--yolo", "codex"}, "CODEX_PATH="+fakeCodex)
	if result.err != nil {
		t.Fatalf("nested Rust-v2 launch: stdout=%q stderr=%q err=%v", result.stdout, result.stderr, result.err)
	}
	if _, err := os.Stat(filepath.Join(nested, "_staging", "modes", "default", "codex-home", "config.toml")); err != nil {
		t.Fatalf("staging was not rooted at ancestor project: %v", err)
	}
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
