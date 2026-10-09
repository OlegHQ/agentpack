package lockfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
	"github.com/pelletier/go-toml/v2"
)

// Version is the newest lockfile schema. Version 3 adds package content_hash
// values and [[mcp_servers]] to version 2; a lock that uses neither is still
// written as version 2 so binaries that predate version 3 keep reading it.
const (
	Version     uint32 = 3
	baseVersion uint32 = 2
)

// ContentHashPrefix names the tree digest algorithm stored in content_hash.
const ContentHashPrefix = "sha256-tree-v1:"

// ErrContentHash marks a lock whose content_hash key is present but is not a
// digest this build can check. Such a lock is never regenerated automatically.
var ErrContentHash = errors.New("invalid content hash in lockfile")

// ContentHashError is the ErrContentHash for one package.
type ContentHashError struct{ Module, Value, Path string }

func (err *ContentHashError) Unwrap() error { return ErrContentHash }

func (err *ContentHashError) Error() string {
	return fmt.Sprintf("%v: package %s has content_hash %q in %s, but a content hash must be %s followed by 64 lowercase hex digits; nothing was fetched or staged; restore pack.lock from version control, or upgrade agentpack if a newer version wrote it", ErrContentHash, err.Module, err.Value, err.Path, ContentHashPrefix)
}

func (err *ContentHashError) Details() string {
	return strings.Join([]string{
		"invalid content_hash in pack.lock: " + err.Module,
		"  found     " + fmt.Sprintf("%q", err.Value),
		"  expected  " + ContentHashPrefix + "<64 lowercase hex digits>",
		"  lockfile  " + err.Path,
		"  fix       restore pack.lock from version control, or upgrade agentpack if a newer version wrote it",
	}, "\n") + "\n"
}

func (err *ContentHashError) Summary() string {
	return fmt.Sprintf("invalid content_hash for %s in pack.lock: nothing was fetched or staged", err.Module)
}

// ValidContentHash reports whether value is a complete sha256-tree-v1 digest.
func ValidContentHash(value string) bool {
	digest, found := strings.CutPrefix(value, ContentHashPrefix)
	if !found || len(digest) != 64 {
		return false
	}
	for _, character := range digest {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

// contentHashKeys reports, per [[packages]] entry in file order, whether the
// content_hash key is written at all, so an empty value is not read as absent.
func contentHashKeys(path string) ([]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read lockfile %s: %w", path, err)
	}
	var raw struct {
		Packages []map[string]any `toml:"packages"`
	}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse lockfile: %w", err)
	}
	present := make([]bool, len(raw.Packages))
	for index, pkg := range raw.Packages {
		_, present[index] = pkg["content_hash"]
	}
	return present, nil
}

type PackageKind string

const (
	PackageSkill  PackageKind = "skill"
	PackagePlugin PackageKind = "plugin"
)

func (kind *PackageKind) UnmarshalText(text []byte) error {
	value := PackageKind(text)
	if value != PackageSkill && value != PackagePlugin {
		return fmt.Errorf("unknown package kind %q", text)
	}
	*kind = value
	return nil
}

type Meta struct {
	Name    string `toml:"name"`
	Version string `toml:"version"`
}

type Config struct {
	DisabledPlugins []string `toml:"disabled_plugins,omitempty"`
}

type Package struct {
	Module   string      `toml:"module"`
	Direct   bool        `toml:"direct,omitempty"`
	Kind     PackageKind `toml:"kind"`
	URL      string      `toml:"url"`
	Owner    string      `toml:"owner"`
	Repo     string      `toml:"repo"`
	Path     string      `toml:"path,omitempty"`
	Commit   string      `toml:"commit"`
	CacheKey string      `toml:"cache_key"`
	// ContentHash is the tree digest of the cached package. Empty means the
	// lock predates content hashes and the package is not verified.
	ContentHash string `toml:"content_hash,omitempty"`
	Name        string `toml:"name,omitempty"`
}

const (
	MCPPinned     = "pinned"
	MCPUnpinnable = "unpinnable"
	MCPUnpinned   = "unpinned"
)

