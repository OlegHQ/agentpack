package codex

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/OlegHQ/agentpack/internal/paths"
)

func TestMCPAuthLastLogoutDoesNotRestoreCredential(t *testing.T) {
	project := t.TempDir()
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	staged, err := paths.StagingCodexHomeDirForMode(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareMCPAuth(project, staged); err != nil {
		t.Fatal(err)
	}
	durable, err := oauthCredentials(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCredentialStore(staged+string(os.PathSeparator)+credentialsFile, map[string]any{"server": map[string]any{"token": "old"}}); err != nil {
		t.Fatal(err)
	}
	if err := reconcileMCPAuthMode(project, "default"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(staged, credentialsFile)); err != nil {
		t.Fatal(err)
	}
	if err := reconcileMCPAuthMode(project, "default"); err != nil {
		t.Fatal(err)
	}
	store, err := readCredentialStore(durable)
	if err != nil || len(store) != 0 {
		t.Fatalf("last MCP logout was restored: %v, %v", store, err)
	}
	if err := verifyMCPAuth(project, staged); err != nil {
		t.Fatal(err)
	}
}

func TestMCPAuthCopiedStoreAppliesChangesAndDeletions(t *testing.T) {
	project := t.TempDir()
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	staged := t.TempDir()
	if err := prepareMCPAuth(project, staged); err != nil {
		t.Fatal(err)
	}
	durable, err := oauthCredentials(project)
	if err != nil {
		t.Fatal(err)
	}
	stagedFile := filepath.Join(staged, credentialsFile)
	if err := os.Remove(stagedFile); err != nil {
		t.Fatal(err)
	}
	if err := writeCredentialStore(stagedFile, map[string]any{"a": map[string]any{"token": "a"}, "b": map[string]any{"token": "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := withMCPOAuthLock(project, func() error { return reconcileMCPAuthModeLocked(staged, durable) }); err != nil {
		t.Fatal(err)
	}
	if err := writeCredentialStore(durable, map[string]any{"a": map[string]any{"token": "a"}, "b": map[string]any{"token": "b"}, "c": map[string]any{"token": "c"}}); err != nil {
		t.Fatal(err)
	}
	if err := writeCredentialStore(stagedFile, map[string]any{"b": map[string]any{"token": "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := withMCPOAuthLock(project, func() error { return reconcileMCPAuthModeLocked(staged, durable) }); err != nil {
		t.Fatal(err)
	}
	store, err := readCredentialStore(durable)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"b": map[string]any{"token": "b"}, "c": map[string]any{"token": "c"}}; !reflect.DeepEqual(store, want) {
		t.Fatalf("merged store=%v, want %v", store, want)
	}
	if info, err := os.Lstat(stagedFile); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("staged credential file is not regular: %v, %v", info, err)
	}
}

func TestMCPAuthCrossFilesystemUsesRegularCopy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/dev/shm is Linux-specific")
	}
	project := t.TempDir()
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	staged, err := os.MkdirTemp("/dev/shm", "agentpack-codex-mcp-*")
	if err != nil {
		t.Skipf("no separate temporary filesystem: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(staged) })
	durable, err := oauthCredentials(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCredentialStore(durable, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(staged, "link-probe")
	if err := os.Link(durable, probe); err == nil {
		os.Remove(probe)
		t.Skip("/dev/shm is on the same filesystem")
	}
	if err := prepareMCPAuth(project, staged); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(staged, credentialsFile)); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("cross-filesystem credentials must be a regular file: %v, %v", info, err)
	}
	if err := verifyMCPAuth(project, staged); err != nil {
		t.Fatal(err)
	}
}

func TestMCPAuthRecoveryAppliesUnfinishedLogout(t *testing.T) {
	project := t.TempDir()
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	staged, err := paths.StagingCodexHomeDirForMode(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareMCPAuth(project, staged); err != nil {
		t.Fatal(err)
	}
	stagedFile := filepath.Join(staged, credentialsFile)
	if err := writeCredentialStore(stagedFile, map[string]any{"server": map[string]any{"token": "old"}}); err != nil {
		t.Fatal(err)
	}
	if err := reconcileMCPAuthMode(project, "default"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stagedFile); err != nil {
		t.Fatal(err)
	}
	if err := recoverMCPAuth(project, "default"); err != nil {
		t.Fatal(err)
	}
	durable, err := oauthCredentials(project)
	if err != nil {
		t.Fatal(err)
	}
	store, err := readCredentialStore(durable)
	if err != nil || len(store) != 0 {
		t.Fatalf("recovered store restored logged-out credential: %v, %v", store, err)
	}
}
