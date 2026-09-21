package grok

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func TestCredentialsFollowProjectsRefreshAndLogout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	first := base.LaunchContext{ProjectRoot: t.TempDir(), Mode: mode.ImplicitEffective()}
	second := base.LaunchContext{ProjectRoot: t.TempDir(), Mode: mode.ImplicitEffective()}
	firstHome, err := paths.StagingGrokHomeDirForMode(first.ProjectRoot, first.Mode.Name())
	if err != nil {
		t.Fatal(err)
	}
	secondHome, err := paths.StagingGrokHomeDirForMode(second.ProjectRoot, second.Mode.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(firstHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(secondHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := loadSharedCredentials(firstHome); err != nil {
		t.Fatal(err)
	}
	for _, name := range credentialFiles {
		if err := os.WriteFile(filepath.Join(firstHome, name), []byte("first-login"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := persistCredentials(first); err != nil {
		t.Fatal(err)
	}
	shared, err := paths.SharedGrokCredentialsDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range credentialFiles {
		info, err := os.Stat(filepath.Join(shared, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("shared %s permissions: %v, %v", name, info, err)
		}
	}
	if err := loadSharedCredentials(secondHome); err != nil {
		t.Fatal(err)
	}
	for _, name := range credentialFiles {
		assertCredential(t, filepath.Join(secondHome, name), "first-login")
		path := filepath.Join(secondHome, name)
		if err := os.WriteFile(path+".new", []byte("refreshed"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path+".new", time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".new", path); err != nil {
			t.Fatal(err)
		}
	}
	if err := persistCredentials(second); err != nil {
		t.Fatal(err)
	}
	if err := loadSharedCredentials(firstHome); err != nil {
		t.Fatal(err)
	}
	for _, name := range credentialFiles {
		assertCredential(t, filepath.Join(firstHome, name), "refreshed")
		if err := os.Remove(filepath.Join(firstHome, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := persistCredentials(first); err != nil {
		t.Fatal(err)
	}
	if err := loadSharedCredentials(secondHome); err != nil {
		t.Fatal(err)
	}
	for _, name := range credentialFiles {
		if _, err := os.Stat(filepath.Join(secondHome, name)); !os.IsNotExist(err) {
			t.Fatalf("%s survived logout: %v", name, err)
		}
	}
}

func TestCredentialsMigrateFromAnotherProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	oldProject, newProject := t.TempDir(), t.TempDir()
	oldHome, err := paths.StagingGrokHomeDirForMode(oldProject, "default")
	if err != nil {
		t.Fatal(err)
	}
	newHome, err := paths.StagingGrokHomeDirForMode(newProject, "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(oldHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldHome, "auth.json"), []byte("prior-login"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadSharedCredentials(newHome); err != nil {
		t.Fatal(err)
	}
	assertCredential(t, filepath.Join(newHome, "auth.json"), "prior-login")
}

func assertCredential(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != expected {
		t.Fatalf("credential %s = %q, %v", path, data, err)
	}
}
