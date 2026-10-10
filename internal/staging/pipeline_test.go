package staging

import (
	"os"
	"path/filepath"
	"testing"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/harness/codex"
	"github.com/OlegHQ/agentpack/internal/harness/registry"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
)

func TestPipelineRebuildAndVerifyEmptyPack(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	for _, path := range []string{filepath.Join(home, ".codex", "auth.json"), filepath.Join(home, ".grok", "auth.json")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pipeline := Pipeline{ProjectRoot: project, Lock: lockfile.EmptyForProject(project), Mode: mode.ImplicitEffective()}
	bundles, err := pipeline.Rebuild()
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 1 {
		t.Fatalf("bundles=%#v", bundles)
	}
	if err := pipeline.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestCodexGenerationSwitchPreservesRunningHome(t *testing.T) {
	project, userHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	for _, path := range []string{filepath.Join(userHome, ".codex", "auth.json"), filepath.Join(userHome, ".grok", "auth.json")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pipeline := Pipeline{ProjectRoot: project, Lock: lockfile.EmptyForProject(project), Mode: mode.ImplicitEffective()}
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	first, err := codex.CurrentHome(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(first)) })
	if err := os.WriteFile(filepath.Join(first, "running-session.marker"), []byte("still here"), 0o600); err != nil {
		t.Fatal(err)
	}
	lease := flock.New(first + ".lease")
	if err := lease.RLock(); err != nil {
		t.Fatal(err)
	}
	defer lease.Unlock()
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	second, err := codex.CurrentHome(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("Codex home did not rotate")
	}
	if data, err := os.ReadFile(filepath.Join(first, "running-session.marker")); err != nil || string(data) != "still here" {
		t.Fatalf("old generation was modified: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(second, "config.toml")); err != nil {
		t.Fatalf("new generation is incomplete: %v", err)
	}
	if err := pipeline.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("unused retired generation was not removed: %v", err)
	}

	otherMode, err := mode.NewEffective("design", mode.ImplicitDefault(), nil)
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Mode = otherMode
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	other, err := codex.CurrentHome(project, "design")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(other)) })
	if other == first || other == second {
		t.Fatalf("modes share a Codex generation: %s", other)
	}
}

