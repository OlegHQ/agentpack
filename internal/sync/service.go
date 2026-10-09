package sync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/cache"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/modecatalog"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/resolve"
	"github.com/OlegHQ/agentpack/internal/staging"
)

type Service struct {
	Client *http.Client
	// Notify receives one-line integrity reports. Nil writes them to stderr.
	Notify func(message string)
}
type SyncOptions struct {
	DryRun, VerifyOnly, UpdateLock bool
	// Repair re-fetches cache entries that fail content verification.
	Repair bool
	Mode   string
	Target *base.Target
}

// LockOptions tunes the commands that are allowed to add records to pack.lock.
type LockOptions struct {
	Refresh bool
	// AllowUnpinnedMCP records an MCP server as unpinned when its registry
	// cannot be asked for an exact version, instead of failing.
	AllowUnpinnedMCP bool
}
type SyncResult struct {
	Skills, Plugins, Shadowed, IndexEntries int
	Mode                                    mode.Effective
	Warnings                                []string
}

func NewService() Service { return Service{Client: &http.Client{Transport: http.DefaultTransport}} }
func (service Service) client() *http.Client {
	if service.Client != nil {
		return service.Client
	}
	return http.DefaultClient
}
func (service Service) notify(format string, arguments ...any) {
	message := fmt.Sprintf(format, arguments...)
	if service.Notify != nil {
		service.Notify(message)
		return
	}
	fmt.Fprintln(os.Stderr, "warning: "+message)
}

// resolveRun says who is resolving. Only an explicit run may change the pins
// of entries that are already locked; an implicit one (sync, launch) keeps them.
type resolveRun struct {
	options resolve.ResolveOptions
	pinMode mcpPinMode
	// explicit marks lock, add, remove and update: they always write the lock.
	explicit bool
	// regenerate lets `agentpack lock` replace a lock it cannot parse.
	regenerate       bool
	allowUnpinnedMCP bool
	primed           []lockfile.Package
}

// resolveAndSave is the resolve step of add, remove and update: it records
// missing content hashes and keeps MCP records as they are.
func (service Service) resolveAndSave(ctx context.Context, projectRoot string, project *manifest.Manifest, refresh bool, primed []lockfile.Package) (lockfile.PackLock, error) {
	return service.resolve(ctx, projectRoot, project, resolveRun{options: resolve.ResolveOptions{RefreshFloating: refresh, RecordContentHashes: true}, explicit: true, primed: primed})
}

func (service Service) resolve(ctx context.Context, projectRoot string, project *manifest.Manifest, run resolveRun) (lockfile.PackLock, error) {
	previous, err := loadCheckedLock(projectRoot)
	exists := err == nil
	switch {
	case err == nil:
	case os.IsNotExist(rootCause(err)):
		previous = lockfile.PackLock{}
	case run.regenerate && !errors.Is(err, lockfile.ErrContentHash) && !errors.As(err, new(*cache.LockEntryError)):
		service.notify("the existing pack.lock could not be read and is regenerated from agentpack.toml; pins it held are resolved again (%v)", err)
		previous = lockfile.PackLock{}
	default:
		return lockfile.PackLock{}, err
	}
	locked := previous
	locked.Packages = append([]lockfile.Package(nil), previous.Packages...)
	unverified := make(map[string]bool)
	for _, pkg := range previous.Packages {
		if pkg.CacheKey != "" && pkg.ContentHash == "" {
			unverified[pkg.CacheKey] = true
		}
	}
	for _, pkg := range run.primed {
		var kept []lockfile.Package
		for _, current := range previous.Packages {
			if current.Module != pkg.Module {
				kept = append(kept, current)
			}
		}
		previous.Packages = append(kept, pkg)
	}
	options := run.options
	options.Previous = &previous
	options.Notify = func(message string) { service.notify("%s", message) }
	resolved, err := resolve.NewResolver(ctx, service.client()).Resolve(ctx, projectRoot, project, options)
	if err != nil {
		return lockfile.PackLock{}, err
	}
	resolved.Config = locked.Config
	if resolved.MCPServers, err = service.settleMCPServers(ctx, projectRoot, project, resolved, locked.MCPServers, run.pinMode, run.allowUnpinnedMCP); err != nil {
		return lockfile.PackLock{}, err
	}
	// An implicit run leaves the file alone unless what it records changed.
	if run.explicit || !exists || !sameLock(locked, resolved) {
		if err := resolved.Save(projectRoot); err != nil {
			return lockfile.PackLock{}, err
		}
	}
	if exists {
		for _, change := range lockChanges(locked, resolved, !run.explicit) {
			service.notify("pack.lock: %s", change)
		}
	}
	recorded := 0
	for _, pkg := range resolved.Packages {
		if pkg.ContentHash != "" && unverified[pkg.CacheKey] {
			recorded++
		}
	}
	if recorded != 0 {
		service.notify("recorded content hashes for %d package(s) in %s that had none", recorded, paths.LockPath(projectRoot))
	}
	return resolved, nil
}
func (service Service) Lock(ctx context.Context, projectRoot string, refresh bool) (lockfile.PackLock, error) {
	return service.LockWithOptions(ctx, projectRoot, LockOptions{Refresh: refresh})
}

