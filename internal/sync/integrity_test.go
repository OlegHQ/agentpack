package sync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/cache"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/paths"
)

const (
	commitOne = "1111111111111111111111111111111111111111"
	commitTwo = "2222222222222222222222222222222222222222"
	demoKey   = "github.com/acme/skills/demo"
	// Already in the shape staging renders, so staged bytes equal source bytes.
	skillOne = "---\nname: demo\ndescription: Demo one\n---\n\n# Demo one\n"
	skillTwo = "---\nname: demo\ndescription: Demo two\n---\n\n# Demo two\n"
)

func TestLockRecordsContentHashAndSyncVerifiesIt(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.manifest(demoKey, commitOne)
	lock := fixture.lock()
	entry := fixture.cacheEntry(lock)
	digest, err := cache.TreeDigest(entry)
	if err != nil {
		t.Fatal(err)
	}
	if lock.Packages[0].ContentHash != digest || !strings.HasPrefix(digest, lockfile.ContentHashPrefix) {
		t.Fatalf("content_hash = %q, cache digest = %q", lock.Packages[0].ContentHash, digest)
	}
	fixture.sync(SyncOptions{})
	if fixture.staged() != skillOne || len(fixture.notices) != 0 || fixture.tarballRequests() != 1 {
		t.Fatalf("staged=%q notices=%v requests=%v", fixture.staged(), fixture.notices, fixture.requests)
	}
}

func TestLockWithoutContentHashesStagesWithNoticeUntilRelocked(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.manifest(demoKey, commitOne)
	fixture.lock()
	fixture.stripContentHashes()
	before := fixture.lockText()

	fixture.sync(SyncOptions{})
	if fixture.staged() != skillOne || !fixture.noticed("1 package(s) in pack.lock have no content hash") {
		t.Fatalf("staged=%q notices=%v", fixture.staged(), fixture.notices)
	}
	if after := fixture.lockText(); after != before || !strings.HasPrefix(after, "lockfile_version = 2\n") {
		t.Fatalf("sync rewrote a lock it could not verify:\n%s", after)
	}
	fixture.notices = nil
	for attempt, wantSkipped := range []bool{false, true} {
		if _, skipped, err := fixture.service.SyncForLaunch(context.Background(), fixture.project, "", base.Claude); err != nil || skipped != wantSkipped {
			t.Fatalf("launch %d: skipped=%v err=%v", attempt, skipped, err)
		}
	}
	if count := strings.Count(strings.Join(fixture.notices, "\n"), "no content hash"); count != 2 {
		t.Fatalf("launches warned %d time(s): %v", count, fixture.notices)
	}

	t.Setenv(RequireVerifiedEnv, "1")
	if _, err := fixture.service.Sync(context.Background(), fixture.project, SyncOptions{}); err == nil || !strings.Contains(err.Error(), demoKey) || !strings.Contains(err.Error(), "agentpack lock") {
		t.Fatalf("strict sync error = %v", err)
	}
	if _, _, err := fixture.service.SyncForLaunch(context.Background(), fixture.project, "", base.Claude); err == nil {
		t.Fatal("strict launch accepted an unverified lock")
	}
	t.Setenv(RequireVerifiedEnv, "")

	// A hash is only ever recorded from bytes fetched for it, so re-locking
	// downloads the pinned commit again even though the cache is warm. Here the
	// warm cache had been edited, and the fetch replaces it.
	appendFile(t, filepath.Join(fixture.cacheEntry(fixture.loadLock()), "SKILL.md"), "TAMPER\n")
	fixture.notices, fixture.requests = nil, nil
	lock := fixture.lock()
	if lock.Packages[0].ContentHash == "" || fixture.tarballRequests() != 1 {
		t.Fatalf("relock: hash=%q requests=%v", lock.Packages[0].ContentHash, fixture.requests)
	}
	if !fixture.noticed("recorded content hashes for 1 package(s)") || !fixture.noticed("differed from commit "+commitOne) {
		t.Fatalf("relock notices = %v", fixture.notices)
	}
	fixture.sync(SyncOptions{})
	if fixture.staged() != skillOne || !strings.HasPrefix(fixture.lockText(), "lockfile_version = 3\n") {
		t.Fatalf("staged=%q lock:\n%s", fixture.staged(), fixture.lockText())
	}
}

