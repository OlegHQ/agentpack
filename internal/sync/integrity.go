package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/cache"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/staging"
)

// RequireVerifiedEnv makes sync and the launchers refuse a lock that still has
// packages without a content hash, instead of staging them with a warning.
const RequireVerifiedEnv = "AGENTPACK_REQUIRE_VERIFIED"

// FullVerifyEnv makes a launch whose inputs are unchanged hash every cache
// entry again, instead of comparing file metadata with the last full check.
const FullVerifyEnv = "AGENTPACK_FULL_VERIFY"

type mcpPinMode uint8

const (
	// mcpCarry keeps records whose definition is unchanged and adds none.
	mcpCarry mcpPinMode = iota
	// mcpResolve also records servers that are new, changed, or still unpinned.
	mcpResolve
	// mcpRefresh additionally re-resolves floating npm specs such as @latest.
	mcpRefresh
)

func envEnabled(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// launchDigest extends the launch input digest with the cache metadata
// fingerprint, so a visible change to a verified cache entry ends the fast
// path and sends the launch through a full sync, which hashes the content.
func launchDigest(projectRoot, workspace string, effective mode.Effective, target *base.Target, lock lockfile.PackLock) (string, error) {
	inputs, err := ComputeLaunchDigest(projectRoot, workspace, effective, target)
	if err != nil {
		return "", err
	}
	fingerprint, err := cache.StatFingerprint(lock)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(inputs + "\x00cache\x00" + fingerprint))
	return hex.EncodeToString(sum[:]), nil
}

// loadCheckedLock loads pack.lock and refuses one whose hashed entries are
// not self-consistent. Every command that acts on the lock reads it this way.
func loadCheckedLock(projectRoot string) (lockfile.PackLock, error) {
	lock, err := lockfile.Load(projectRoot)
	if err != nil {
		return lockfile.PackLock{}, err
	}
	return lock, cache.CheckLockEntries(lock)
}

// sameLock reports whether two locks record the same thing, ignoring order.
func sameLock(first, second lockfile.PackLock) bool {
	packages := func(lock lockfile.PackLock) map[string]lockfile.Package {
		byModule := make(map[string]lockfile.Package, len(lock.Packages))
		for _, pkg := range lock.Packages {
			byModule[pkg.Module] = pkg
		}
		return byModule
	}
	servers := func(lock lockfile.PackLock) map[string]lockfile.MCPServer {
		byName := make(map[string]lockfile.MCPServer, len(lock.MCPServers))
		for _, server := range lock.MCPServers {
			byName[server.Name] = server
		}
		return byName
	}
	return first.Meta == second.Meta && slices.Equal(first.Config.DisabledPlugins, second.Config.DisabledPlugins) &&
		len(first.Packages) == len(second.Packages) && maps.Equal(packages(first), packages(second)) &&
		len(first.MCPServers) == len(second.MCPServers) && maps.Equal(servers(first), servers(second))
}

// lockChanges describes, one line each, how a resolve changed the lock: pins
// that moved, and (for an implicit run) entries and MCP records that came or
// went. A first content hash is reported separately.
func lockChanges(previous, resolved lockfile.PackLock, membership bool) []string {
	var changes []string
	before := make(map[string]lockfile.Package, len(previous.Packages))
	for _, pkg := range previous.Packages {
		before[pkg.Module] = pkg
	}
	after := make(map[string]bool, len(resolved.Packages))
	for _, pkg := range resolved.Packages {
		after[pkg.Module] = true
		old, found := before[pkg.Module]
		switch {
		case !found && membership:
			changes = append(changes, fmt.Sprintf("added %s at commit %s", pkg.Module, pkg.Commit))
		case found && old.Commit != pkg.Commit:
			changes = append(changes, fmt.Sprintf("%s moved from commit %s to %s", pkg.Module, old.Commit, pkg.Commit))
		}
	}
	for _, pkg := range previous.Packages {
		if !after[pkg.Module] && membership {
			changes = append(changes, fmt.Sprintf("removed %s", pkg.Module))
		}
	}
	for _, old := range previous.MCPServers {
		current, found := resolved.MCPServer(old.Name)
		switch {
		case !found && membership:
			changes = append(changes, fmt.Sprintf("dropped the record of MCP server %s, which is gone or was redefined", old.Name))
		case found && current.Version != old.Version:
			changes = append(changes, fmt.Sprintf("MCP server %s moved from %s@%s to %s@%s", old.Name, old.Package, old.Version, current.Package, current.Version))
		}
	}
	sort.Strings(changes)
	return changes
}

// checkLockRecords reports what the lock does not vouch for: packages without
// a content hash and MCP servers without a record for their current
// definition. Both still stage, as they did before the lock could hold them.
func (service Service) checkLockRecords(projectRoot string, project *manifest.Manifest, lock lockfile.PackLock) error {
	if unverified := lock.UnverifiedPackages(); len(unverified) != 0 {
		if envEnabled(RequireVerifiedEnv) {
			return fmt.Errorf("%s has %d package(s) without a content hash (%s) and %s is set; run `agentpack lock` to record them", paths.LockPath(projectRoot), len(unverified), strings.Join(unverified, ", "), RequireVerifiedEnv)
		}
		service.notify("%d package(s) in pack.lock have no content hash and are not verified; run `agentpack lock` to record them", len(unverified))
	}
	entries, err := staging.CollectMCP(projectRoot, lock, project, nil)
	if err != nil {
		return err
	}
	if unrecorded := mcp.Unrecorded(entries, lock.MCPServers); len(unrecorded) != 0 {
		service.notify("MCP server(s) %s have no current record in pack.lock and run as written; run `agentpack lock` to pin them", strings.Join(unrecorded, ", "))
	}
	return nil
}

