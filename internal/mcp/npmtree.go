package mcp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/olegpustovit/agentpack/internal/lockfile"
)

// TreeDigestPrefix names the digest of a locked npm tree.
const TreeDigestPrefix = "sha256-npm-tree-v1:"

// ErrNoNPM reports that the npm CLI, which resolves a tree at lock time, is
// not on PATH. The server then stays pinned at its top-level version only.
var ErrNoNPM = errors.New("npm is not on PATH")

// TreeDigest is a digest of every package a tree installs: path, version and
// integrity, in path order. Resolved URLs and platform constraints describe
// where and when a package is fetched, not what it is, so they are not part of it.
func TreeDigest(tree []lockfile.NPMTreeEntry) string {
	entries := append([]lockfile.NPMTreeEntry(nil), tree...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	hasher := sha256.New()
	for _, entry := range entries {
		fmt.Fprintf(hasher, "%s\x00%s\x00%s\n", entry.Path, entry.Version, entry.Integrity)
	}
	return TreeDigestPrefix + hex.EncodeToString(hasher.Sum(nil))
}

// ResolveTree resolves a server's npm tree at lock time. Tests replace it so
// that they never start npm against their stand-in registries.
var ResolveTree = ResolveNPMTree

// ResolveNPMTree asks npm which packages it would install for name@version,
// without running any package script, and returns them with the executable
// that npx would start. The root package must have the integrity the lock
// already records.
func ResolveNPMTree(ctx context.Context, registry, name, version, integrity string) ([]lockfile.NPMTreeEntry, string, error) {
	npm, err := exec.LookPath("npm")
	if err != nil {
		return nil, "", ErrNoNPM
	}
	dir, err := os.MkdirTemp("", "agentpack-npm-tree-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	manifest, _ := json.Marshal(map[string]any{"name": "agentpack-mcp-tree", "private": true, "dependencies": map[string]string{name: version}})
	if err := os.WriteFile(filepath.Join(dir, "package.json"), manifest, 0o644); err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, npm, "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund",
		"--registry", registry)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		return nil, "", fmt.Errorf("npm could not resolve the dependencies of %s@%s: %v: %s", name, version, err, strings.TrimSpace(lastLines(string(output), 5)))
	}
	data, err := os.ReadFile(filepath.Join(dir, "package-lock.json"))
	if err != nil {
		return nil, "", err
	}
	return parseNPMLock(data, name, version, integrity)
}

type npmLockPackage struct {
	Version   string          `json:"version"`
	Resolved  string          `json:"resolved"`
	Integrity string          `json:"integrity"`
	Optional  bool            `json:"optional"`
	Dev       bool            `json:"dev"`
	Link      bool            `json:"link"`
	OS        []string        `json:"os"`
	CPU       []string        `json:"cpu"`
	Bin       json.RawMessage `json:"bin"`
}

