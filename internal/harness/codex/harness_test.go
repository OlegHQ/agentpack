package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
	"github.com/pelletier/go-toml/v2"
)

func TestKeyringAccountUsesCanonicalSHA256Prefix(t *testing.T) {
	home := t.TempDir()
	first := keyringAccount(home)
	if len(first) != 20 || first[:4] != "cli|" {
		t.Fatalf("account=%q", first)
	}
}

func TestPreserveLegacyRegularAuth(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	staged := t.TempDir()
	if err := os.WriteFile(filepath.Join(staged, "auth.json"), []byte(`{"refresh":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := preserveAuth(staged); err != nil {
		t.Fatal(err)
	}
	shared, _ := paths.SharedCodexAuthPath()
	data, err := os.ReadFile(shared)
	if err != nil || !json.Valid(data) {
		t.Fatalf("shared=%q err=%v", data, err)
	}
}

func TestLegacyHistoryRecoveryRejectsActiveWriter(t *testing.T) {
	staged := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staged, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "sessions", "thread.jsonl"), []byte("session"), 0o644); err != nil {
		t.Fatal(err)
	}
	locks := filepath.Join(staged, "thread-writer-locks")
	if err := os.MkdirAll(locks, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(filepath.Join(locks, "thread.lock"))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	if err := rejectActiveWriters(staged); err == nil {
		t.Fatal("active writer accepted")
	}
}
func TestPrepareMakesAuthOAuthAndHistoryDurable(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	native := filepath.Join(home, ".codex")
	if err := os.MkdirAll(native, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(native, "auth.json"), []byte(`{"token":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}
	h := New()
	if err := h.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	root, _ := h.StagedRoot(ctx)
	if !base.DurablePathMatches(filepath.Join(root, "auth.json"), filepath.Join(native, "auth.json")) {
		t.Fatal("auth not linked")
	}
	credentials, _ := oauthCredentials(project)
	if !base.DurablePathMatches(filepath.Join(root, credentialsFile), credentials) {
		t.Fatal("oauth not linked")
	}
	if err := os.WriteFile(filepath.Join(root, "sessions", "thread.jsonl"), []byte("session"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(native, "sessions", "thread.jsonl")); err != nil || string(data) != "session" {
		t.Fatalf("native session=%q err=%v", data, err)
	}
}
func TestMCPRecoveryMergesNewerKeys(t *testing.T) {
	project := t.TempDir()
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	older, _ := paths.StagingCodexHomeDirForMode(project, "default")
	newer, _ := paths.StagingCodexHomeDirForMode(project, "design")
	for _, path := range []string{older, newer} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeCredentialStore(filepath.Join(older, credentialsFile), map[string]any{"linear": map[string]any{"token": "old"}, "github": map[string]any{"token": "keep"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := writeCredentialStore(filepath.Join(newer, credentialsFile), map[string]any{"linear": map[string]any{"token": "new"}}); err != nil {
		t.Fatal(err)
	}
	if err := recoverMCPAuth(project, "default"); err != nil {
		t.Fatal(err)
	}
	durable, _ := oauthCredentials(project)
	data, err := os.ReadFile(durable)
	if err != nil {
		t.Fatal(err)
	}
	var store map[string]map[string]any
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	if store["linear"]["token"] != "new" || store["github"]["token"] != "keep" {
		t.Fatalf("store=%#v", store)
	}
}

func TestPrepareStripsLegacyAttributionAndDisablesDaemon(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	t.Setenv("AGENTPACK_KEEP_ATTRIBUTION", "")

	native := filepath.Join(home, ".codex")
	if err := os.MkdirAll(native, 0o755); err != nil {
		t.Fatal(err)
	}
	nativeConfig := `commit_attribution = "Someone <someone@example.com>"
[features]
daemon_auto_start = true
fast_mode = true
`
	if err := os.WriteFile(filepath.Join(native, "config.toml"), []byte(nativeConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}
	h := New()
	if err := h.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.Verify(ctx); err != nil {
		t.Fatal(err)
	}

	root, _ := h.StagedRoot(ctx)
	data, err := os.ReadFile(filepath.Join(root, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg["commit_attribution"]; exists {
		t.Fatalf("expected commit_attribution to be stripped, got %v", cfg["commit_attribution"])
	}
	features, ok := cfg["features"].(map[string]any)
	if !ok {
		t.Fatalf("expected features table, got %#v", cfg["features"])
	}
	if features["daemon_auto_start"] != false {
		t.Fatalf("expected daemon_auto_start to be disabled, got %v", features["daemon_auto_start"])
	}
	if features["fast_mode"] != true {
		t.Fatalf("expected fast_mode = true to be preserved, got %v", features["fast_mode"])
	}
}

func TestPreparePreservesLegacyAttributionWhenRequested(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	t.Setenv("AGENTPACK_KEEP_ATTRIBUTION", "1")

	native := filepath.Join(home, ".codex")
	if err := os.MkdirAll(native, 0o755); err != nil {
		t.Fatal(err)
	}
	nativeConfig := `commit_attribution = "Keep Me <keep@example.com>"`
	if err := os.WriteFile(filepath.Join(native, "config.toml"), []byte(nativeConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}
	h := New()
	if err := h.Prepare(ctx); err != nil {
		t.Fatal(err)
	}

	root, _ := h.StagedRoot(ctx)
	data, err := os.ReadFile(filepath.Join(root, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["commit_attribution"] != "Keep Me <keep@example.com>" {
		t.Fatalf("expected commit_attribution preserved, got %v", cfg["commit_attribution"])
	}
}

func TestLaunchDoesNotInjectNoDaemon(t *testing.T) {
	binDir := t.TempDir()
	stubName := "codex"
	script := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		stubName = "codex.cmd"
		script = "@echo off\nexit /b 0\n"
	}
	stub := filepath.Join(binDir, stubName)
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_PATH", stub)
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())

	ctx := base.LaunchContext{
		ProjectRoot: t.TempDir(),
		Mode:        mode.ImplicitEffective(),
		Arguments:   []string{"exec", "hello"},
		Yolo:        true,
	}
	cmd, err := launch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "hello"}
	if !reflect.DeepEqual(cmd.Args[1:], want) {
		t.Fatalf("got %v, want %v", cmd.Args[1:], want)
	}

	// When --no-daemon is explicitly provided by the user, preserve it
	ctx.Arguments = []string{"--no-daemon", "exec", "hello"}
	cmd, err = launch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantExplicit := []string{"--dangerously-bypass-approvals-and-sandbox", "--no-daemon", "exec", "hello"}
	if !reflect.DeepEqual(cmd.Args[1:], wantExplicit) {
		t.Fatalf("got %v, want %v", cmd.Args[1:], wantExplicit)
	}
}

func TestPrepareShortensHomeWhenSocketExceedsSunLen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SUN_LEN does not apply on Windows")
	}
	// Create an artificially long staging root to trigger needsShortHome
	longStaging := filepath.Join(t.TempDir(), "very-long-staging-path-to-exceed-sun-len-"+strings.Repeat("a", 60))
	t.Setenv("AGENTPACK_STAGING_ROOT", longStaging)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTPACK_HOME", t.TempDir())

	project := t.TempDir()
	ctx := base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}
	h := New()
	if err := h.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.Verify(ctx); err != nil {
		t.Fatal(err)
	}

	root, _ := h.StagedRoot(ctx)
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected staged root %s to be a symlink to short directory, got regular file/dir", root)
	}
	target, err := os.Readlink(root)
	if err != nil {
		t.Fatal(err)
	}
	// The short directory target + socket suffix must fit within maxSunLen
	limit := maxSunLen()
	if len(target)+len(controlSocketSuffix) >= limit {
		t.Fatalf("short target %s with socket suffix %d exceeds limit %d", target, len(target)+len(controlSocketSuffix), limit)
	}

	// Verify reset paths cleans up both symlink and target
	reset, err := h.ResetPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reset) < 2 {
		t.Fatalf("expected reset paths to include root and short dir, got %v", reset)
	}
}

func TestLaunchUsesEmbeddedServerWithModernCodex(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\nprintf '%s\\n' '--no-daemon'\n"
	if runtime.GOOS == "windows" {
		stub += ".cmd"
		script = "@echo off\necho --no-daemon\n"
	}
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_PATH", stub)
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	for _, tc := range []struct {
		name       string
		args, want []string
	}{
		{"interactive", nil, []string{"--no-daemon"}},
		{"resume", []string{"resume", "--last"}, []string{"--no-daemon", "resume", "--last"}},
		{"fork", []string{"fork", "--last"}, []string{"--no-daemon", "fork", "--last"}},
		{"explicit", []string{"--no-daemon", "resume"}, []string{"--no-daemon", "resume"}},
		{"remote", []string{"--remote", "ws://localhost:1234"}, []string{"--remote", "ws://localhost:1234"}},
		{"remote equals", []string{"--remote=ws://localhost:1234"}, []string{"--remote=ws://localhost:1234"}},
		{"prompt delimiter", []string{"--", "--no-daemon"}, []string{"--no-daemon", "--", "--no-daemon"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := launch(base.LaunchContext{ProjectRoot: t.TempDir(), Mode: mode.ImplicitEffective(), Arguments: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cmd.Args[1:], tc.want) {
				t.Fatalf("got %v, want %v", cmd.Args[1:], tc.want)
			}
		})
	}
}