// LockWithOptions resolves the manifest, records a content hash for every
// package, and pins the MCP servers the project stages.
func (service Service) LockWithOptions(ctx context.Context, projectRoot string, options LockOptions) (lockfile.PackLock, error) {
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		return lockfile.PackLock{}, err
	}
	project, err := manifest.Load(projectRoot)
	if err != nil {
		return lockfile.PackLock{}, err
	}
	if project == nil {
		return lockfile.PackLock{}, fmt.Errorf("agentpack.toml required")
	}
	pinMode := mcpResolve
	if options.Refresh {
		pinMode = mcpRefresh
	}
	lock, err := service.resolve(ctx, projectRoot, project, resolveRun{options: resolve.ResolveOptions{RefreshFloating: options.Refresh, RecordContentHashes: true}, pinMode: pinMode, explicit: true, regenerate: true, allowUnpinnedMCP: options.AllowUnpinnedMCP})
	if err == nil {
		service.reportMCPServers(lock)
	}
	return lock, err
}

// Update refreshes every floating dependency when specs is empty. Otherwise it
// refreshes only the requested direct dependencies, using their manifest pins.
func (service Service) Update(ctx context.Context, projectRoot string, specs []string, noSync bool) (lockfile.PackLock, error) {
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		return lockfile.PackLock{}, err
	}
	project, err := manifest.Load(projectRoot)
	if err != nil {
		return lockfile.PackLock{}, err
	}
	if project == nil {
		return lockfile.PackLock{}, fmt.Errorf("agentpack.toml required")
	}
	refresh := len(specs) == 0
	modules := make(map[string]bool, len(specs))
	for _, spec := range specs {
		module, err := ResolveRemoveSpec(projectRoot, spec, project)
		if err != nil {
			return lockfile.PackLock{}, err
		}
		modules[module] = true
	}
	pinMode := mcpCarry
	if refresh {
		pinMode = mcpRefresh
	}
	lock, err := service.resolve(ctx, projectRoot, project, resolveRun{options: resolve.ResolveOptions{RefreshFloating: refresh, RefreshModules: modules, RecordContentHashes: true}, pinMode: pinMode, explicit: true})
	if err != nil {
		return lockfile.PackLock{}, err
	}
	if !noSync {
		_, err = service.Sync(ctx, projectRoot, SyncOptions{})
	}
	return lock, err
}
func (service Service) Add(ctx context.Context, projectRoot, spec string, noSync bool) (lockfile.Package, error) {
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		return lockfile.Package{}, err
	}
	if err := ensureProjectFiles(projectRoot); err != nil {
		return lockfile.Package{}, err
	}
	if source, ok := existingPath(projectRoot, spec); ok {
		name := filepath.Base(source)
		relative, err := filepath.Rel(projectRoot, source)
		if err != nil {
			return lockfile.Package{}, err
		}
		if err := manifest.AppendPathDependency(projectRoot, name, filepath.ToSlash(relative)); err != nil {
			return lockfile.Package{}, err
		}
		project, _ := manifest.Load(projectRoot)
		lock, err := service.resolveAndSave(ctx, projectRoot, project, false, nil)
		if err != nil {
			return lockfile.Package{}, err
		}
		pkg := findPackage(lock, name)
		if !noSync {
			_, err = service.Sync(ctx, projectRoot, SyncOptions{})
		}
		return pkg, err
	}
	resolved, err := NewAddResolver(service.client()).Resolve(ctx, projectRoot, spec)
	if err != nil {
		return lockfile.Package{}, err
	}
	key := cache.DependencyKey(resolved.Package.Module, resolved.Package.Owner, resolved.Package.Repo, resolved.Package.Path)
	if err := manifest.AppendDependencyPin(projectRoot, key, resolved.GitRef); err != nil {
		return lockfile.Package{}, err
	}
	if err := RecordFetched(resolved.Package, resolved.Shorthand); err != nil {
		return lockfile.Package{}, err
	}
	project, _ := manifest.Load(projectRoot)
	lock, err := service.resolveAndSave(ctx, projectRoot, project, false, []lockfile.Package{resolved.Package})
	if err != nil {
		return lockfile.Package{}, err
	}
	pkg := findPackage(lock, key)
	if !noSync {
		_, err = service.Sync(ctx, projectRoot, SyncOptions{})
	}
	return pkg, err
}
func (service Service) Remove(ctx context.Context, projectRoot, spec string, noSync bool) (string, error) {
	project, err := manifest.Load(projectRoot)
	if err != nil {
		return "", err
	}
	if project == nil {
		return "", fmt.Errorf("agentpack.toml required")
	}
	key, err := ResolveRemoveSpec(projectRoot, spec, project)
	if err != nil {
		return "", err
	}
	if err := manifest.RemoveDependencyEntry(projectRoot, key); err != nil {
		return "", err
	}
	project, _ = manifest.Load(projectRoot)
	if _, err := service.resolveAndSave(ctx, projectRoot, project, false, nil); err != nil {
		return "", err
	}
	if !noSync {
		_, err = service.Sync(ctx, projectRoot, SyncOptions{})
	}
	return key, err
}
func (service Service) Sync(ctx context.Context, projectRoot string, options SyncOptions) (SyncResult, error) {
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		return SyncResult{}, err
	}
	project, err := manifest.Load(projectRoot)
	if err != nil {
		return SyncResult{}, err
	}
	if !options.DryRun && project != nil && len(project.Dependencies) != 0 {
		// sync verifies what the lock already pins. It records a content hash
		// only for content it has to download, and adds no MCP records.
		// --update-lock is the one way it re-resolves on purpose.
		resolveOptions := resolve.ResolveOptions{RefreshFloating: options.UpdateLock, RepairCache: options.Repair}
		if _, err := service.resolve(ctx, projectRoot, project, resolveRun{options: resolveOptions, explicit: options.UpdateLock}); err != nil {
			return SyncResult{}, err
		}
	}
	lock, err := loadCheckedLock(projectRoot)
	if os.IsNotExist(rootCause(err)) && options.Target != nil {
		lock = lockfile.EmptyForProject(projectRoot)
		err = nil
	}
	if err != nil {
		return SyncResult{}, err
	}
	effective, err := resolveMode(projectRoot, project, &lock, options.Mode)
	if err != nil {
		return SyncResult{}, err
	}
	plugins := lock.Plugins()
	shadowed := 0
	for _, skill := range lock.Skills() {
		if staging.SkillIsShadowed(skill, plugins) {
			shadowed++
		}
	}
	result := SyncResult{Skills: lock.SkillCount(), Plugins: lock.PluginCount(), Shadowed: shadowed, Mode: effective}
	if options.DryRun {
		return result, nil
	}
	dirty := false
	for index := range lock.Packages {
		pkg := &lock.Packages[index]
		if pkg.NeedsBackfill() {
			resolved, err := cache.FetchGitHubAssetURL(ctx, service.client(), pkg.URL)
			if err != nil {
				return result, err
			}
			if resolved.Kind != lockfile.PackagePlugin {
				return result, fmt.Errorf("plugin URL %s resolved to a skill subtree", pkg.URL)
			}
			*pkg = resolved
			dirty = true
		}
	}
	if dirty {
		if err := lock.Save(projectRoot); err != nil {
			return result, err
		}
	}
	for _, pkg := range lock.Packages {
		if pkg.CacheKey == "" {
			continue
		}
		restore := cache.GitHubRestore(ctx, service.client())
		ready, err := cache.EnsureLockCached(pkg, restore)
		var mismatch *cache.IntegrityError
		if options.Repair && errors.As(err, &mismatch) && !mismatch.Fetched {
			if _, _, err = cache.RefetchPackage(pkg, restore); err == nil {
				service.notify("%s", mismatch.Repaired())
				ready, err = cache.EnsureLockCached(pkg, restore)
			}
		}
		if err != nil {
			return result, err
		}
		if !ready {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s %s: cache missing and source unavailable", pkg.Kind, pkg.CacheKey))
		}
		record := cache.EntryRecord{Kind: pkg.Kind, SourceURL: pkg.URL, Owner: pkg.Owner, Repo: pkg.Repo, Path: pkg.Path, Commit: pkg.Commit, FetchedAtUnix: time.Now().Unix()}
		if err := cache.UpsertEntry(pkg.CacheKey, record, nil); err != nil {
			return result, err
		}
	}
	if err := service.checkLockRecords(projectRoot, project, lock); err != nil {
		return result, err
	}
	pipeline := staging.Pipeline{ProjectRoot: projectRoot, Lock: lock, Manifest: project, Mode: effective, Target: options.Target}
	drift, recorded, driftErr := pipeline.StagedDrift(options.VerifyOnly)
	if driftErr != nil {
		return result, driftErr
	}
	if options.VerifyOnly {
		switch {
		case !recorded:
			err = fmt.Errorf("cannot verify staging: no sync by this version of agentpack has recorded what it staged for mode %q; run `agentpack sync` once", effective.Name())
		case len(drift) != 0:
			err = &staging.DriftError{Changes: drift}
		default:
			// Verify may drop staged duplicates of skills the user has since
			// installed; record the tree again so that is not read as drift.
			if err = pipeline.Verify(); err == nil {
				err = pipeline.RecordStaged()
			}
		}
	} else {
		if len(drift) != 0 {
			shown := append([]string(nil), drift...)
			if len(shown) > 5 {
				shown = append(shown[:5], fmt.Sprintf("and %d more", len(drift)-5))
			}
			for index, change := range shown {
				// "modified  /path" is aligned for a list; use one space in a sentence.
				if kind, path, found := strings.Cut(change, " "); found {
					shown[index] = kind + " " + strings.TrimLeft(path, " ")
				}
			}
			service.notify("%d staged file(s) changed since the last sync and were replaced from the verified cache: %s", len(drift), strings.Join(shown, "; "))
		}
		_, err = pipeline.Rebuild()
		if err == nil {
			err = pipeline.Verify()
		}
	}
	if err != nil {
		return result, err
	}
	keys, err := cache.ListKeys()
	result.IndexEntries = len(keys)
	return result, err
}