func TestTamperedCacheIsRefusedBySyncVerifyOnlyAndLaunchThenRepaired(t *testing.T) {
	tamperings := map[string]func(t *testing.T, entry string){
		"edited": func(t *testing.T, entry string) { appendFile(t, filepath.Join(entry, "SKILL.md"), "TAMPER\n") },
		"added": func(t *testing.T, entry string) {
			writeIntegrityFile(t, filepath.Join(entry, "scripts", "extra.sh"), "TAMPER")
		},
		"deleted": func(t *testing.T, entry string) {
			if err := os.Remove(filepath.Join(entry, "scripts", "run.sh")); err != nil {
				t.Fatal(err)
			}
		},
		"executable": func(t *testing.T, entry string) {
			if err := os.Chmod(filepath.Join(entry, "scripts", "run.sh"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, entry string) {
			target := filepath.Join(t.TempDir(), "SKILL.md")
			writeIntegrityFile(t, target, skillOne+"TAMPER\n")
			if err := os.Remove(filepath.Join(entry, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(entry, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, tamper := range tamperings {
		t.Run(name, func(t *testing.T) {
			if (name == "executable" || name == "symlink") && runtime.GOOS == "windows" {
				t.Skip("POSIX file modes and links")
			}
			fixture := newIntegrityFixture(t)
			fixture.manifest(demoKey, commitOne)
			lock := fixture.lock()
			entry := fixture.cacheEntry(lock)
			for attempt, wantSkipped := range []bool{false, true} {
				if _, skipped, err := fixture.service.SyncForLaunch(context.Background(), fixture.project, "", base.Claude); err != nil || skipped != wantSkipped {
					t.Fatalf("launch %d before tampering: skipped=%v err=%v", attempt, skipped, err)
				}
			}
			stagedBefore := fixture.stagedTree()
			tamper(t, entry)
			tampered, _ := cache.TreeDigest(entry)

			attempts := map[string]func() error{
				"sync": func() error {
					_, err := fixture.service.Sync(context.Background(), fixture.project, SyncOptions{})
					return err
				},
				"sync --verify-only": func() error {
					_, err := fixture.service.Sync(context.Background(), fixture.project, SyncOptions{VerifyOnly: true})
					return err
				},
				"launch": func() error {
					_, _, err := fixture.service.SyncForLaunch(context.Background(), fixture.project, "", base.Claude)
					return err
				},
			}
			for command, run := range attempts {
				err := run()
				var mismatch *cache.IntegrityError
				if !errors.As(err, &mismatch) || mismatch.Fetched {
					t.Fatalf("%s error = %v", command, err)
				}
				for _, part := range []string{demoKey, lock.Packages[0].ContentHash, tampered, entry} {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("%s error does not name %q:\n%v", command, part, err)
					}
				}
				if now, _ := cache.TreeDigest(entry); now != tampered {
					t.Fatalf("%s changed the tampered cache entry", command)
				}
				if staged := fixture.stagedTree(); staged != stagedBefore {
					t.Fatalf("%s changed staging:\n%s", command, staged)
				}
			}
			if fixture.tarballRequests() != 1 {
				t.Fatalf("a refused sync fetched again: %v", fixture.requests)
			}

			fixture.sync(SyncOptions{Repair: true})
			if now, _ := cache.TreeDigest(entry); now != lock.Packages[0].ContentHash {
				t.Fatal("repair left a cache entry that does not match the lock")
			}
			if !fixture.noticed("repaired "+demoKey+": re-fetched commit "+commitOne) || !fixture.noticed(tampered) || fixture.tarballRequests() != 2 {
				t.Fatalf("repair notices=%v requests=%v", fixture.notices, fixture.requests)
			}
			if staged := fixture.stagedTree(); staged != stagedBefore {
				t.Fatalf("staging after repair:\n%s", staged)
			}
			fixture.sync(SyncOptions{VerifyOnly: true})
		})
	}
}

func TestLaunchFastPathNeverStagesAnEditItCannotSee(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.manifest(demoKey, commitOne)
	lock := fixture.lock()
	launch := func() (bool, error) {
		_, skipped, err := fixture.service.SyncForLaunch(context.Background(), fixture.project, "", base.Claude)
		return skipped, err
	}
	if skipped, err := launch(); err != nil || skipped {
		t.Fatalf("first launch: skipped=%v err=%v", skipped, err)
	}
	// Same size, original modification time: invisible to file metadata.
	path := filepath.Join(fixture.cacheEntry(lock), "SKILL.md")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, path, strings.Replace(skillOne, "# Demo one", "# Dem0 one", 1))
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	// The fast path skips staging, so the harness still gets the tree built
	// from the verified cache.
	if skipped, err := launch(); err != nil || !skipped || fixture.staged() != skillOne {
		t.Fatalf("fast launch: skipped=%v err=%v staged=%q", skipped, err, fixture.staged())
	}
	// Anything that would stage from the cache hashes it first.
	var mismatch *cache.IntegrityError
	if _, err := fixture.service.Sync(context.Background(), fixture.project, SyncOptions{}); !errors.As(err, &mismatch) {
		t.Fatalf("sync error = %v", err)
	}
	t.Setenv(FullVerifyEnv, "1")
	if _, err := launch(); !errors.As(err, &mismatch) {
		t.Fatalf("launch with %s error = %v", FullVerifyEnv, err)
	}
	if fixture.staged() != skillOne {
		t.Fatalf("staged = %q", fixture.staged())
	}
}

func TestRepairRefusesAFetchThatContradictsTheLock(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.manifest(demoKey, commitOne)
	lock := fixture.lock()
	fixture.sync(SyncOptions{})
	entry := fixture.cacheEntry(lock)
	appendFile(t, filepath.Join(entry, "SKILL.md"), "TAMPER\n")
	tampered, _ := cache.TreeDigest(entry)
	fixture.upstream.tarballs[commitOne] = map[string]string{"demo/SKILL.md": "# Rewritten\n"}
	_, err := fixture.service.Sync(context.Background(), fixture.project, SyncOptions{Repair: true})
	var mismatch *cache.IntegrityError
	if !errors.As(err, &mismatch) || !mismatch.Fetched {
		t.Fatalf("repair error = %v", err)
	}
	if now, _ := cache.TreeDigest(entry); now != tampered {
		t.Fatal("repair replaced the cache entry with content the lock does not pin")
	}
	if fixture.staged() != skillOne {
		t.Fatalf("staged = %q", fixture.staged())
	}
}

func TestTamperedStagedFileIsRestoredFromVerifiedCache(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.manifest(demoKey, commitOne)
	fixture.lock()
	fixture.sync(SyncOptions{})
	appendFile(t, fixture.stagedPath(), "TAMPER\n")
	fixture.sync(SyncOptions{})
	if fixture.staged() != skillOne || fixture.tarballRequests() != 1 {
		t.Fatalf("staged=%q requests=%v", fixture.staged(), fixture.requests)
	}
}

func TestRewrittenHistoryKeepsPinnedContentOrFailsClosed(t *testing.T) {
	t.Run("warm cache keeps the pinned content", func(t *testing.T) {
		fixture := newIntegrityFixture(t)
		fixture.manifest(demoKey, commitOne)
		fixture.lock()
		delete(fixture.upstream.tarballs, commitOne)
		fixture.sync(SyncOptions{})
		if fixture.staged() != skillOne || fixture.tarballRequests() != 1 {
			t.Fatalf("staged=%q requests=%v", fixture.staged(), fixture.requests)
		}
	})
	t.Run("clean cache fails when the commit is gone", func(t *testing.T) {
		fixture := newIntegrityFixture(t)
		fixture.manifest(demoKey, commitOne)
		lock := fixture.lock()
		delete(fixture.upstream.tarballs, commitOne)
		fixture.wipeCache()
		if _, err := fixture.service.Sync(context.Background(), fixture.project, SyncOptions{}); err == nil {
			t.Fatal("sync succeeded without the pinned commit")
		}
		if _, err := os.Stat(fixture.cacheEntry(lock)); !os.IsNotExist(err) {
			t.Fatalf("cache entry after failed fetch: %v", err)
		}
	})
	t.Run("clean cache refuses different bytes for the pinned commit", func(t *testing.T) {
		fixture := newIntegrityFixture(t)
		fixture.manifest(demoKey, commitOne)
		lock := fixture.lock()
		fixture.upstream.tarballs[commitOne] = map[string]string{"demo/SKILL.md": "# Rewritten\n", "demo/scripts/run.sh": "echo one\n"}
		fixture.wipeCache()
		for _, options := range []SyncOptions{{}, {Repair: true}} {
			_, err := fixture.service.Sync(context.Background(), fixture.project, options)
			var mismatch *cache.IntegrityError
			if !errors.As(err, &mismatch) || !mismatch.Fetched || !strings.Contains(err.Error(), lock.Packages[0].ContentHash) {
				t.Fatalf("sync %+v error = %v", options, err)
			}
			if _, err := os.Stat(fixture.cacheEntry(lock)); !os.IsNotExist(err) {
				t.Fatalf("content that contradicts the lock was cached: %v", err)
			}
		}
		if _, err := os.Stat(fixture.stagedPath()); !os.IsNotExist(err) {
			t.Fatalf("content that contradicts the lock was staged: %v", err)
		}
		if after := fixture.loadLock(); after.Packages[0].ContentHash != lock.Packages[0].ContentHash {
			t.Fatal("the lock was rewritten to accept the new content")
		}
	})
}

func TestMovedBranchKeepsPinnedContent(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.upstream.refs["main"] = commitOne
	fixture.manifest(demoKey, "main")
	lock := fixture.lock()
	if lock.Packages[0].Commit != commitOne {
		t.Fatalf("locked commit = %s", lock.Packages[0].Commit)
	}
	fixture.upstream.refs["main"] = commitTwo
	fixture.wipeCache()
	fixture.requests = nil
	fixture.sync(SyncOptions{})
	after := fixture.loadLock()
	if after.Packages[0].Commit != commitOne || after.Packages[0].ContentHash != lock.Packages[0].ContentHash || fixture.staged() != skillOne {
		t.Fatalf("after branch move: %#v staged=%q", after.Packages[0], fixture.staged())
	}
	if strings.Join(fixture.requests, " ") != "/acme/skills/tar.gz/"+commitOne {
		t.Fatalf("requests = %v", fixture.requests)
	}
}

func TestLockPinsMCPServersAndStagesTheExactNPMVersion(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.upstream.tarballs[commitTwo] = map[string]string{
		"browser/.claude-plugin/plugin.json": `{"name":"browser","version":"1.0.0"}`,
		"browser/mcp.json":                   `{"mcpServers":{"playwright":{"command":"npx","args":["@playwright/mcp@latest"]}}}`,
	}
	fixture.writeManifest(`
[dependencies]
"github.com/acme/skills/browser" = "` + commitTwo + `"

[mcp.servers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem@2025.1.0", "/srv"]
env = { TOKEN = "secret-value" }

[mcp.servers.linear]
url = "https://mcp.linear.app/mcp"

[mcp.servers.retrieval]
command = "uvx"
args = ["mcp-retrieval"]

[mcp.servers.local]
command = "/opt/tools/server"
`)
	lock := fixture.lock()
	got := make(map[string]string)
	for _, server := range lock.MCPServers {
		got[server.Name] = strings.Join([]string{server.Source, server.Launcher, server.Status, server.Package, server.Version, server.Integrity, server.Host}, "|")
	}
	want := map[string]string{
		"playwright": "plugin|npm|pinned|@playwright/mcp|0.0.41|sha512-playwright41|",
		"filesystem": "manifest|npm|pinned|@modelcontextprotocol/server-filesystem|2025.1.0|sha512-filesystem|",
		"linear":     "manifest|remote|unpinnable||||mcp.linear.app",
		"retrieval":  "manifest|pypi|unpinned|mcp-retrieval|||",
		"local":      "manifest|command|unpinnable||||",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("mcp_servers:\n got %v\nwant %v", got, want)
	}
	if text := fixture.lockText(); strings.Contains(text, "secret-value") || strings.Contains(text, "/srv") || !strings.Contains(text, "registry = 'https://registry.test'") {
		t.Fatalf("lock text:\n%s", text)
	}
	if !fixture.noticed("MCP server(s) retrieval are recorded as unpinned") {
		t.Fatalf("notices = %v", fixture.notices)
	}

	fixture.notices = nil
	fixture.sync(SyncOptions{})
	staged := fixture.stagedMCP()
	for _, part := range []string{`"@playwright/mcp@0.0.41"`, `"@modelcontextprotocol/server-filesystem@2025.1.0"`, `"mcp-retrieval"`, `"/srv"`, `"secret-value"`} {
		if !strings.Contains(staged, part) {
			t.Errorf("staged MCP config lacks %s:\n%s", part, staged)
		}
	}
	if strings.Contains(staged, "@latest") || len(fixture.notices) != 0 {
		t.Fatalf("staged=%s notices=%v", staged, fixture.notices)
	}

	// A newer release does not move the pin until the lock is refreshed, and
	// an unchanged lock needs no registry at all.
	fixture.upstream.npm["/@playwright%2Fmcp/latest"] = `{"version":"0.0.42","dist":{"integrity":"sha512-playwright42"}}`
	fixture.upstream.registryDown = true
	if pinned, _ := fixture.lock().MCPServer("playwright"); pinned.Version != "0.0.41" {
		t.Fatalf("plain lock moved the pin to %s", pinned.Version)
	}
	fixture.upstream.registryDown = false
	refreshed, err := fixture.service.LockWithOptions(context.Background(), fixture.project, LockOptions{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if pinned, _ := refreshed.MCPServer("playwright"); pinned.Version != "0.0.42" || pinned.Integrity != "sha512-playwright42" {
		t.Fatalf("refreshed pin = %#v", pinned)
	}

	// The registry may not change what an already locked version contains.
	fixture.upstream.npm["/@playwright%2Fmcp/latest"] = `{"version":"0.0.42","dist":{"integrity":"sha512-republished"}}`
	before := fixture.lockText()
	if _, err := fixture.service.LockWithOptions(context.Background(), fixture.project, LockOptions{Refresh: true}); err == nil || !strings.Contains(err.Error(), "sha512-republished") || !strings.Contains(err.Error(), "sha512-playwright42") {
		t.Fatalf("republished version error = %v", err)
	}
	if fixture.lockText() != before {
		t.Fatal("a failed lock rewrote pack.lock")
	}
}

func TestUnreachableRegistryFailsTheLockUnlessUnpinnedIsAllowed(t *testing.T) {
	fixture := newIntegrityFixture(t)
	fixture.writeManifest("\n[dependencies]\n\n[mcp.servers.playwright]\ncommand = \"npx\"\nargs = [\"@playwright/mcp@latest\"]\n")
	if err := lockfile.Init(fixture.project, "demo", "0.0.1"); err != nil {
		t.Fatal(err)
	}
	before := fixture.lockText()

	// sync never asks a registry: the server runs as written, with a notice.
	fixture.upstream.registryDown = true
	fixture.sync(SyncOptions{})
	if !strings.Contains(fixture.stagedMCP(), `"@playwright/mcp@latest"`) || !fixture.noticed("MCP server(s) playwright have no current record in pack.lock") || fixture.lockText() != before {
		t.Fatalf("staged=%s notices=%v", fixture.stagedMCP(), fixture.notices)
	}

	_, err := fixture.service.LockWithOptions(context.Background(), fixture.project, LockOptions{})
	if err == nil || !strings.Contains(err.Error(), `cannot pin MCP server "playwright"`) || !strings.Contains(err.Error(), "--allow-unpinned-mcp") {
		t.Fatalf("lock error = %v", err)
	}
	if fixture.lockText() != before {
		t.Fatal("a failed lock rewrote pack.lock")
	}

	lock, err := fixture.service.LockWithOptions(context.Background(), fixture.project, LockOptions{AllowUnpinnedMCP: true})
	if err != nil {
		t.Fatal(err)
	}
	record, _ := lock.MCPServer("playwright")
	if record.Status != lockfile.MCPUnpinned || !record.AllowUnpinned || record.Version != "" || !strings.Contains(fixture.lockText(), "allow_unpinned = true") {
		t.Fatalf("record = %#v\n%s", record, fixture.lockText())
	}
	fixture.sync(SyncOptions{})
	if !strings.Contains(fixture.stagedMCP(), `"@playwright/mcp@latest"`) {
		t.Fatalf("staged = %s", fixture.stagedMCP())
	}
	// Once allowed, the record lets a later lock proceed offline too.
	if _, err := fixture.service.LockWithOptions(context.Background(), fixture.project, LockOptions{}); err != nil {
		t.Fatal(err)
	}

	fixture.upstream.registryDown = false
	lock = fixture.lock()
	if record, _ = lock.MCPServer("playwright"); record.Status != lockfile.MCPPinned || record.Version != "0.0.41" || record.AllowUnpinned {
		t.Fatalf("record after the registry returned = %#v", record)
	}

	// Editing the definition makes the record stale: it is not applied.
	fixture.writeManifest("\n[dependencies]\n\n[mcp.servers.playwright]\ncommand = \"npx\"\nargs = [\"@playwright/mcp@next\"]\n")
	fixture.notices = nil
	fixture.sync(SyncOptions{})
	if !strings.Contains(fixture.stagedMCP(), `"@playwright/mcp@next"`) || !fixture.noticed("playwright have no current record") {
		t.Fatalf("staged=%s notices=%v", fixture.stagedMCP(), fixture.notices)
	}
}

type integrityUpstream struct {
	tarballs     map[string]map[string]string
	refs         map[string]string
	npm          map[string]string
	registryDown bool
}

type integrityFixture struct {
	t        *testing.T
	project  string
	upstream *integrityUpstream
	service  Service
	notices  []string
	requests []string
}

func newIntegrityFixture(t *testing.T) *integrityFixture {
	t.Helper()
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("NPM_CONFIG_REGISTRY", "https://registry.test")
	t.Setenv(RequireVerifiedEnv, "")
	t.Setenv(FullVerifyEnv, "")
	for _, path := range []string{filepath.Join(home, ".codex", "auth.json"), filepath.Join(home, ".grok", "auth.json")} {
		writeIntegrityFile(t, path, "{}")
	}
	fixture := &integrityFixture{t: t, project: project, upstream: &integrityUpstream{
		tarballs: map[string]map[string]string{
			commitOne: {"demo/SKILL.md": skillOne, "demo/scripts/run.sh": "echo one\n"},
			commitTwo: {"demo/SKILL.md": skillTwo, "demo/scripts/run.sh": "echo two\n"},
		},
		refs: map[string]string{},
		npm: map[string]string{
			"/@playwright%2Fmcp/latest":                           `{"version":"0.0.41","dist":{"integrity":"sha512-playwright41"}}`,
			"/@playwright%2Fmcp/next":                             `{"version":"0.1.0-next.1","dist":{"integrity":"sha512-next"}}`,
			"/@modelcontextprotocol%2Fserver-filesystem/2025.1.0": `{"version":"2025.1.0","dist":{"integrity":"sha512-filesystem"}}`,
		},
	}}
	fixture.service = Service{
		Client: &http.Client{Transport: integrityTransport(fixture.serve)},
		Notify: func(message string) { fixture.notices = append(fixture.notices, message) },
	}
	return fixture
}

// serve stands in for codeload, the GitHub REST API, and an npm registry. A
// missing commit answers 410 so the client fails without trying the Git
// protocol against the real github.com.
func (fixture *integrityFixture) serve(request *http.Request) (int, []byte) {
	path := request.URL.EscapedPath()
	if request.URL.Host == "registry.test" {
		if fixture.upstream.registryDown {
			return 0, nil
		}
		fixture.requests = append(fixture.requests, "npm:"+path)
		if body, found := fixture.upstream.npm[path]; found {
			return http.StatusOK, []byte(body)
		}
		return http.StatusNotFound, nil
	}
	fixture.requests = append(fixture.requests, path)
	if _, ref, found := strings.Cut(path, "/commits/"); found {
		if sha := fixture.upstream.refs[ref]; sha != "" {
			return http.StatusOK, []byte(`{"sha":"` + sha + `"}`)
		}
		return http.StatusTeapot, nil
	}
	if _, sha, found := strings.Cut(path, "/tar.gz/"); found {
		if files, exists := fixture.upstream.tarballs[sha]; exists {
			return http.StatusOK, integrityTarball(fixture.t, "skills-"+sha, files)
		}
	}
	return http.StatusGone, nil
}

func (fixture *integrityFixture) manifest(module, ref string) {
	fixture.writeManifest("\n[dependencies]\n\"" + module + "\" = \"" + ref + "\"\n")
}

func (fixture *integrityFixture) writeManifest(body string) {
	writeIntegrityFile(fixture.t, filepath.Join(fixture.project, "agentpack.toml"), "name = \"demo\"\nversion = \"0.0.1\"\n"+body)
}

func (fixture *integrityFixture) lock() lockfile.PackLock {
	fixture.t.Helper()
	lock, err := fixture.service.Lock(context.Background(), fixture.project, false)
	if err != nil {
		fixture.t.Fatalf("lock: %v", err)
	}
	return lock
}

func (fixture *integrityFixture) sync(options SyncOptions) {
	fixture.t.Helper()
	if _, err := fixture.service.Sync(context.Background(), fixture.project, options); err != nil {
		fixture.t.Fatalf("sync %+v: %v", options, err)
	}
}

func (fixture *integrityFixture) loadLock() lockfile.PackLock {
	fixture.t.Helper()
	lock, err := lockfile.Load(fixture.project)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return lock
}

func (fixture *integrityFixture) lockText() string {
	fixture.t.Helper()
	data, err := os.ReadFile(paths.LockPath(fixture.project))
	if err != nil {
		fixture.t.Fatal(err)
	}
	return string(data)
}

// stripContentHashes turns the lock into what a binary without content hashes wrote.
func (fixture *integrityFixture) stripContentHashes() {
	fixture.t.Helper()
	lock := fixture.loadLock()
	for index := range lock.Packages {
		lock.Packages[index].ContentHash = ""
	}
	if err := lock.Save(fixture.project); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *integrityFixture) cacheEntry(lock lockfile.PackLock) string {
	fixture.t.Helper()
	entry, err := cache.EntryDir(lock.Packages[0].CacheKey)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return entry
}

func (fixture *integrityFixture) wipeCache() {
	fixture.t.Helper()
	root, err := paths.CacheDir()
	if err != nil {
		fixture.t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *integrityFixture) bundle() string {
	fixture.t.Helper()
	plugins, err := paths.StagingPluginsDirForMode(fixture.project, "default")
	if err != nil {
		fixture.t.Fatal(err)
	}
	return filepath.Join(plugins, paths.StagedAgentpackBundleName)
}

func (fixture *integrityFixture) stagedPath() string {
	return filepath.Join(fixture.bundle(), "skills", "demo", "SKILL.md")
}

func (fixture *integrityFixture) staged() string {
	data, _ := os.ReadFile(fixture.stagedPath())
	return string(data)
}

func (fixture *integrityFixture) stagedMCP() string {
	data, _ := os.ReadFile(filepath.Join(fixture.bundle(), ".mcp.json"))
	return string(data)
}

// stagedTree lists every file staged for the skill with its content.
func (fixture *integrityFixture) stagedTree() string {
	fixture.t.Helper()
	root := filepath.Join(fixture.bundle(), "skills", "demo")
	var tree strings.Builder
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, _ := filepath.Rel(root, path)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		fmt.Fprintf(&tree, "%s %s %q\n", filepath.ToSlash(relative), info.Mode(), data)
		return err
	})
	if err != nil {
		fixture.t.Fatal(err)
	}
	return tree.String()
}

func (fixture *integrityFixture) noticed(part string) bool {
	return strings.Contains(strings.Join(fixture.notices, "\n"), part)
}

func (fixture *integrityFixture) tarballRequests() int {
	count := 0
	for _, request := range fixture.requests {
		if strings.Contains(request, "/tar.gz/") {
			count++
		}
	}
	return count
}

type integrityTransport func(*http.Request) (int, []byte)

func (transport integrityTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	status, body := transport(request)
	if status == 0 {
		return nil, errors.New("connection refused")
	}
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
}

func integrityTarball(t *testing.T, prefix string, files map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, body := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: prefix + "/" + name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func writeIntegrityFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, body string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
