package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/OlegHQ/agentpack/internal/artifacts"
	"github.com/OlegHQ/agentpack/internal/harness/claude"
	"github.com/OlegHQ/agentpack/internal/harness/codex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/cache"
	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/harness/registry"
	"github.com/OlegHQ/agentpack/internal/hooks"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/staging"
)

// PreflightOptions selects the pure offline check surface.
type PreflightOptions struct {
	Mode           string
	Target         *base.Target
	Policy         environment.Policy
	StrictExternal bool
	WorkspaceRoot  string
	ContractPath   string // optional; empty loads <definition>/contract.json when present
	ReceiptID      string // optional; compare a prior native probe receipt (read-only)
}

// Preflight inspects an already-available definition without repairing cache,
// downloading, amending the lock, rebuilding staging, or writing overlays.
func (service Service) Preflight(definitionRoot string, options PreflightOptions) (environment.Report, error) {
	if options.Policy == "" {
		options.Policy = environment.PolicyLocal
	}
	workspace := options.WorkspaceRoot
	if workspace == "" {
		workspace = definitionRoot
	}
	report := environment.Report{
		SchemaVersion:  1,
		Phase:          "preflight",
		DefinitionRoot: definitionRoot,
		WorkspaceRoot:  workspace,
		Policy:         options.Policy,
		StrictExternal: options.StrictExternal,
		Capabilities:   map[string]string{},
		Coverage: []environment.CoverageNote{{
			Category: "skill", Scope: "native_catalog", Completeness: "none",
			Reason: "pure preflight does not execute native CLI discovery",
		}},
	}
	if options.Target != nil {
		report.Target = string(*options.Target)
	}
	for _, target := range base.AllTargets() {
		status, note := environment.ExternalCapability(target)
		report.Capabilities[string(target)] = status + ": " + note
	}

	project, err := manifest.Load(definitionRoot)
	if err != nil {
		return report, err
	}
	lock, err := loadCheckedLock(definitionRoot)
	if err != nil {
		return report, err
	}
	effective, err := resolveMode(definitionRoot, project, &lock, options.Mode)
	if err != nil {
		return report, err
	}
	report.Mode = effective.Name()
	report.Skills = lock.SkillCount()
	report.Plugins = lock.PluginCount()
	plugins := lock.Plugins()
	for _, skill := range lock.Skills() {
		if staging.SkillIsShadowed(skill, plugins) {
			report.Shadowed++
		}
	}
	if identity, err := sourceIdentity(definitionRoot, lock); err == nil {
		report.SourceIdentity = identity
	}
	if digest, err := lockDigest(definitionRoot); err == nil {
		report.LockDigest = digest
	}
	if binding, found, _ := environment.LoadBinding(workspace); found {
		report.Environment = binding.Environment
	}
	report.Artifacts, err = plannedArtifacts(lock, effective, workspace, options.Target)
	if err != nil {
		return report, fmt.Errorf("plan effective artifacts: %w", err)
	}
	report.Skills, report.Plugins = 0, 0
	for _, artifact := range report.Artifacts {
		if !artifact.Omitted {
			if artifact.Kind == "skill" {
				report.Skills++
			}
			if artifact.Kind == "plugin" {
				report.Plugins++
			}
		}
	}
	if err := populateFreshness(&report, options, effective); err != nil {
		return report, fmt.Errorf("fingerprint preflight inputs: %w", err)
	}
	appendAmbientFindings(&report, workspace, lock, options)
	for _, artifact := range report.Artifacts {
		if artifact.Omitted {
			continue
		}
		severity := environment.SeverityWarning
		if options.Policy == environment.PolicyCI {
			severity = environment.SeverityViolation
		}
		if preserved, ok := artifact.Properties["scope_preserved"].(bool); ok && !preserved {
			report.Findings = append(report.Findings, environment.Finding{Code: "RULE_SCOPE_DEGRADED", Severity: severity, Source: artifact.ID, Target: report.Target, Message: "scoped rule rendered as an unscoped skill; native glob enforcement is not preserved", Evidence: environment.EvidenceGenerated, Remedy: "choose a native rule target, disable this rule in the selected mode, or explicitly accept conversion under policy local"})
		}
		if fields, ok := artifact.Properties["dropped_fields"].([]string); ok && len(fields) > 0 {
			report.Findings = append(report.Findings, environment.Finding{Code: "ARTIFACT_FIELDS_DROPPED", Severity: severity, Source: artifact.ID, Target: report.Target, Message: "native rendering drops fields: " + strings.Join(fields, ", "), Evidence: environment.EvidenceGenerated, Remedy: "remove unsupported fields or choose a target that preserves them"})
		}
	}

	report.Inheritance = inheritanceNotes(options.Target, options.Policy)
	for _, note := range report.Inheritance {
		report.Findings = append(report.Findings, environment.Finding{Code: "INHERITANCE_BOUNDARY", Severity: environment.SeverityInfo, Message: note, Evidence: environment.EvidenceDeclared})
	}
	appendDetectedInheritance(&report, options)

	if options.StrictExternal {
		report.Findings = append(report.Findings, environment.Finding{
			Code:     "STRICT_EXTERNAL",
			Severity: environment.SeverityInfo,
			Message:  "strict external mode refuses workspace overlay writes",
			Remedy:   "launch claude or opencode, or omit --strict-external for compatibility mode",
		})
		if options.Target != nil && options.Target.UsesWorkspaceOverlay() {
			status, note := environment.ExternalCapability(*options.Target)
			report.Findings = append(report.Findings, environment.Finding{
				Code:     "WORKSPACE_WRITE_REQUIRED",
				Severity: environment.SeverityViolation,
				Target:   string(*options.Target),
				Message:  fmt.Sprintf("%s requires a workspace write (%s)", *options.Target, note),
				Remedy:   "choose claude/opencode, or run without --strict-external (compatibility mode)",
				Winner:   status,
			})
			report.PlannedWrites = append(report.PlannedWrites, plannedOverlayWrite(*options.Target, workspace)...)
		}
	} else if options.Target != nil && options.Target.UsesWorkspaceOverlay() {
		writes := plannedOverlayWrite(*options.Target, workspace)
		report.PlannedWrites = append(report.PlannedWrites, writes...)
		for _, write := range writes {
			report.Findings = append(report.Findings, environment.Finding{
				Code:     "WORKSPACE_WRITE_PLANNED",
				Severity: environment.SeverityWarning,
				Target:   string(*options.Target),
				Source:   write,
				Message:  "compatibility mode will write a workspace overlay",
				Remedy:   "add the path to .gitignore, or use --strict-external with claude/opencode",
			})
		}
	}

	if err := cache.VerifyLockCacheIntegrity(lock); err != nil {
		report.Findings = append(report.Findings, environment.Finding{
			Code:     "CACHE_INTEGRITY",
			Severity: environment.SeverityError,
			Message:  err.Error(),
			Remedy:   "run `agentpack sync --repair` (mutates cache) then re-run preflight",
		})
	} else if err := cache.VerifyLockCacheLayout(lock); err != nil {
		report.Findings = append(report.Findings, environment.Finding{
			Code:     "CACHE_MISSING",
			Severity: environment.SeverityError,
			Message:  err.Error(),
			Remedy:   "run `agentpack sync` or `agentpack env restore` to fetch locked inputs, then re-run preflight offline",
		})
	}

	for _, server := range lock.MCPServers {
		switch server.Status {
		case lockfile.MCPUnpinned, lockfile.MCPUnpinnable:
			severity := environment.SeverityWarning
			if options.Policy == environment.PolicyCI {
				severity = environment.SeverityViolation
			}
			report.Findings = append(report.Findings, environment.Finding{
				Code:     "MCP_UNPINNED",
				Severity: severity,
				Source:   "mcp:" + server.Name,
				Message:  fmt.Sprintf("MCP server %s is %s", server.Name, server.Status),
				Remedy:   "pin with `agentpack lock`, or accept explicitly under policy local",
			})
		}
	}
	if project != nil {
		for name, server := range project.MCP.Servers {
			for key := range server.Env {
				if !looksLikeSecret(key) {
					continue
				}
				if os.Getenv(key) == "" {
					report.Findings = append(report.Findings, environment.Finding{
						Code:     "SECRET_NAME_MISSING",
						Severity: environment.SeverityWarning,
						Source:   "mcp:" + name,
						Message:  fmt.Sprintf("required environment variable %s is unset (value not reported)", key),
						Remedy:   "export " + key + " in the process environment before launch",
					})
				}
			}
		}
	}

	if options.Target != nil {
		if path, lookErr := lookPathForTarget(*options.Target); lookErr != nil {
			report.Findings = append(report.Findings, environment.Finding{
				Code:     "BINARY_MISSING",
				Severity: environment.SeverityError,
				Target:   string(*options.Target),
				Message:  lookErr.Error(),
				Remedy:   "install the native CLI or set the harness path env var",
			})
		} else {
			report.Findings = append(report.Findings, environment.Finding{
				Code:     "BINARY_FOUND",
				Severity: environment.SeverityInfo,
				Target:   string(*options.Target),
				Source:   path,
				Message:  "native CLI located",
			})
		}
		hookFindings, err := collectHookFindings(workspace, definitionRoot, lock, effective, *options.Target)
		if err != nil {
			report.Findings = append(report.Findings, environment.Finding{
				Code:     "HOOK_COLLECT_FAILED",
				Severity: environment.SeverityWarning,
				Target:   string(*options.Target),
				Message:  err.Error(),
				Remedy:   "inspect hooks under packages and .agents/hooks",
			})
		} else {
			report.Findings = append(report.Findings, hookFindings...)
		}
	}

	pipeline := staging.Pipeline{
		ProjectRoot:    definitionRoot,
		WorkspaceRoot:  workspace,
		Lock:           lock,
		Manifest:       project,
		Mode:           effective,
		Target:         options.Target,
		StrictExternal: options.StrictExternal,
	}
	materialized, materializedFound, materializedErr := readMaterializationDigest(definitionRoot, effective.Name())
	if materializedErr != nil {
		report.Findings = append(report.Findings, environment.Finding{Code: "STAGING_UNREADABLE", Severity: environment.SeverityError, Message: materializedErr.Error(), Evidence: environment.EvidenceUnknown, Remedy: "run sync to restore materialization metadata"})
	} else if !materializedFound {
		report.Findings = append(report.Findings, environment.Finding{Code: "STAGING_INPUTS_UNKNOWN", Severity: environment.SeverityWarning, Message: "no materialization input identity is recorded", Evidence: environment.EvidenceUnknown, Remedy: "run sync before probing native configuration"})
	} else if current, err := computeMaterializationDigest(definitionRoot, workspace, effective); err != nil {
		return report, err
	} else if materialized != current {
		report.Findings = append(report.Findings, environment.Finding{Code: "STAGING_INPUTS_STALE", Severity: environment.SeverityViolation, Message: "staged configuration was built from different definition, mode, workspace, or inherited inputs", Evidence: environment.EvidenceGenerated, Remedy: "run sync to materialize current inputs before probing"})
	}
	drift, recorded, driftErr := pipeline.StagedDrift(true)
	if driftErr != nil {
		report.Findings = append(report.Findings, environment.Finding{
			Code:     "STAGING_UNREADABLE",
			Severity: environment.SeverityError,
			Message:  driftErr.Error(),
			Remedy:   "run `agentpack sync` to materialize staging, then re-run preflight",
		})
	} else if !recorded {
		report.Findings = append(report.Findings, environment.Finding{
			Code:     "STAGING_NOT_RECORDED",
			Severity: environment.SeverityWarning,
			Message:  fmt.Sprintf("no staged record for mode %q", effective.Name()),
			Remedy:   "run `agentpack sync` once to materialize, then preflight compares without rewriting",
		})
	} else if len(drift) != 0 {
		limit := min(len(drift), 5)
		report.Findings = append(report.Findings, environment.Finding{
			Code:     "STAGING_DRIFT",
			Severity: environment.SeverityError,
			Message:  fmt.Sprintf("%d staged file(s) differ from the last recorded sync", len(drift)),
			Source:   strings.Join(drift[:limit], "; "),
			Remedy:   "run `agentpack sync` to rebuild staging from the verified cache",
		})
	}

	if options.ReceiptID != "" {
		applyReceipt(&report, workspace, options)
	}

	if options.ContractPath != "" {
		contract, err := environment.LoadContract(options.ContractPath)
		if err != nil {
			return report, fmt.Errorf("load contract: %w", err)
		}
		applyContract(&report, contract)
	} else if path := filepath.Join(definitionRoot, "contract.json"); regularFile(path) {
		contract, err := environment.LoadContract(path)
		if err != nil {
			return report, fmt.Errorf("load contract: %w", err)
		}
		applyContract(&report, contract)
	}

	report.OverallStatus = overallStatus(report)
	report.OK = report.OverallStatus == "ready" || report.OverallStatus == "ready_with_warnings"
	return report, nil
}