func (service Service) SyncForLaunch(ctx context.Context, projectRoot, selectedMode string, target base.Target) (mode.Effective, bool, error) {
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		return mode.Effective{}, false, err
	}
	project, err := manifest.Load(projectRoot)
	if err != nil {
		return mode.Effective{}, false, err
	}
	lock, err := loadCheckedLock(projectRoot)
	if os.IsNotExist(rootCause(err)) {
		lock = lockfile.EmptyForProject(projectRoot)
		err = nil
	}
	if err != nil {
		return mode.Effective{}, false, err
	}
	effective, err := resolveMode(projectRoot, project, &lock, selectedMode)
	if err != nil {
		return mode.Effective{}, false, err
	}
	current, err := launchDigest(projectRoot, effective, &target, lock)
	if err != nil {
		return mode.Effective{}, false, err
	}
	if stored, found, err := ReadLaunchDigest(projectRoot, effective.Name()); err != nil {
		return mode.Effective{}, false, err
	} else if found && stored == current {
		// Nothing is staged on this path, so the harness keeps the tree built
		// by the last full sync. The digest above already covers the cache
		// metadata; hashing the content again is opt-in.
		verifyCache := cache.VerifyLockCacheLayout
		if envEnabled(FullVerifyEnv) {
			verifyCache = cache.VerifyLockCacheIntegrity
		}
		pipeline := staging.Pipeline{ProjectRoot: projectRoot, Lock: lock, Manifest: project, Mode: effective, Target: &target}
		if verifyCache(lock) == nil && pipeline.Verify() == nil {
			if err := service.checkLockRecords(projectRoot, project, lock); err != nil {
				return mode.Effective{}, false, err
			}
			return effective, true, nil
		}
	}
	result, err := service.Sync(ctx, projectRoot, SyncOptions{Mode: effective.Name(), Target: &target})
	if err != nil {
		return mode.Effective{}, false, err
	}
	effective = result.Mode
	if lock, err = loadCheckedLock(projectRoot); os.IsNotExist(rootCause(err)) {
		lock = lockfile.EmptyForProject(projectRoot)
	} else if err != nil {
		return mode.Effective{}, false, err
	}
	digest, err := launchDigest(projectRoot, effective, &target, lock)
	if err != nil {
		return mode.Effective{}, false, err
	}
	if err := WriteLaunchDigest(projectRoot, effective.Name(), digest); err != nil {
		return mode.Effective{}, false, err
	}
	return effective, false, nil
}
func resolveMode(projectRoot string, project *manifest.Manifest, lock *lockfile.PackLock, name string) (mode.Effective, error) {
	if name == "" {
		name = mode.DefaultName
	}
	definition := mode.ImplicitDefault()
	if project != nil {
		var found bool
		definition, found = project.ModeDefinition(name)
		if !found {
			return mode.Effective{}, fmt.Errorf("unknown mode: %s", name)
		}
	} else if name != mode.DefaultName {
		return mode.Effective{}, fmt.Errorf("unknown mode: %s", name)
	}
	catalog, err := modecatalog.BuildCapabilityCatalog(projectRoot, lock, project)
	if err != nil {
		return mode.Effective{}, fmt.Errorf("build mode capability catalog: %w", err)
	}
	return mode.NewEffective(name, definition, catalog)
}
func ensureProjectFiles(root string) error {
	if project, err := manifest.Load(root); err != nil {
		return err
	} else if project != nil {
		return nil
	}
	name := filepath.Base(root)
	if err := manifest.WriteStub(root, name, "0.0.1"); err != nil {
		return err
	}
	if _, err := os.Stat(paths.LockPath(root)); os.IsNotExist(err) {
		return lockfile.Init(root, name, "0.0.1")
	}
	return nil
}
func existingPath(root, spec string) (string, bool) {
	path := spec
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(canonical)
	return canonical, err == nil && info.IsDir()
}
func findPackage(lock lockfile.PackLock, module string) lockfile.Package {
	for _, pkg := range lock.Packages {
		if pkg.Module == module {
			return pkg
		}
	}
	return lockfile.Package{}
}
func rootCause(err error) error {
	for err != nil {
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok || unwrapped.Unwrap() == nil {
			return err
		}
		err = unwrapped.Unwrap()
	}
	return nil
}
