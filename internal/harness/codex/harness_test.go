package codex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
	"github.com/pelletier/go-toml/v2"
	keyring "github.com/zalando/go-keyring"
)

func TestKeyringAccountUsesCanonicalSHA256Prefix(t *testing.T) {
	home := t.TempDir()
	first := keyringAccount(home)
	if len(first) != 20 || first[:4] != "cli|" {
		t.Fatalf("account=%q", first)
	}
	if got := codexKeyringTarget(authKeyringService, first); got != first+".Codex Auth" {
		t.Fatalf("Windows Codex keyring target = %q", got)
	}
	if got, err := decodeCodexWindowsPassword([]byte{'k', 0, 'e', 0, 'y', 0}); err != nil || got != "key" {
		t.Fatalf("Windows Codex keyring password = %q, %v", got, err)
	}
}

func TestEncryptedCodexAuthBridgesIntoSharedFile(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()
	native := filepath.Join(home, ".codex")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(native, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	passphrase := "test-passphrase-for-codex-auth"
	account := "secrets|" + strings.TrimPrefix(keyringAccount(native), "cli|")
	if err := keyring.Set(encryptedAuthKeyringService, account, passphrase); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(authKeyringService, keyringAccount(native), `{"OPENAI_API_KEY":"stale-direct"}`); err != nil {
		t.Fatal(err)
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext bytes.Buffer
	writer, err := age.Encrypt(&ciphertext, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(`{"version":1,"secrets":{"global/CODEX_AUTH":"{\"OPENAI_API_KEY\":\"encrypted\"}"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(native, "secrets", "codex_auth.age"), ciphertext.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	shared, err := sharedAuthSource(native)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(shared)
	if err != nil || string(data) != `{"OPENAI_API_KEY":"encrypted"}` {
		t.Fatalf("bridged encrypted auth = %q, %v", data, err)
	}
}

func TestVerifyDanglingAuthThroughSymlinkedHome(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv("USERPROFILE", user)
	realHome := t.TempDir()
	alias := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(realHome, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	t.Setenv("AGENTPACK_HOME", alias)
	staged := t.TempDir()
	if err := prepareAuth(filepath.Join(user, ".codex"), staged); err != nil {
		t.Fatal(err)
	}
	if err := verifyAuth(staged); err != nil {
		t.Fatal(err)
	}
}

func TestPreserveLegacyRegularAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	staged := t.TempDir()
	if err := os.WriteFile(filepath.Join(staged, "auth.json"), []byte(`{"OPENAI_API_KEY":"test-key"}`), 0o600); err != nil {
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

func TestPreserveReloginOverExistingSharedAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	shared, err := paths.SharedCodexAuthPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte(`{"OPENAI_API_KEY":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := t.TempDir()
	if err := os.WriteFile(filepath.Join(staged, "auth.json"), []byte(`{"OPENAI_API_KEY":"new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(shared, old, old); err != nil {
		t.Fatal(err)
	}
	if err := verifyAuth(staged); err == nil {
		t.Fatal("accepted detached staged auth")
	}
	if err := preserveAuth(staged); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(shared)
	if err != nil || string(data) != `{"OPENAI_API_KEY":"new"}` {
		t.Fatalf("shared auth was not updated: %q, %v", data, err)
	}
	previous, err := os.ReadFile(filepath.Join(filepath.Dir(shared), "auth.json.previous"))
	if err != nil || string(previous) != `{"OPENAI_API_KEY":"old"}` {
		t.Fatalf("previous auth was not preserved: %q, %v", previous, err)
	}
}

func TestLogoutRemovesNativeAndSharedAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	native := filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(native), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte(`{"token":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	shared, err := paths.SharedCodexAuthPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte(`{"token":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := t.TempDir()
	if err := os.WriteFile(filepath.Join(staged, "config.toml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := finishAuthLaunch(staged, []string{"-c", "model=example", "logout"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{native, shared} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("credential still exists at %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(shared), authLogoutMarker)); err != nil {
		t.Fatalf("logout marker missing: %v", err)
	}
	source, err := sharedAuthSource(filepath.Dir(native))
	if err != nil || source != shared {
		t.Fatalf("logout credential source = %q, %v", source, err)
	}
}

func TestFailedLoginRestoresStagedLinkWithoutDeletingDurableAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	native := filepath.Join(home, ".codex")
	if err := os.MkdirAll(native, 0o755); err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(native, "auth.json")
	if err := os.WriteFile(auth, []byte(`{"OPENAI_API_KEY":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := t.TempDir()
	if err := os.WriteFile(filepath.Join(staged, "config.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareAuth(native, staged); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(staged, "auth.json")); err != nil {
		t.Fatal(err)
	}
	if err := finishAuthLaunch(staged, []string{"login", "--device-auth"}); err != nil {
		t.Fatal(err)
	}
	if err := verifyAuth(staged); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(auth)
	if err != nil || string(data) != `{"OPENAI_API_KEY":"old"}` {
		t.Fatalf("durable login changed after failed login: %q, %v", data, err)
	}
}

func TestInteractiveLogoutRemovesDurableAuth(t *testing.T) {
	for _, arguments := range [][]string{nil, {"resume", "--last"}} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("AGENTPACK_HOME", t.TempDir())
			native := filepath.Join(home, ".codex")
			if err := os.MkdirAll(native, 0o755); err != nil {
				t.Fatal(err)
			}
			credential := filepath.Join(native, "auth.json")
			if err := os.WriteFile(credential, []byte(`{"OPENAI_API_KEY":"old"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			staged := t.TempDir()
			if err := os.WriteFile(filepath.Join(staged, "config.toml"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := prepareAuth(native, staged); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(staged, "auth.json")); err != nil {
				t.Fatal(err)
			}
			if err := finishAuthLaunch(staged, arguments); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(credential); !os.IsNotExist(err) {
				t.Fatalf("interactive logout retained durable auth: %v", err)
			}
		})
	}
}

func TestIsolatedLoginKeepsOldAuthOnFailureAndInstallsSuccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	native := filepath.Join(home, ".codex")
	if err := os.MkdirAll(native, 0o755); err != nil {
		t.Fatal(err)
	}
	oldAuth := filepath.Join(native, "auth.json")
	if err := os.WriteFile(oldAuth, []byte(`{"OPENAI_API_KEY":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := t.TempDir()
	if err := os.WriteFile(filepath.Join(staged, "config.toml"), []byte("cli_auth_credentials_store = 'file'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareAuth(native, staged); err != nil {
		t.Fatal(err)
	}
	failedHome, err := prepareIsolatedLoginHome(staged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(failedHome, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("isolated login inherited existing auth: %v", err)
	}
	if err := finishIsolatedLoginHome(failedHome, staged); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(oldAuth); err != nil || string(data) != `{"OPENAI_API_KEY":"old"}` {
		t.Fatalf("failed login changed existing auth: %q, %v", data, err)
	}
	successHome, err := prepareIsolatedLoginHome(staged)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(successHome, "auth.json"), []byte(`{"OPENAI_API_KEY":"new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := finishIsolatedLoginHome(successHome, staged); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(oldAuth); err != nil || string(data) != `{"OPENAI_API_KEY":"new"}` {
		t.Fatalf("successful login was not installed: %q, %v", data, err)
	}
	if err := verifyAuth(staged); err != nil {
		t.Fatal(err)
	}
	shared, err := paths.SharedCodexAuthPath()
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(shared), "auth.json.previous")); err != nil || string(data) != `{"OPENAI_API_KEY":"old"}` {
		t.Fatalf("previous login was not backed up: %q, %v", data, err)
	}
}

func TestLoginCommandUsesIsolatedHome(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	stub := filepath.Join(t.TempDir(), "codex")
	if runtime.GOOS == "windows" {
		stub += ".cmd"
	}
	if err := os.WriteFile(stub, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_PATH", stub)
	stage := base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}
	if err := New().Prepare(stage); err != nil {
		t.Fatal(err)
	}
	ctx := base.LaunchContext{ProjectRoot: project, Mode: mode.ImplicitEffective(), Arguments: []string{"login", "--device-auth"}}
	command, err := launch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := paths.StagingCodexHomeDirForMode(project, ctx.Mode.Name())
	if err != nil {
		t.Fatal(err)
	}
	var childHome string
	for _, item := range command.Env {
		if strings.HasPrefix(item, "CODEX_HOME=") {
			childHome = strings.TrimPrefix(item, "CODEX_HOME=")
		}
	}
	if childHome == "" || childHome == staged {
		t.Fatalf("login was not isolated: %q", childHome)
	}
	if _, err := os.Lstat(filepath.Join(childHome, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("isolated login home inherited auth: %v", err)
	}
	ctx.Command = command
	if err := afterLaunch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(childHome); !os.IsNotExist(err) {
		t.Fatalf("temporary login home was not removed: %v", err)
	}
}

func TestOnlyTopLevelAccountLoginUsesIsolatedHome(t *testing.T) {
	cases := []struct {
		arguments []string
		login     bool
		logout    bool
	}{
		{[]string{"login", "--with-api-key"}, true, false},
		{[]string{"-c", "model=example", "login", "--device-auth"}, true, false},
		{[]string{"login", "-c", "model=example", "status"}, false, false},
		{[]string{"mcp", "login", "manual", "--no-browser"}, false, false},
		{[]string{"mcp", "logout", "manual"}, false, false},
		{[]string{"-c", "model=example", "logout"}, false, true},
		{[]string{"--", "login"}, false, false},
	}
	for _, test := range cases {
		if actual := isCodexLogin(test.arguments); actual != test.login {
			t.Errorf("isCodexLogin(%q) = %t, want %t", test.arguments, actual, test.login)
		}
		if actual := isCodexLogout(test.arguments); actual != test.logout {
			t.Errorf("isCodexLogout(%q) = %t, want %t", test.arguments, actual, test.logout)
		}
	}
}

func TestInteractiveReloginPersistsBeforeNextSync(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	native := filepath.Join(home, ".codex")
	if err := os.MkdirAll(native, 0o755); err != nil {
		t.Fatal(err)
	}
	oldAuth := filepath.Join(native, "auth.json")
	if err := os.WriteFile(oldAuth, []byte(`{"OPENAI_API_KEY":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldAuth, old, old); err != nil {
		t.Fatal(err)
	}
	staged := t.TempDir()
	if err := os.WriteFile(filepath.Join(staged, "config.toml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "auth.json"), []byte(`{"OPENAI_API_KEY":"new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := finishAuthLaunch(staged, nil); err != nil {
		t.Fatal(err)
	}
	if err := verifyAuth(staged); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(oldAuth)
	if err != nil || string(data) != `{"OPENAI_API_KEY":"new"}` {
		t.Fatalf("new login not durable: %q, %v", data, err)
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

func TestPrepareStripsLegacyAttributionAndPreservesDaemonSetting(t *testing.T) {
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
	if features["daemon_auto_start"] != true {
		t.Fatalf("expected daemon_auto_start to be preserved, got %v", features["daemon_auto_start"])
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

func TestLaunchDoesNotForceEmbeddedServer(t *testing.T) {
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
		{"interactive", nil, []string{}},
		{"resume", []string{"resume", "--last"}, []string{"resume", "--last"}},
		{"fork", []string{"fork", "--last"}, []string{"fork", "--last"}},
		{"agents", []string{"agents"}, []string{"agents"}},
		{"queue", []string{"queue", "--thread", "id", "--message", "hello"}, []string{"queue", "--thread", "id", "--message", "hello"}},
		{"exec", []string{"exec", "hello"}, []string{"exec", "hello"}},
		{"explicit", []string{"--no-daemon", "resume"}, []string{"--no-daemon", "resume"}},
		{"remote", []string{"--remote", "ws://localhost:1234"}, []string{"--remote", "ws://localhost:1234"}},
		{"remote equals", []string{"--remote=ws://localhost:1234"}, []string{"--remote=ws://localhost:1234"}},
		{"prompt delimiter", []string{"--", "--no-daemon"}, []string{"--", "--no-daemon"}},
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

func TestLaunchFinishesAgainstOriginalGenerationAfterSwitch(t *testing.T) {
	project, userHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	stub := filepath.Join(t.TempDir(), "codex")
	if runtime.GOOS == "windows" {
		stub += ".cmd"
	}
	if err := os.WriteFile(stub, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_PATH", stub)
	native := filepath.Join(userHome, ".codex")
	if err := os.MkdirAll(native, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(native, "auth.json"), []byte(`{"OPENAI_API_KEY":"fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stage := base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}
	harness := New()
	build := func() string {
		t.Helper()
		transaction, err := beginGeneration(stage)
		if err != nil {
			t.Fatal(err)
		}
		defer transaction.Abort()
		stage.StagedRoots = map[base.Target]string{base.Codex: transaction.Root()}
		if err := harness.Prepare(stage); err != nil {
			t.Fatal(err)
		}
		if err := harness.Verify(stage); err != nil {
			t.Fatal(err)
		}
		if err := transaction.Commit(); err != nil {
			t.Fatal(err)
		}
		return transaction.Root()
	}
	first := build()
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(first)) })
	launchCtx := base.LaunchContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}
	command, err := harness.LaunchCommand(launchCtx)
	if err != nil {
		t.Fatal(err)
	}
	launchCtx.Command = command
	second := build()
	if first == second {
		t.Fatal("generation did not rotate")
	}
	if err := os.Remove(filepath.Join(first, "auth.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(first, credentialsFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, credentialsFile), []byte(`{"fixture":{"access_token":"old-generation"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := harness.AfterLaunch(launchCtx); err != nil {
		t.Fatal(err)
	}
	shared, err := paths.SharedCodexAuthPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(shared), authLogoutMarker)); err != nil {
		t.Fatalf("old generation logout was not persisted: %v", err)
	}
	durable, err := oauthCredentials(project)
	if err != nil {
		t.Fatal(err)
	}
	store, err := readCredentialStore(durable)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store["fixture"]; !ok {
		t.Fatalf("old generation MCP OAuth change was lost: %#v", store)
	}
}