// MCPServer records what one merged MCP server definition runs. Environment
// values and arguments are never stored; Definition is a digest of them.
type MCPServer struct {
	Name          string `toml:"name"`
	Source        string `toml:"source"`
	Launcher      string `toml:"launcher"`
	Status        string `toml:"status"`
	Definition    string `toml:"definition"`
	Requested     string `toml:"requested,omitempty"`
	Package       string `toml:"package,omitempty"`
	Version       string `toml:"version,omitempty"`
	Integrity     string `toml:"integrity,omitempty"`
	Registry      string `toml:"registry,omitempty"`
	Host          string `toml:"host,omitempty"`
	Command       string `toml:"command,omitempty"`
	AllowUnpinned bool   `toml:"allow_unpinned,omitempty"`
	Reason        string `toml:"reason,omitempty"`
}

func (pkg Package) NeedsBackfill() bool {
	return pkg.Kind == PackagePlugin && pkg.URL != "" &&
		(pkg.CacheKey == "" || pkg.Commit == "" || pkg.Owner == "" || pkg.Repo == "")
}

type PackLock struct {
	LockfileVersion uint32      `toml:"lockfile_version"`
	Meta            Meta        `toml:"meta"`
	Config          Config      `toml:"config,omitempty"`
	Packages        []Package   `toml:"packages,omitempty"`
	MCPServers      []MCPServer `toml:"mcp_servers,omitempty"`
}

// diskLock uses a pointer so the empty config table is omitted exactly as it
// is by the canonical writer. PackLock keeps the friendlier concrete value in code.
type diskLock struct {
	LockfileVersion       uint32      `toml:"lockfile_version"`
	LegacyLockfileVersion uint32      `toml:"lockfile-version,omitempty"`
	Meta                  Meta        `toml:"meta"`
	Config                *Config     `toml:"config,omitempty"`
	Packages              []Package   `toml:"packages,omitempty"`
	MCPServers            []MCPServer `toml:"mcp_servers,omitempty"`
}

func EmptyForProject(projectRoot string) PackLock {
	name := filepath.Base(projectRoot)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "project"
	}
	return PackLock{LockfileVersion: baseVersion, Meta: Meta{Name: name, Version: "0.0.1"}}
}

func Load(projectRoot string) (PackLock, error) {
	return LoadFromPath(paths.LockPath(projectRoot))
}

func LoadFromPath(path string) (PackLock, error) {
	var result PackLock
	err := withOperationLock(path, func() error {
		var err error
		result, err = loadFromPath(path)
		return err
	})
	return result, err
}

func loadFromPath(path string) (PackLock, error) {
	file, err := os.Open(path)
	if err != nil {
		return PackLock{}, fmt.Errorf("read lockfile %s: %w", path, err)
	}
	defer file.Close()
	var disk diskLock
	decoder := toml.NewDecoder(file).DisallowUnknownFields()
	if err := decoder.Decode(&disk); err != nil {
		var unknown *toml.StrictMissingError
		if errors.As(err, &unknown) {
			fields := make([]string, 0, len(unknown.Errors))
			for _, item := range unknown.Errors {
				fields = append(fields, strings.Join(item.Key(), "."))
			}
			return PackLock{}, fmt.Errorf("parse lockfile: unsupported field(s) %s in %s; run `agentpack lock` to regenerate it", strings.Join(fields, ", "), path)
		}
		return PackLock{}, fmt.Errorf("parse lockfile: %w", err)
	}
	if disk.LockfileVersion == 0 {
		disk.LockfileVersion = disk.LegacyLockfileVersion
	} else if disk.LegacyLockfileVersion != 0 && disk.LegacyLockfileVersion != disk.LockfileVersion {
		return PackLock{}, fmt.Errorf("parse lockfile: conflicting lockfile_version values in %s", path)
	}
	if disk.LockfileVersion != baseVersion && disk.LockfileVersion != Version {
		return PackLock{}, fmt.Errorf("parse lockfile: unsupported lockfile_version %d (expected %d or %d); run `agentpack lock` to regenerate %s", disk.LockfileVersion, baseVersion, Version, path)
	}
	present, err := contentHashKeys(path)
	if err != nil {
		return PackLock{}, err
	}
	for index, pkg := range disk.Packages {
		// "No content hash" means the key is absent. A key that is present must
		// hold a well-formed digest of a known algorithm, or the lock is refused.
		if !present[index] {
			continue
		}
		if !ValidContentHash(pkg.ContentHash) {
			return PackLock{}, &ContentHashError{Module: pkg.Module, Value: pkg.ContentHash, Path: path}
		}
	}
	lock := PackLock{LockfileVersion: disk.LockfileVersion, Meta: disk.Meta, Packages: disk.Packages, MCPServers: disk.MCPServers}
	if disk.Config != nil {
		lock.Config = *disk.Config
	}
	return lock, nil
}