func parseNPMLock(data []byte, name, version, integrity string) ([]lockfile.NPMTreeEntry, string, error) {
	var lock struct {
		LockfileVersion int                       `json:"lockfileVersion"`
		Packages        map[string]npmLockPackage `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, "", fmt.Errorf("unreadable package-lock.json: %w", err)
	}
	if lock.LockfileVersion < 2 || lock.Packages == nil {
		return nil, "", fmt.Errorf("package-lock.json version %d has no packages map; npm 7 or newer is needed", lock.LockfileVersion)
	}
	rootPath := "node_modules/" + name
	root, found := lock.Packages[rootPath]
	if !found || root.Version != version {
		return nil, "", fmt.Errorf("npm resolved %s to a different version than %s", name, version)
	}
	if integrity != "" && root.Integrity != integrity {
		return nil, "", fmt.Errorf("npm reports integrity %s for %s@%s, but the lock records %s", root.Integrity, name, version, integrity)
	}
	var tree []lockfile.NPMTreeEntry
	for key, pkg := range lock.Packages {
		if key == "" || pkg.Dev {
			continue
		}
		if pkg.Link {
			return nil, "", fmt.Errorf("%s is a link in the resolved tree; only registry packages can be locked", key)
		}
		if !strings.HasPrefix(key, "node_modules/") || strings.Contains(key, "..") {
			return nil, "", fmt.Errorf("unexpected package path %q in the resolved tree", key)
		}
		if pkg.Integrity == "" || pkg.Version == "" {
			return nil, "", fmt.Errorf("%s has no version or integrity in the resolved tree", key)
		}
		tree = append(tree, lockfile.NPMTreeEntry{Path: key, Version: pkg.Version, Integrity: pkg.Integrity, Resolved: pkg.Resolved,
			Optional: pkg.Optional, OS: pkg.OS, CPU: pkg.CPU})
	}
	sort.Slice(tree, func(i, j int) bool { return tree[i].Path < tree[j].Path })
	bin, err := npxBin(name, root.Bin)
	if err != nil {
		return nil, "", err
	}
	return tree, bin, nil
}

// npxBin picks the executable npx runs for a package: its only bin, or the
// bin named after the package without its scope.
func npxBin(name string, raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("%s declares no executable", name)
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return cleanBin(single)
	}
	var bins map[string]string
	if err := json.Unmarshal(raw, &bins); err != nil {
		return "", fmt.Errorf("%s has an unreadable bin field", name)
	}
	if len(bins) == 1 {
		for _, target := range bins {
			return cleanBin(target)
		}
	}
	base := name
	if index := strings.LastIndex(base, "/"); index >= 0 {
		base = base[index+1:]
	}
	if target, ok := bins[base]; ok {
		return cleanBin(target)
	}
	return "", fmt.Errorf("%s has %d executables and none is named %s; npx would not know which to run either", name, len(bins), base)
}

func cleanBin(target string) (string, error) {
	cleaned := path.Clean(strings.TrimPrefix(target, "./"))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", fmt.Errorf("executable path %q leaves its package", target)
	}
	return cleaned, nil
}

// TreeSpec is what `agentpack mcp-exec` needs to start a locked server.
type TreeSpec struct {
	Server     string                  `json:"server"`
	Package    string                  `json:"package"`
	Version    string                  `json:"version"`
	Registry   string                  `json:"registry"`
	Bin        string                  `json:"bin"`
	TreeDigest string                  `json:"tree_digest"`
	Tree       []lockfile.NPMTreeEntry `json:"tree"`
}

// SpecFor returns the spec of a pinned record whose tree is locked.
func SpecFor(record lockfile.MCPServer) (TreeSpec, bool) {
	if record.Status != lockfile.MCPPinned || record.Launcher != LauncherNPM || len(record.Tree) == 0 || record.Bin == "" {
		return TreeSpec{}, false
	}
	return TreeSpec{Server: record.Name, Package: record.Package, Version: record.Version, Registry: record.Registry,
		Bin: record.Bin, TreeDigest: record.TreeDigest, Tree: record.Tree}, true
}

// InstallTree makes sure the locked tree is unpacked under cacheRoot and
// returns the path of the server's executable. Every tarball is checked
// against its locked integrity before anything is written; no package script
// runs. A finished tree is marked and reused by later launches.
func InstallTree(ctx context.Context, client *http.Client, cacheRoot string, spec TreeSpec) (string, error) {
	if got := TreeDigest(spec.Tree); got != spec.TreeDigest {
		return "", fmt.Errorf("MCP server %q: the locked tree has digest %s, not %s as recorded; nothing was run", spec.Server, got, spec.TreeDigest)
	}
	key := strings.TrimPrefix(spec.TreeDigest, TreeDigestPrefix)
	root := filepath.Join(cacheRoot, key)
	executable := filepath.Join(root, filepath.FromSlash("node_modules/"+spec.Package), filepath.FromSlash(spec.Bin))
	marker := filepath.Join(root, ".agentpack-tree-complete")
	if installedIntact(root, marker, spec.TreeDigest) {
		return executable, nil
	}
	if client == nil {
		client = http.DefaultClient
	}
	staging, err := os.MkdirTemp(cacheRoot, ".install-"+key[:12]+"-")
	if err != nil {
		if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
			return "", err
		}
		if staging, err = os.MkdirTemp(cacheRoot, ".install-"+key[:12]+"-"); err != nil {
			return "", err
		}
	}
	defer os.RemoveAll(staging)
	for _, entry := range spec.Tree {
		if !platformMatches(entry) {
			if entry.Optional {
				continue
			}
			return "", fmt.Errorf("MCP server %q: %s@%s does not support this platform", spec.Server, entry.Path, entry.Version)
		}
		if err := fetchPackage(ctx, client, spec.Registry, entry, filepath.Join(staging, filepath.FromSlash(entry.Path))); err != nil {
			return "", fmt.Errorf("MCP server %q: %w; nothing was run", spec.Server, err)
		}
	}
	files, err := filesDigest(staging)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staging, ".agentpack-tree-complete"), []byte(spec.TreeDigest+"\n"+files+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return "", err
	}
	_ = os.RemoveAll(root)
	if err := os.Rename(staging, root); err != nil {
		if installedIntact(root, marker, spec.TreeDigest) {
			return executable, nil // another launch finished the same tree first
		}
		return "", err
	}
	return executable, nil
}

// installedIntact reports whether root holds a finished install of the tree
// whose files are unchanged since it was unpacked. A tree edited in the cache
// is installed again from the verified tarballs.
func installedIntact(root, marker, treeDigest string) bool {
	recorded, err := os.ReadFile(marker)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(recorded)), "\n")
	if len(lines) != 2 || lines[0] != treeDigest {
		return false
	}
	files, err := filesDigest(root)
	return err == nil && files == lines[1]
}

// filesDigest is a digest of every file under root (path, mode bit for
// executables, contents), except the completion marker.
func filesDigest(root string) (string, error) {
	hasher := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || entry.IsDir() || relative == ".agentpack-tree-complete" {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected non-file %s in an installed tree", relative)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(hasher, "%s\x00%v\x00%x\n", filepath.ToSlash(relative), info.Mode()&0o111 != 0, sum)
		return nil
	})
	if err != nil {
		return "", err
	}
	return "files-sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

func platformMatches(entry lockfile.NPMTreeEntry) bool {
	return matchesList(entry.OS, runtime.GOOS, map[string]string{"windows": "win32"}) &&
		matchesList(entry.CPU, runtime.GOARCH, map[string]string{"amd64": "x64", "386": "ia32"})
}

// matchesList applies npm's os/cpu rule: a listed "!value" excludes value,
// and when any plain value is listed, value must be one of them.
func matchesList(list []string, value string, rename map[string]string) bool {
	if renamed, ok := rename[value]; ok {
		value = renamed
	}
	positive, allowed := false, false
	for _, item := range list {
		if strings.HasPrefix(item, "!") {
			if item[1:] == value {
				return false
			}
			continue
		}
		positive = true
		allowed = allowed || item == value
	}
	return !positive || allowed
}

// tarballURL is the entry's resolved URL when it is on the server's registry,
// otherwise the registry's standard tarball path.
func tarballURL(registry string, entry lockfile.NPMTreeEntry) string {
	registry = strings.TrimRight(registry, "/")
	if entry.Resolved != "" && strings.HasPrefix(entry.Resolved, registry+"/") {
		return entry.Resolved
	}
	name := strings.TrimPrefix(entry.Path[strings.LastIndex(entry.Path, "node_modules/"):], "node_modules/")
	base := name
	if index := strings.LastIndex(base, "/"); index >= 0 {
		base = base[index+1:]
	}
	return registry + "/" + name + "/-/" + base + "-" + entry.Version + ".tgz"
}

func fetchPackage(ctx context.Context, client *http.Client, registry string, entry lockfile.NPMTreeEntry, destination string) error {
	algorithm, expected, found := strings.Cut(entry.Integrity, "-")
	var hasher hash.Hash
	switch algorithm {
	case "sha512":
		hasher = sha512.New()
	case "sha256":
		hasher = sha256.New()
	case "sha1":
		hasher = sha1.New()
	default:
		found = false
	}
	if !found {
		return fmt.Errorf("%s@%s has an integrity this build cannot check: %q", entry.Path, entry.Version, entry.Integrity)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	location := tarballURL(registry, entry)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("could not download %s: %w", location, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("could not download %s: %s", location, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 512<<20))
	if err != nil {
		return fmt.Errorf("could not download %s: %w", location, err)
	}
	hasher.Write(data)
	if got := base64.StdEncoding.EncodeToString(hasher.Sum(nil)); got != expected {
		return fmt.Errorf("%s@%s: the downloaded tarball has integrity %s-%s, but pack.lock records %s", entry.Path, entry.Version, algorithm, got, entry.Integrity)
	}
	return extractTarball(data, destination)
}

// extractTarball unpacks an npm tarball (whose files sit under one top
// directory, usually package/) into destination. Links and paths that would
// leave destination are refused.
func extractTarball(data []byte, destination string) error {
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.ToSlash(header.Name)
		if index := strings.Index(name, "/"); index >= 0 {
			name = name[index+1:]
		} else {
			continue
		}
		cleaned := path.Clean(name)
		if cleaned == "." || cleaned == "" {
			continue
		}
		if strings.HasPrefix(cleaned, "../") || cleaned == ".." || path.IsAbs(cleaned) {
			return fmt.Errorf("tarball entry %q leaves its package", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(cleaned))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if header.Mode&0o111 != 0 {
				mode = 0o755
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, io.LimitReader(reader, 512<<20)); err != nil {
				file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		default:
			// npm itself ignores links and devices in package tarballs
		}
	}
}

func lastLines(text string, n int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ApplyTrees rewrites each npm server whose lock record holds a locked tree
// so that it starts through `executable mcp-exec`, which installs exactly that
// tree and runs the package's executable with the server's own arguments. The
// spec of each server is written under specDir. Servers without a tree are
// left to ApplyPins.
func ApplyTrees(entries Entries, records []lockfile.MCPServer, executable, specDir string) (Entries, error) {
	out := make(Entries, len(entries))
	for name, entry := range entries {
		out[name] = entry
		record, found := recordFor(records, name)
		if !found || record.Definition != entry.Server.Definition() || !StartsWithNPX(entry.Server) {
			continue
		}
		spec, ok := SpecFor(record)
		if !ok {
			continue
		}
		index, _ := npmPackageArgument(programName(*entry.Server.Command), entry.Server.Args)
		if index < 0 || entry.Server.Args[index] != record.Requested {
			continue
		}
		data, err := json.MarshalIndent(spec, "", " ")
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(specDir, 0o755); err != nil {
			return nil, err
		}
		specPath := filepath.Join(specDir, safeFileName(name)+".json")
		if err := os.WriteFile(specPath, append(data, '\n'), 0o644); err != nil {
			return nil, err
		}
		command := executable
		entry.Server.Command = &command
		entry.Server.Args = append([]string{"mcp-exec", "--spec", specPath, "--"}, entry.Server.Args[index+1:]...)
		out[name] = entry
	}
	return out, nil
}

// StartsWithNPX reports whether a server is started by npx, the only npm
// runner whose packages run on node; bunx, pnpm dlx and yarn dlx keep their
// own runtime and stay pinned at the top level.
func StartsWithNPX(server Server) bool {
	return server.Command != nil && programName(*server.Command) == "npx"
}

func safeFileName(name string) string {
	var builder strings.Builder
	for _, r := range name {
		if r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}
