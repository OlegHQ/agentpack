package lockfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/pelletier/go-toml/v2"
)

const Version uint32 = 2

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
	Name     string      `toml:"name,omitempty"`
}

func (pkg Package) NeedsBackfill() bool {
	return pkg.Kind == PackagePlugin && pkg.URL != "" &&
		(pkg.CacheKey == "" || pkg.Commit == "" || pkg.Owner == "" || pkg.Repo == "")
}

type PackLock struct {
	LockfileVersion uint32    `toml:"lockfile-version"`
	Meta            Meta      `toml:"meta"`
	Config          Config    `toml:"config,omitempty"`
	Packages        []Package `toml:"packages,omitempty"`
}

// diskLock uses a pointer so the empty config table is omitted exactly as it
// is by the Rust writer. PackLock keeps the friendlier concrete value in code.
type diskLock struct {
	LockfileVersion uint32    `toml:"lockfile-version"`
	Meta            Meta      `toml:"meta"`
	Config          *Config   `toml:"config,omitempty"`
	Packages        []Package `toml:"packages,omitempty"`
}

func EmptyForProject(projectRoot string) PackLock {
	name := filepath.Base(projectRoot)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "project"
	}
	return PackLock{LockfileVersion: Version, Meta: Meta{Name: name, Version: "0.0.1"}}
}

func Load(projectRoot string) (PackLock, error) {
	return LoadFromPath(paths.LockPath(projectRoot))
}

func LoadFromPath(path string) (PackLock, error) {
	file, err := os.Open(path)
	if err != nil {
		return PackLock{}, fmt.Errorf("read lockfile %s: %w", path, err)
	}
	defer file.Close()
	var disk diskLock
	decoder := toml.NewDecoder(file).DisallowUnknownFields()
	if err := decoder.Decode(&disk); err != nil {
		return PackLock{}, fmt.Errorf("parse lockfile: %w", err)
	}
	if disk.LockfileVersion != Version {
		return PackLock{}, fmt.Errorf("parse lockfile: unsupported lockfile-version %d (expected %d); run `agentpack lock` to regenerate %s", disk.LockfileVersion, Version, path)
	}
	lock := PackLock{LockfileVersion: disk.LockfileVersion, Meta: disk.Meta, Packages: disk.Packages}
	if disk.Config != nil {
		lock.Config = *disk.Config
	}
	return lock, nil
}

func (lock PackLock) Plugins() []Package { return lock.packagesByKind(PackagePlugin) }
func (lock PackLock) Skills() []Package  { return lock.packagesByKind(PackageSkill) }
func (lock PackLock) PluginCount() int   { return len(lock.Plugins()) }
func (lock PackLock) SkillCount() int    { return len(lock.Skills()) }

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
	disk := diskLock{LockfileVersion: snapshot.LockfileVersion, Meta: snapshot.Meta, Packages: snapshot.Packages}
	if len(snapshot.Config.DisabledPlugins) != 0 {
		config := snapshot.Config
		disk.Config = &config
	}
	var output bytes.Buffer
	if err := toml.NewEncoder(&output).Encode(disk); err != nil {
		return fmt.Errorf("encode lockfile: %w", err)
	}
	path := paths.LockPath(projectRoot)
	if err := os.WriteFile(path, output.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write lockfile %s: %w", path, err)
	}
	return nil
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