func TestFailedCodexRebuildKeepsPublishedGeneration(t *testing.T) {
	project, userHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	nativeConfig := filepath.Join(userHome, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(nativeConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{ProjectRoot: project, Lock: lockfile.EmptyForProject(project), Mode: mode.ImplicitEffective()}
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	first, err := codex.CurrentHome(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(first)) })
	if err := os.WriteFile(nativeConfig, []byte("[invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Rebuild(); err == nil {
		t.Fatal("invalid native Codex config did not fail the rebuild")
	}
	current, err := codex.CurrentHome(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	if current != first {
		t.Fatalf("failed rebuild replaced %s with %s", first, current)
	}
	if err := codex.New().Verify(base.StageContext{ProjectRoot: project, Mode: mode.ImplicitEffective()}); err != nil {
		t.Fatalf("published home was damaged by failed rebuild: %v", err)
	}
}

func TestFailedMCPRebuildRestoresEveryPublishedRoot(t *testing.T) {
	project, userHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	pipeline := Pipeline{ProjectRoot: project, Lock: lockfile.EmptyForProject(project), Mode: mode.ImplicitEffective()}
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	ctx := pipeline.context()
	var roots []string
	for _, candidate := range registry.All() {
		root, err := candidate.StagedRoot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, root)
	}
	grokHome, err := paths.StagingGrokHomeDirForMode(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	roots = append(roots, grokHome)
	for _, root := range roots {
		if err := os.WriteFile(filepath.Join(root, "prior-state.marker"), []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifestPath, err := paths.StagedManifestPath(project, "default")
	if err != nil {
		t.Fatal(err)
	}
	manifestBefore, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(project, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".agents", "mcp.json"), []byte(`{"mcpServers":{"example":{"command":"node"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	nativeConfig := filepath.Join(userHome, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(nativeConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nativeConfig, []byte("mcp_servers = \"not-a-table\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Rebuild(); err == nil {
		t.Fatal("bad MCP input did not fail rebuild")
	}
	for _, root := range roots {
		data, err := os.ReadFile(filepath.Join(root, "prior-state.marker"))
		if err != nil || string(data) != "preserve" {
			t.Fatalf("root %s damaged: %s %v", root, data, err)
		}
	}
	manifestAfter, err := os.ReadFile(manifestPath)
	if err != nil || string(manifestBefore) != string(manifestAfter) {
		t.Fatalf("prior staged manifest changed: %v", err)
	}
	if err := pipeline.Verify(); err != nil {
		t.Fatalf("published prior staging no longer usable: %v", err)
	}
}

func TestRebuildRollbackPreservesLinksAndDeduplicatesNestedPaths(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "tree")
	outside := filepath.Join(root, "outside-secret")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(original, "auth")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	backups, err := backupRebuildPaths([]string{original, filepath.Join(original, "auth"), original}, filepath.Join(root, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("nested backups = %d", len(backups))
	}
	os.Mkdir(original, 0o700)
	os.WriteFile(filepath.Join(original, "new"), []byte("partial"), 0o600)
	if err := finishRebuildBackups(backups, false); err != nil {
		t.Fatal(err)
	}
	if link, err := os.Readlink(filepath.Join(original, "auth")); err != nil || link != outside {
		t.Fatalf("auth link changed %s %v", link, err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "unchanged" {
		t.Fatal("rollback followed or changed auth link")
	}
	if _, err := os.Stat(filepath.Join(original, "new")); !os.IsNotExist(err) {
		t.Fatal("partial output survived rollback")
	}
}

func TestInterruptedRebuildRestoresOriginalsAndRemovesPartialNewRoots(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old")
	newRoot := filepath.Join(root, "new")
	journalPath := filepath.Join(root, "journal.json")
	if err := os.Mkdir(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "state"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	managed := normalizeRebuildPaths([]string{old, newRoot})
	if _, err := backupRebuildPaths(managed, journalPath); err != nil {
		t.Fatal(err)
	}
	// Simulate termination: there is no defer/rollback, and a new tree is partial.
	os.Mkdir(old, 0o700)
	os.WriteFile(filepath.Join(old, "state"), []byte("partial"), 0o600)
	os.Mkdir(newRoot, 0o700)
	if err := recoverRebuildJournal(journalPath, managed); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(old, "state")); err != nil || string(data) != "original" {
		t.Fatalf("prior tree not restored: %s %v", data, err)
	}
	if _, err := os.Stat(newRoot); !os.IsNotExist(err) {
		t.Fatal("partial previously absent root remains")
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatal("completed recovery journal remains")
	}
}

func TestRebuildRecoveryCrashBeforeRenamePreservesUntouchedOriginal(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "original")
	journalPath := filepath.Join(root, "journal.json")
	os.Mkdir(path, 0o700)
	os.WriteFile(filepath.Join(path, "state"), []byte("untouched"), 0o600)
	backupDir, err := os.MkdirTemp(root, ".agentpack-rebuild-backup-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRebuildJournal(journalPath, rebuildJournal{SchemaVersion: 1, Backups: []rebuildBackup{{Path: path, Directory: backupDir, Existed: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := recoverRebuildJournal(journalPath, []string{path}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(path, "state")); err != nil || string(data) != "untouched" {
		t.Fatal("untouched original deleted")
	}
}

func TestCommittedRebuildRecoveryKeepsNewTrees(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "original")
	journalPath := filepath.Join(root, "journal.json")
	os.Mkdir(path, 0o700)
	os.WriteFile(filepath.Join(path, "state"), []byte("old"), 0o600)
	backups, err := backupRebuildPaths([]string{path}, journalPath)
	if err != nil {
		t.Fatal(err)
	}
	os.Mkdir(path, 0o700)
	os.WriteFile(filepath.Join(path, "state"), []byte("committed"), 0o600)
	if err := writeRebuildJournal(journalPath, rebuildJournal{SchemaVersion: 1, Committed: true, Backups: backups}); err != nil {
		t.Fatal(err)
	}
	if err := recoverRebuildJournal(journalPath, []string{path}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(path, "state")); err != nil || string(data) != "committed" {
		t.Fatal("committed tree rolled back")
	}
}

func TestRebuildRecoveryRejectsUnknownPathsAndSymlinkBackupDirectories(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	private := filepath.Join(root, "native-secret")
	journalPath := filepath.Join(root, "journal.json")
	os.WriteFile(private, []byte("unchanged"), 0o600)
	for _, journal := range []rebuildJournal{
		{SchemaVersion: 1, Backups: []rebuildBackup{{Path: private}}},
		{SchemaVersion: 2, Backups: []rebuildBackup{{Path: managed}}},
	} {
		if err := writeRebuildJournal(journalPath, journal); err != nil {
			t.Fatal(err)
		}
		if err := recoverRebuildJournal(journalPath, []string{managed}); err == nil {
			t.Fatal("unsafe recovery journal accepted")
		}
	}
	backupPath := filepath.Join(root, ".agentpack-rebuild-backup-link")
	if err := os.Symlink(root, backupPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := writeRebuildJournal(journalPath, rebuildJournal{SchemaVersion: 1, Backups: []rebuildBackup{{Path: managed, Directory: backupPath, Existed: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := recoverRebuildJournal(journalPath, []string{managed}); err == nil {
		t.Fatal("symlink backup accepted")
	}
	if data, err := os.ReadFile(private); err != nil || string(data) != "unchanged" {
		t.Fatal("native secret touched during rejected recovery")
	}
}

func TestPipelineDetectsAndRecoversInterruptedRebuildBeforePreReset(t *testing.T) {
	project, userHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	pipeline := Pipeline{ProjectRoot: project, Lock: lockfile.EmptyForProject(project), Mode: mode.ImplicitEffective()}
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatal(err)
	}
	managed, err := pipeline.managedRebuildPaths(pipeline.context(), registry.All())
	if err != nil {
		t.Fatal(err)
	}
	journalPath, err := pipeline.rebuildJournalPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backupRebuildPaths(managed, journalPath); err != nil {
		t.Fatal(err)
	}
	if pending, err := pipeline.RebuildPending(); err != nil || !pending {
		t.Fatalf("interruption not visible: %v %v", pending, err)
	}
	if _, err := pipeline.Rebuild(); err != nil {
		t.Fatalf("rebuild failed to recover: %v", err)
	}
	if pending, err := pipeline.RebuildPending(); err != nil || pending {
		t.Fatalf("recovery not completed: %v %v", pending, err)
	}
	if err := pipeline.Verify(); err != nil {
		t.Fatal(err)
	}
}
