package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
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
