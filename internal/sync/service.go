package sync

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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

type Service struct{ Client *http.Client }
type SyncOptions struct {
	DryRun, VerifyOnly, UpdateLock bool
	Mode                           string
	Target                         *base.Target
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
func (service Service) resolveAndSave(ctx context.Context, projectRoot string, project *manifest.Manifest, refresh bool, primed []lockfile.Package) (lockfile.PackLock, error) {
	previous, _ := lockfile.Load(projectRoot)
	if len(primed) != 0 {
		for _, pkg := range primed {
			var kept []lockfile.Package
			for _, current := range previous.Packages {
				if current.Module != pkg.Module {
					kept = append(kept, current)
				}
			}
			previous.Packages = append(kept, pkg)
		}
	}
	resolved, err := resolve.NewResolver(ctx, service.client()).Resolve(ctx, projectRoot, project, resolve.ResolveOptions{Previous: &previous, RefreshFloating: refresh})
	if err != nil {
		return lockfile.PackLock{}, err
	}
	if err := resolved.Save(projectRoot); err != nil {
		return lockfile.PackLock{}, err
	}
	return resolved, nil
}
func (service Service) Lock(ctx context.Context, projectRoot string, refresh bool) (lockfile.PackLock, error) {
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
	return service.resolveAndSave(ctx, projectRoot, project, refresh, nil)
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
		if _, err := service.resolveAndSave(ctx, projectRoot, project, options.UpdateLock, nil); err != nil {
			return SyncResult{}, err
		}
	}
	lock, err := lockfile.Load(projectRoot)
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
		ready, err := cache.EnsureLockCached(pkg, cache.GitHubRestore(ctx, service.client()))
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
	pipeline := staging.Pipeline{ProjectRoot: projectRoot, Lock: lock, Manifest: project, Mode: effective, Target: options.Target}
	if options.VerifyOnly {
		err = pipeline.Verify()
	} else {
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
	lock, err := lockfile.Load(projectRoot)
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
	current, err := ComputeLaunchDigest(projectRoot, effective, &target)
	if err != nil {
		return mode.Effective{}, false, err
	}
	if stored, found, err := ReadLaunchDigest(projectRoot, effective.Name()); err != nil {
		return mode.Effective{}, false, err
	} else if found && stored == current {
		pipeline := staging.Pipeline{ProjectRoot: projectRoot, Lock: lock, Manifest: project, Mode: effective, Target: &target}
		if cache.VerifyLockCacheIntegrity(lock) == nil && pipeline.Verify() == nil {
			return effective, true, nil
		}
	}
	result, err := service.Sync(ctx, projectRoot, SyncOptions{Mode: effective.Name(), Target: &target})
	if err != nil {
		return mode.Effective{}, false, err
	}
	effective = result.Mode
	digest, err := ComputeLaunchDigest(projectRoot, effective, &target)
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