// RestoreFrozen ensures locked cache trees exist and rebuilds staging without
// re-resolving pins or rewriting pack.lock. Missing locked inputs fail hard.
func (service Service) RestoreFrozen(ctx context.Context, definitionRoot string) error {
	return service.RestoreFrozenOptions(ctx, definitionRoot, SyncOptions{})
}

// RestoreFrozenOptions is RestoreFrozen with workspace / strict-external controls.
func (service Service) RestoreFrozenOptions(ctx context.Context, definitionRoot string, options SyncOptions) error {
	options.Frozen = true
	options.UpdateLock = false
	options.Repair = false
	options.DryRun = false
	options.VerifyOnly = false
	_, err := service.Sync(ctx, definitionRoot, options)
	return err
}

func sourceIdentity(definitionRoot string, lock lockfile.PackLock) (string, error) {
	hash := sha256.New()
	for _, path := range []string{paths.ManifestPath(definitionRoot), paths.LockPath(definitionRoot)} {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte(filepath.Base(path)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	for _, pkg := range lock.Packages {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", pkg.Module, pkg.Commit, pkg.ContentHash)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func lockDigest(definitionRoot string) (string, error) {
	data, err := os.ReadFile(paths.LockPath(definitionRoot))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func plannedArtifacts(lock lockfile.PackLock, effective mode.Effective, workspace string, target *base.Target) ([]environment.ArtifactRecord, error) {
	var records []environment.ArtifactRecord
	plugins := lock.Plugins()
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].CacheKey < plugins[j].CacheKey })
	skills := lock.Skills()
	sort.Slice(skills, func(i, j int) bool { return skills[i].CacheKey < skills[j].CacheKey })
	appendTree := func(root, module, bareName string, enabled bool, dot bool) error {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			allowed := false
			if dot {
				allowed, err = effective.AllowsDotAgentsPath(relative)
			} else {
				allowed, err = effective.AllowsPackagePath(module, relative)
			}
			if err != nil {
				return err
			}
			if extension := strings.ToLower(filepath.Ext(path)); extension != ".md" && extension != ".mdc" {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			parseRelative := relative
			if dot {
				if strings.HasPrefix(relative, "claude/") {
					if target != nil && *target != base.Claude {
						return nil
					}
					parseRelative = strings.TrimPrefix(relative, "claude/")
				}
				if strings.HasPrefix(relative, "codex/") {
					if target != nil && *target != base.Codex {
						return nil
					}
					parseRelative = strings.TrimPrefix(relative, "codex/")
				}
			}
			parsed, err := artifacts.Parse(parseRelative, string(contents), bareName)
			if err != nil {
				return fmt.Errorf("parse %s artifact: %w", module, err)
			}
			if parsed == nil {
				return nil
			}
			kind := map[artifacts.Kind]string{artifacts.Skill: "skill", artifacts.Command: "command", artifacts.Agent: "agent", artifacts.Rule: "rule"}[parsed.Kind]
			record := environment.ArtifactRecord{ID: module + ":" + relative, Kind: kind, Name: parsed.StorageName, Module: module, Winner: "package", Evidence: environment.EvidenceGenerated, Properties: map[string]any{"source_digest": digestBytes(contents)}}
			if dot {
				record.Winner = "project"
			}
			if !enabled || !allowed {
				record.Omitted = true
				record.Reason = "disabled by mode or lock"
			}
			if target != nil {
				rendered := parsed.Render(*target)
				if dot {
					switch *target {
					case base.Claude:
						rendered.RelativePath = parseRelative
						rendered.Contents = string(contents)
						if parsed.Kind == artifacts.Rule {
							rendered.RelativePath = "rules/dot-agents--" + strings.ReplaceAll(strings.TrimPrefix(parseRelative, "rules/"), "/", "--")
						}
					case base.Codex:
						if parsed.Kind == artifacts.Skill {
							rendered.RelativePath = parseRelative
							rendered.Contents = string(contents)
						}
						if parsed.Kind == artifacts.Rule {
							record.Evidence = environment.EvidenceDeclared
							rendered.RelativePath = ""
							rendered.Contents = ""
						}
					default:
						// shortcut: adapter-specific .agents seeding has no shared renderer, leave its projection unknown until modeled.
						record.Evidence = environment.EvidenceDeclared
						rendered.RelativePath = ""
						rendered.Contents = ""
					}
				}

				record.OutputPath = rendered.RelativePath
				record.Properties["output_path"] = rendered.RelativePath
				if rendered.RelativePath != "" {
					record.Properties["rendered_digest"] = digestBytes([]byte(rendered.Contents))
				}
				record.Properties["source_kind"] = kind
				if parsed.Kind == artifacts.Rule && strings.HasPrefix(rendered.RelativePath, "skills/") && len(parsed.Globs) > 0 {
					record.Properties["scope_preserved"] = false
				}
				projected, parseErr := artifacts.Parse(rendered.RelativePath, rendered.Contents, "")
				if parseErr != nil {
					return parseErr
				}
				var dropped []string
				for key := range parsed.ExtraFrontmatter {
					if rendered.RelativePath == "" {
						continue
					}
					if projected == nil {
						dropped = append(dropped, key)
					} else if _, ok := projected.ExtraFrontmatter[key]; !ok {
						dropped = append(dropped, key)
					}
				}
				sort.Strings(dropped)
				if len(dropped) > 0 {
					record.Properties["dropped_fields"] = dropped
				}

				if strings.HasPrefix(rendered.RelativePath, "skills/") {
					record.Kind = "skill"
				}
			}
			// Later sources overwrite the same native output, just as materialization does.
			if !record.Omitted {
				for index := range records {
					prior := &records[index]
					same := prior.Kind == record.Kind && prior.Name == record.Name
					if record.OutputPath != "" {
						same = prior.OutputPath == record.OutputPath
					}
					if same && !prior.Omitted {
						prior.Omitted = true
						prior.Winner = record.Winner
						prior.Reason = "overridden by " + record.ID
					}
				}
			}
			records = append(records, record)
			return nil
		})
	}
	disabled := func(key string) bool {
		for _, value := range lock.Config.DisabledPlugins {
			if value == key {
				return true
			}
		}
		return false
	}
	for _, plugin := range plugins {
		root, err := cache.EntryDir(plugin.CacheKey)
		if err != nil {
			return nil, err
		}
		name := plugin.Name
		if name == "" {
			name = staging.SkillFolderName(plugin)
		}
		record := environment.ArtifactRecord{ID: plugin.Module, Kind: "plugin", Name: name, Module: plugin.Module, Winner: "package", Evidence: environment.EvidenceDeclared, Properties: map[string]any{"source_digest": plugin.ContentHash}}
		if disabled(plugin.CacheKey) {
			record.Omitted = true
			record.Reason = "disabled by lock"
		}
		records = append(records, record)
		if err := appendTree(root, plugin.Module, "", !disabled(plugin.CacheKey), false); err != nil {
			return nil, err
		}
	}
	for _, skill := range skills {
		root, err := cache.EntryDir(skill.CacheKey)
		if err != nil {
			return nil, err
		}
		if err := appendTree(root, skill.Module, staging.SkillFolderName(skill), !disabled(skill.CacheKey) && !staging.SkillIsShadowed(skill, plugins), false); err != nil {
			return nil, err
		}
	}
	if err := appendTree(paths.ProjectDotAgentsDir(workspace), ".agents", "", true, true); err != nil {
		return nil, err
	}
	return records, nil
}