func (lock PackLock) Plugins() []Package { return lock.packagesByKind(PackagePlugin) }
func (lock PackLock) Skills() []Package  { return lock.packagesByKind(PackageSkill) }
func (lock PackLock) PluginCount() int   { return len(lock.Plugins()) }
func (lock PackLock) SkillCount() int    { return len(lock.Skills()) }

// UnverifiedPackages lists modules whose lock entry has no content hash.
func (lock PackLock) UnverifiedPackages() []string {
	var modules []string
	for _, pkg := range lock.Packages {
		if pkg.CacheKey != "" && pkg.ContentHash == "" {
			modules = append(modules, pkg.Module)
		}
	}
	return modules
}

func (lock PackLock) MCPServer(name string) (MCPServer, bool) {
	for _, server := range lock.MCPServers {
		if server.Name == name {
			return server, true
		}
	}
	return MCPServer{}, false
}

func (lock PackLock) usesVersion3() bool {
	if len(lock.MCPServers) != 0 {
		return true
	}
	for _, pkg := range lock.Packages {
		if pkg.ContentHash != "" {
			return true
		}
	}
	return false
}

func (lock PackLock) packagesByKind(kind PackageKind) []Package {
	result := make([]Package, 0, len(lock.Packages))
	for _, pkg := range lock.Packages {
		if pkg.Kind == kind {
			result = append(result, pkg)
		}
	}
	return result
}

func (lock PackLock) Save(projectRoot string) error {
	snapshot := lock
	snapshot.Packages = append([]Package(nil), lock.Packages...)
	sort.Slice(snapshot.Packages, func(i, j int) bool {
		return snapshot.Packages[i].Module < snapshot.Packages[j].Module
	})
	snapshot.MCPServers = append([]MCPServer(nil), lock.MCPServers...)
	sort.Slice(snapshot.MCPServers, func(i, j int) bool {
		return snapshot.MCPServers[i].Name < snapshot.MCPServers[j].Name
	})
	snapshot.LockfileVersion = baseVersion
	if snapshot.usesVersion3() {
		snapshot.LockfileVersion = Version
	}
	disk := diskLock{LockfileVersion: snapshot.LockfileVersion, Meta: snapshot.Meta, Packages: snapshot.Packages, MCPServers: snapshot.MCPServers}
	if len(snapshot.Config.DisabledPlugins) != 0 {
		config := snapshot.Config
		disk.Config = &config
	}
	var output bytes.Buffer
	if err := toml.NewEncoder(&output).Encode(disk); err != nil {
		return fmt.Errorf("encode lockfile: %w", err)
	}
	path := paths.LockPath(projectRoot)
	if err := withOperationLock(path, func() error { return writeAtomic(path, output.Bytes(), 0o644) }); err != nil {
		return fmt.Errorf("write lockfile %s: %w", path, err)
	}
	return nil
}

func withOperationLock(path string, operation func() error) (err error) {
	hash, err := paths.ProjectPathHash(filepath.Dir(path))
	if err != nil {
		return err
	}
	directory := filepath.Join(os.TempDir(), "agentpack-"+hash)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	guard := flock.New(filepath.Join(directory, "pack.lock.lock"))
	if err := guard.Lock(); err != nil {
		return err
	}
	defer func() {
		if unlockErr := guard.Unlock(); unlockErr != nil {
			err = errors.Join(err, unlockErr)
		}
	}()
	return operation()
}

func writeAtomic(path string, data []byte, mode os.FileMode) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".pack.lock-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func Init(projectRoot, name, version string) error {
	path := paths.LockPath(projectRoot)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("pack.lock already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect lockfile %s: %w", path, err)
	}
	lock := EmptyForProject(projectRoot)
	if name != "" {
		lock.Meta.Name = name
	}
	if version != "" {
		lock.Meta.Version = version
	}
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		return err
	}
	return lock.Save(projectRoot)
}