// settleMCPServers builds the lock records for every MCP server the project
// stages, in any mode. A record is kept while the server's definition is
// unchanged, so an unchanged lock never needs the network.
func (service Service) settleMCPServers(ctx context.Context, projectRoot string, project *manifest.Manifest, lock lockfile.PackLock, previous []lockfile.MCPServer, pinMode mcpPinMode, allowUnpinned bool) ([]lockfile.MCPServer, error) {
	entries, err := staging.CollectMCP(projectRoot, lock, project, nil)
	if err != nil {
		return nil, err
	}
	var records []lockfile.MCPServer
	for _, name := range entries.Names() {
		entry := entries[name]
		record := mcp.Describe(name, entry)
		var kept *lockfile.MCPServer
		for index := range previous {
			if previous[index].Name == name && previous[index].Definition == record.Definition {
				kept = &previous[index]
			}
		}
		needsRegistry := record.Launcher == mcp.LauncherNPM && record.Status == ""
		// A kept record is asked again only where that can improve it: an
		// unpinned one on any lock, a dist-tag pin when refreshing.
		retry := needsRegistry && kept != nil && pinMode != mcpCarry &&
			(kept.Status != lockfile.MCPPinned || pinMode == mcpRefresh && kept.Version != mcp.NPMSpec(record))
		if kept != nil && !retry {
			carried := *kept
			carried.Source = record.Source
			records = append(records, carried)
			continue
		}
		if pinMode == mcpCarry {
			continue
		}
		if !needsRegistry {
			records = append(records, record)
			continue
		}
		record.Registry = mcp.NPMRegistry(entry.Server)
		version, integrity, err := mcp.ResolveNPM(ctx, service.client(), record.Registry, record.Package, mcp.NPMSpec(record))
		switch {
		case err == nil:
			if kept != nil && kept.Status == lockfile.MCPPinned && kept.Version == version && kept.Integrity != integrity {
				return nil, fmt.Errorf("MCP server %q: registry %s now reports integrity %s for %s@%s, but pack.lock recorded %s; nothing was written", name, record.Registry, integrity, record.Package, version, kept.Integrity)
			}
			record.Status, record.Version, record.Integrity = lockfile.MCPPinned, version, integrity
		case kept != nil && (kept.Status == lockfile.MCPPinned || kept.AllowUnpinned):
			service.notify("MCP server %q: kept its existing pack.lock record (%v)", name, err)
			record = *kept
			record.Source = string(entry.Source)
		case allowUnpinned:
			record.Status, record.AllowUnpinned = lockfile.MCPUnpinned, true
			record.Reason = "the registry could not be asked at lock time"
			service.notify("MCP server %q recorded as unpinned (%v)", name, err)
		default:
			return nil, fmt.Errorf("cannot pin MCP server %q (npm package %s): %w; nothing was written to pack.lock; retry when the registry is reachable, or run `agentpack lock --allow-unpinned-mcp` to record this server as unpinned", name, record.Requested, err)
		}
		records = append(records, record)
	}
	return records, nil
}

func (service Service) reportMCPServers(lock lockfile.PackLock) {
	var unpinned []string
	for _, server := range lock.MCPServers {
		if server.Status == lockfile.MCPUnpinned {
			unpinned = append(unpinned, server.Name)
		}
	}
	if len(unpinned) != 0 {
		service.notify("MCP server(s) %s are recorded as unpinned in pack.lock: what they run can change between launches", strings.Join(unpinned, ", "))
	}
}

// RefreshMCPRecords rewrites only the MCP records of an existing lock, without
// resolving packages. With pin set it also records servers that are new or
// changed (`mcp add`); without it it only drops records that no longer match
// (`mcp remove`), which needs no network.
func (service Service) RefreshMCPRecords(ctx context.Context, projectRoot string, pin, allowUnpinned bool) error {
	project, err := manifest.Load(projectRoot)
	if err != nil {
		return err
	}
	lock, err := loadCheckedLock(projectRoot)
	if os.IsNotExist(rootCause(err)) {
		return nil
	}
	if err != nil {
		return err
	}
	pinMode := mcpCarry
	if pin {
		pinMode = mcpResolve
	}
	records, err := service.settleMCPServers(ctx, projectRoot, project, lock, lock.MCPServers, pinMode, allowUnpinned)
	if err != nil {
		return err
	}
	lock.MCPServers = records
	if err := lock.Save(projectRoot); err != nil {
		return err
	}
	if pin {
		service.reportMCPServers(lock)
	}
	return nil
}

// ValidateLaunchInputs runs under the launch lease so a concurrent rebuild
// between sync and lease acquisition cannot launch another workspace's staging.
func (service *Service) ValidateLaunchInputs(definition, workspace string, effective mode.Effective, target base.Target) error {
	pending, err := (staging.Pipeline{ProjectRoot: definition, WorkspaceRoot: workspace, Mode: effective, Target: &target}).RebuildPending()
	if err != nil {
		return err
	}
	if pending {
		return fmt.Errorf("interrupted staging rebuild requires recovery; retry the launch or run env restore")
	}
	lock, err := loadCheckedLock(definition)
	if err != nil {
		return err
	}
	current, err := launchDigest(definition, workspace, effective, &target, lock)
	if err != nil {
		return err
	}
	stored, found, err := ReadLaunchDigest(definition, effective.Name())
	if err != nil {
		return err
	}
	if !found || stored != current {
		return fmt.Errorf("launch inputs changed before the staging lease was acquired; retry the launch")
	}
	return nil
}