func markArtifactWinner(report *environment.Report, name, winner, reason string) {
	for index := range report.Artifacts {
		artifact := &report.Artifacts[index]
		if artifact.Kind != "skill" || artifact.Module == "user" || artifact.Module == "project" || !strings.EqualFold(artifact.Name, name) {
			continue
		}
		artifact.Winner = winner
		artifact.Omitted = true
		artifact.Reason = reason
		artifact.Evidence = environment.EvidenceGenerated
	}
}

func appendAmbientFindings(report *environment.Report, workspace string, lock lockfile.PackLock, options PreflightOptions) {
	packSkills := map[string]string{}
	for _, artifact := range report.Artifacts {
		if artifact.Kind == "skill" && !artifact.Omitted {
			packSkills[strings.ToLower(artifact.Name)] = artifact.Name
		}
	}
	home, _ := os.UserHomeDir()
	userSkills, err := staging.UserHomeSkillNames(home)
	if err != nil {
		report.Findings = append(report.Findings, environment.Finding{
			Code: "AMBIENT_SCAN_FAILED", Severity: environment.SeverityWarning,
			Message: err.Error(), Evidence: environment.EvidenceUnknown,
			Remedy: "fix permissions on ~/.claude/skills or ~/.grok/skills",
		})
		return
	}
	projectSkills, projectErr := staging.ProjectClaudeSkillNames(workspace)
	if projectErr != nil {
		report.Findings = append(report.Findings, environment.Finding{Code: "AMBIENT_SCAN_FAILED", Severity: environment.SeverityError, Message: projectErr.Error(), Evidence: environment.EvidenceUnknown})
		return
	}
	for _, layer := range []struct {
		names  map[string]struct{}
		winner string
	}{{userSkills, "user"}, {projectSkills, "project"}} {
		keys := make([]string, 0, len(layer.names))
		for key := range layer.names {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, lower := range keys {
			display := lower
			report.Artifacts = append(report.Artifacts, environment.ArtifactRecord{ID: layer.winner + ":" + display, Kind: "skill", Name: display, Module: layer.winner, Winner: layer.winner, Evidence: environment.EvidenceGenerated})
			if _, packaged := packSkills[lower]; !packaged {
				severity := environment.SeverityInfo
				if options.Policy == environment.PolicyCI {
					severity = environment.SeverityViolation
				}
				report.Findings = append(report.Findings, environment.Finding{Code: "AMBIENT_ARTIFACT", Severity: severity, Source: display, Winner: layer.winner, Message: "undeclared skill exists in a native discovery root", Evidence: environment.EvidenceGenerated, Remedy: "use a controlled home/workspace for CI or narrow the policy to local"})
			}
		}
	}

	for lower, display := range packSkills {
		if _, hit := userSkills[lower]; hit {
			severity := environment.SeverityWarning
			if options.Policy == environment.PolicyCI || options.StrictExternal {
				severity = environment.SeverityViolation
			}
			markArtifactWinner(report, display, "user", "user skill wins over packaged skill")
			report.Findings = append(report.Findings, environment.Finding{
				Code: "CONFIG_SHADOWED", Severity: severity, Source: display, Winner: "user",
				Message:  fmt.Sprintf("user skill %q overrides the packaged skill", display),
				Remedy:   "rename/remove the user skill, or accept under --policy local without --strict-external",
				Evidence: environment.EvidenceGenerated,
			})
		}
		if _, hit := projectSkills[lower]; hit {
			severity := environment.SeverityWarning
			if options.Policy == environment.PolicyCI {
				severity = environment.SeverityViolation
			}
			markArtifactWinner(report, display, "project", "project .claude/skills wins over packaged skill")
			report.Findings = append(report.Findings, environment.Finding{
				Code: "AMBIENT_ARTIFACT", Severity: severity, Source: display, Winner: "project",
				Message:  fmt.Sprintf("project .claude/skills/%s shadows the packaged skill", display),
				Remedy:   "remove the project skill, disable the package in a mode, or accept under --policy local",
				Evidence: environment.EvidenceGenerated,
			})
		}
	}
}

func applyReceipt(report *environment.Report, workspace string, options PreflightOptions) {
	receipt, err := environment.LoadReceipt(workspace, options.ReceiptID)
	if err != nil {
		report.Findings = append(report.Findings, environment.Finding{
			Code: "RECEIPT_STALE", Severity: environment.SeverityError,
			Message: err.Error(), Evidence: environment.EvidenceUnknown,
			Remedy: "run `agentpack probe --agent <target>` to create a fresh receipt",
		})
		return
	}
	stale := receipt.SourceIdentity != report.SourceIdentity || receipt.LockDigest != report.LockDigest || receipt.Mode != report.Mode || receipt.WorkspaceDigest != report.WorkspaceDigest || receipt.GenerationID != report.GenerationID || receipt.PolicyDigest != report.PolicyDigest
	expected := report.ExpectedTarget
	stale = stale || expected.Adapter == "" || receipt.Target.Adapter != expected.Adapter || receipt.Target.AdapterRevision != expected.AdapterRevision || receipt.Target.ExecutableIdentity != expected.ExecutableIdentity || receipt.Target.CapabilityRevision != expected.CapabilityRevision
	if stale {
		report.Findings = append(report.Findings, environment.Finding{Code: "RECEIPT_STALE", Severity: environment.SeverityViolation, Message: "receipt identities do not match current definition, workspace inputs, generation, policy, or native adapter", Evidence: environment.EvidenceUnknown, Remedy: "run `agentpack probe --agent <target>` again after sync/restore"})
		return
	}
	if receipt.Termination.Status != "completed" || receipt.Termination.NativeExitCode != 0 || (receipt.OverallStatus != "ready" && receipt.OverallStatus != "ready_with_warnings") {
		report.Findings = append(report.Findings, environment.Finding{Code: "PROBE_FAILED", Severity: environment.SeverityError, Message: "receipt does not describe a successful completed probe", Evidence: environment.EvidenceUnknown, Remedy: "resolve probe failures and run probe again"})
		return
	}
	for _, prop := range receipt.Properties {
		if prop.Evidence != environment.EvidenceObserved {
			continue
		}
		info, statErr := os.Stat(prop.EvidenceRef)
		digest := ""
		var err error
		if statErr == nil && info.Mode().IsRegular() && info.Size() <= 2<<20 {
			digest, err = environment.ExecutableIdentity(prop.EvidenceRef)
		} else {
			err = fmt.Errorf("native evidence must be a bounded regular file")
		}
		if prop.EvidenceRef == "" || prop.EvidenceDigest == "" || err != nil || digest != prop.EvidenceDigest {
			report.Findings = append(report.Findings, environment.Finding{Code: "OBSERVATION_UNAVAILABLE", Severity: environment.SeverityError, Message: "native evidence is unavailable or differs from its recorded digest", Evidence: environment.EvidenceUnknown, Remedy: "run probe again to retain current diagnostic evidence"})
			return
		}
	}
	// Complete coverage alone never manufactures positive observations.
	report.Coverage = receipt.Coverage
	report.Properties = receipt.Properties

	for _, prop := range receipt.Properties {
		if prop.Evidence != environment.EvidenceObserved {
			continue
		}
		report.Findings = append(report.Findings, environment.Finding{
			Code: "NATIVE_OBSERVED", Severity: environment.SeverityInfo,
			Source: prop.ArtifactID, Message: fmt.Sprintf("observed %s=%v via %s", prop.Property, prop.Value, prop.Method),
			Evidence: environment.EvidenceObserved,
		})
	}
}

func applyContract(report *environment.Report, contract environment.Contract) {
	results := environment.EvaluateContract(contract, *report)
	report.ContractResults = results
	unknownMode := strings.ToLower(strings.TrimSpace(contract.UnknownRequired))
	if unknownMode == "" {
		unknownMode = "fail"
	}
	requirements := map[string]environment.Requirement{}
	for _, req := range contract.Requirements {
		requirements[req.ID] = req
	}
	for _, result := range results {
		reqSeverity := requirements[result.RequirementID].Severity
		violationSeverity := environment.SeverityViolation
		if reqSeverity == environment.SeverityInfo || reqSeverity == environment.SeverityWarning {
			violationSeverity = reqSeverity
		}
		switch result.Result {
		case "violated":
			report.Findings = append(report.Findings, environment.Finding{
				Code: result.FindingCode, Severity: violationSeverity,
				Message: result.Message, Remedy: "adjust the environment or add a scoped allowance with rationale",
				Evidence: environment.EvidenceGenerated,
			})
		case "unknown":
			severity := environment.SeverityWarning
			if unknownMode == "fail" {
				severity = violationSeverity
			} else if unknownMode == "allow" {
				severity = environment.SeverityInfo
			}
			code := result.FindingCode
			if code == "" {
				code = "OBSERVATION_UNAVAILABLE"
			}
			report.Findings = append(report.Findings, environment.Finding{
				Code: code, Severity: severity, Message: result.Message,
				Remedy:   "run an explicit native probe, or narrow the contract to generated evidence",
				Evidence: environment.EvidenceUnknown,
			})
		}
	}
}

func overallStatus(report environment.Report) string {
	hasViolation, hasError, hasBlockingUnknown, hasWarning := false, false, false, false
	for _, finding := range report.Findings {
		switch finding.Severity {
		case environment.SeverityViolation:
			hasViolation = true
		case environment.SeverityError:
			hasError = true
		case environment.SeverityWarning:
			hasWarning = true
		}
		if finding.Evidence == environment.EvidenceUnknown &&
			(finding.Severity == environment.SeverityViolation || finding.Severity == environment.SeverityError) {
			hasBlockingUnknown = true
		}
	}
	switch {
	case hasError:
		return "error"
	case hasBlockingUnknown:
		return "unknown"
	case hasViolation:
		return "violated"
	case hasWarning:
		return "ready_with_warnings"
	default:
		return "ready"
	}
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func inheritanceNotes(target *base.Target, policy environment.Policy) []string {
	notes := []string{
		"credentials, remote MCP services, npm/npx installs, and model answers stay outside the portable closure",
	}
	if target == nil {
		return notes
	}
	switch *target {
	case base.Claude:
		notes = append(notes, "Claude still reads real ~/.claude.json and ~/.claude/settings.json; agentpack only adds --plugin-dir/--settings")
	case base.OpenCode:
		notes = append(notes, "OpenCode config root is redirected; user files may be seeded into the staged root when present")
	case base.Codex:
		notes = append(notes, "Codex keeps native history/auth continuity under CODEX_HOME bridging")
	case base.Grok:
		notes = append(notes, "Grok reads real ~/.grok and ~/.claude in addition to GROK_HOME")
	case base.Cursor:
		notes = append(notes, "Cursor fake HOME bridges user cli-config/mcp and may write .cursor/agents in the workspace")
	case base.Agy:
		notes = append(notes, "Antigravity leaves HOME untouched and writes a workspace plugin overlay")
	}
	if policy == environment.PolicyCI {
		notes = append(notes, "CI policy treats undeclared inheritance as a violation")
	}
	return notes
}

func plannedOverlayWrite(target base.Target, workspace string) []string {
	switch target {
	case base.Cursor:
		return []string{filepath.Join(workspace, ".cursor", "agents")}
	case base.Agy:
		return []string{filepath.Join(workspace, ".agents", "plugins", "agentpack-bundle")}
	default:
		return nil
	}
}

func collectHookFindings(workspace, definitionRoot string, lock lockfile.PackLock, effective mode.Effective, target base.Target) ([]environment.Finding, error) {
	bundle, err := hooks.Collect(workspace, lock, "", effective)
	if err != nil {
		return nil, err
	}
	renderer := registry.Renderer(target)
	if renderer == nil || len(bundle.Hooks) == 0 {
		return nil, nil
	}
	root, err := paths.StagingRootForMode(definitionRoot, effective.Name())
	if err != nil {
		return nil, err
	}
	output, err := renderer.Render(bundle, hooks.RenderContext{ProjectRoot: definitionRoot, TargetRoot: root})
	if err != nil {
		return nil, err
	}
	var findings []environment.Finding
	for _, diagnostic := range output.Diagnostics {
		severity := environment.SeverityWarning
		code := "HOOK_DIAGNOSTIC"
		switch strings.ToLower(diagnostic.Level) {
		case "error":
			severity = environment.SeverityError
			code = "HOOK_ERROR"
		case "omitted", "unsupported":
			code = "HOOK_UNSUPPORTED"
		case "degraded":
			code = "HOOK_DEGRADED"
		}
		findings = append(findings, environment.Finding{
			Code:     code,
			Severity: severity,
			Source:   diagnostic.Source,
			Target:   string(target),
			Message:  diagnostic.Message,
			Remedy:   "disable the hook, choose a target with native support, or accept the finding under policy local",
		})
	}
	if output.Summary.Omitted != 0 || output.Summary.Degraded != 0 || output.Summary.Emulated != 0 {
		findings = append(findings, environment.Finding{
			Code:     "HOOK_SUMMARY",
			Severity: environment.SeverityInfo,
			Target:   string(target),
			Message:  fmt.Sprintf("hooks: native=%d emulated=%d degraded=%d omitted=%d", output.Summary.Native, output.Summary.Emulated, output.Summary.Degraded, output.Summary.Omitted),
		})
	}
	return findings, nil
}

func lookPathForTarget(target base.Target) (string, error) {
	envName, binary := "", string(target)
	switch target {
	case base.Claude:
		envName, binary = "CLAUDE_CODE_PATH", "claude"
	case base.OpenCode:
		envName, binary = "OPENCODE_PATH", "opencode"
	case base.Codex:
		envName, binary = "CODEX_PATH", "codex"
	case base.Grok:
		envName, binary = "GROK_PATH", "grok"
	case base.Cursor:
		envName, binary = "CURSOR_AGENT_PATH", "cursor-agent"
	case base.Agy:
		envName, binary = "AGY_PATH", "agy"
	}
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		info, err := os.Stat(value)
		if err != nil {
			return "", fmt.Errorf("native CLI configured by %s is unavailable: %w", envName, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("native CLI configured by %s is not a regular file", envName)
		}
		return value, nil
	}
	return exec.LookPath(binary)
}

func looksLikeSecret(key string) bool {
	upper := strings.ToUpper(key)
	return strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD")
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func populateFreshness(report *environment.Report, options PreflightOptions, effective mode.Effective) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	inputs := []string{filepath.Join(report.WorkspaceRoot, ".agents"), filepath.Join(report.WorkspaceRoot, ".claude"), filepath.Join(report.WorkspaceRoot, ".cursor"), filepath.Join(report.WorkspaceRoot, "AGENTS.md"), filepath.Join(report.WorkspaceRoot, "CLAUDE.md"), filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".grok", "skills")}
	inputs = append(inputs, inheritedConfigPaths(options.Target)...)
	report.WorkspaceDigest, err = fingerprintInputs(inputs)
	if err != nil {
		return err
	}
	stagedPath, err := paths.StagedManifestPath(report.DefinitionRoot, effective.Name())
	if err != nil {
		return err
	}
	stagedBytes, err := os.ReadFile(stagedPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var stagedConfigs []string
	if options.Target != nil {
		candidate, err := registry.ByTarget(*options.Target)
		if err != nil {
			return err
		}
		ctx := base.StageContext{ProjectRoot: report.DefinitionRoot, WorkspaceRoot: report.WorkspaceRoot, Mode: effective}
		root, err := candidate.StagedRoot(ctx)
		if err != nil {
			return err
		}
		for _, name := range []string{"config.toml", "opencode.json", "cli-config.json", "AGENTS.md", "CLAUDE.md", "plugin.json", ".claude-plugin/plugin.json", "hooks.json", ".mcp.json", "mcp.json", "mcp_config.json"} {
			stagedConfigs = append(stagedConfigs, filepath.Join(root, filepath.FromSlash(name)))
		}
		if *options.Target == base.Claude {
			path, err := claude.SettingsPath(ctx)
			if err != nil {
				return err
			}
			stagedConfigs = append(stagedConfigs, path)
		}
	}
	stagedConfigDigest, err := fingerprintInputs(stagedConfigs)
	if err != nil {
		return err
	}
	report.GenerationID = digestBytes(append([]byte(report.SourceIdentity+"\x00"+effective.FingerprintMaterial()+"\x00"+report.WorkspaceDigest+"\x00"+stagedConfigDigest+"\x00"), stagedBytes...))
	contractPath := options.ContractPath
	if contractPath == "" {
		contractPath = filepath.Join(report.DefinitionRoot, "contract.json")
	}
	contractBytes, err := os.ReadFile(contractPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	report.PolicyDigest = digestBytes(append([]byte(fmt.Sprintf("policy=%s\nstrict=%t\n", options.Policy, options.StrictExternal)), contractBytes...))
	if options.Target != nil {
		report.ExpectedTarget.Adapter = string(*options.Target)
		var capability map[string]string
		switch *options.Target {
		case base.Claude:
			capability = claude.Capability()
		case base.Codex:
			capability = codex.Capability()
		}
		report.ExpectedTarget.AdapterRevision = capability["adapter_revision"]
		report.ExpectedTarget.CapabilityRevision = capability["capability_revision"]
		if path, err := lookPathForTarget(*options.Target); err == nil {
			identity, err := environment.ExecutableIdentity(path)
			if err == nil {
				report.ExpectedTarget.ExecutableIdentity = identity
			}
		}
	}
	return nil
}

// Fingerprints include absence and file bytes; absolute paths stay local to this identity.
func fingerprintInputs(inputs []string) (string, error) {
	hash := sha256.New()
	active := map[string]bool{}
	var visit func(string, string) error
	visit = func(path, relative string) error {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			fmt.Fprintf(hash, "%q absent\n", relative)
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%q mode=%s size=%d\n", relative, info.Mode(), info.Size())
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(hash, "link=%q\n", link)
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			return visit(resolved, relative+"/resolved")
		}
		if info.IsDir() {
			absolute, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if active[absolute] {
				return fmt.Errorf("cyclic configuration symlink")
			}
			active[absolute] = true
			defer delete(active, absolute)
			entries, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := visit(filepath.Join(path, entry.Name()), relative+"/"+entry.Name()); err != nil {
					return err
				}
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("nonregular configuration input %s", relative)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(hash, file)
		hash.Write([]byte{0})
		return err
	}
	for index, root := range inputs {
		if err := visit(root, fmt.Sprintf("input-%d", index)); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func inheritedConfigPaths(target *base.Target) []string {
	if target == nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	var names []string
	switch *target {
	case base.Claude:
		names = []string{".claude.json", ".claude/settings.json"}
	case base.OpenCode:
		names = []string{".config/opencode/opencode.json", ".config/opencode/opencode.jsonc"}
	case base.Codex:
		names = []string{".codex/config.toml", ".codex/AGENTS.md"}
	case base.Grok:
		names = []string{".grok/config.toml", ".grok/hooks", ".claude/settings.json"}
	case base.Cursor:
		names = []string{".cursor/cli-config.json", ".cursor/mcp.json"}
	case base.Agy:
		names = []string{".gemini/settings.json", ".gemini/GEMINI.md"}
	}
	var result []string
	for _, name := range names {
		result = append(result, filepath.Join(home, filepath.FromSlash(name)))
	}
	return result
}

func appendDetectedInheritance(report *environment.Report, options PreflightOptions) {
	for _, path := range inheritedConfigPaths(options.Target) {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			report.Findings = append(report.Findings, environment.Finding{Code: "AMBIENT_SCAN_FAILED", Severity: environment.SeverityError, Source: path, Message: "cannot inspect inherited configuration", Evidence: environment.EvidenceUnknown})
			continue
		}
		if !info.IsDir() && info.Size() == 0 {
			continue
		}
		severity := environment.SeverityWarning
		if options.Policy == environment.PolicyCI {
			severity = environment.SeverityViolation
		}
		report.Findings = append(report.Findings, environment.Finding{Code: "INHERITED_CONFIG", Severity: severity, Source: path, Message: "native launch may read existing undeclared configuration", Evidence: environment.EvidenceGenerated, Remedy: "use a controlled account/configuration home for CI, or use policy local"})
	}
}
