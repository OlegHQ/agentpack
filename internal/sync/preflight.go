package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	report.Artifacts = plannedArtifacts(lock)
	appendAmbientFindings(&report, workspace, lock, options)

	report.Inheritance = inheritanceNotes(options.Target, options.Policy)
	for index, note := range report.Inheritance {
		severity := environment.SeverityInfo
		// Target-specific inheritance is a CI warning (named, not silent). It
		// becomes a violation only with --strict-external, which claims no
		// undeclared configuration influence beyond the portable inputs.
		if index > 0 && options.Policy == environment.PolicyCI {
			severity = environment.SeverityWarning
			if options.StrictExternal {
				severity = environment.SeverityViolation
			}
		}
		report.Findings = append(report.Findings, environment.Finding{
			Code:     "INHERITED_CONFIG",
			Severity: severity,
			Message:  note,
			Remedy:   "use --policy local to accept inheritance, or narrow the claim with fixtures that prove exclusion",
		})
	}

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

func plannedArtifacts(lock lockfile.PackLock) []environment.ArtifactRecord {
	plugins := lock.Plugins()
	var artifacts []environment.ArtifactRecord
	for _, skill := range lock.Skills() {
		name := staging.SkillFolderName(skill)
		record := environment.ArtifactRecord{
			ID: skill.Module, Kind: "skill", Name: name, Module: skill.Module,
			Winner: "package", Evidence: environment.EvidenceDeclared,
		}
		if staging.SkillIsShadowed(skill, plugins) {
			record.Omitted = true
			record.Winner = "plugin"
			record.Reason = "shadowed by containing plugin"
		}
		artifacts = append(artifacts, record)
	}
	for _, plugin := range plugins {
		name := plugin.Name
		if name == "" {
			name = staging.SkillFolderName(plugin)
		}
		artifacts = append(artifacts, environment.ArtifactRecord{
			ID: plugin.Module, Kind: "plugin", Name: name, Module: plugin.Module,
			Winner: "package", Evidence: environment.EvidenceDeclared,
		})
	}
	return artifacts
}

func markArtifactWinner(report *environment.Report, name, winner, reason string) {
	for index := range report.Artifacts {
		artifact := &report.Artifacts[index]
		if artifact.Kind != "skill" || !strings.EqualFold(artifact.Name, name) {
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
	plugins := lock.Plugins()
	for _, skill := range lock.Skills() {
		if staging.SkillIsShadowed(skill, plugins) {
			continue
		}
		name := staging.SkillFolderName(skill)
		packSkills[strings.ToLower(name)] = name
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
	projectSkills, _ := staging.ProjectClaudeSkillNames(workspace)
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
	stale := false
	if report.SourceIdentity != "" && receipt.SourceIdentity != "" && report.SourceIdentity != receipt.SourceIdentity {
		stale = true
	}
	if report.LockDigest != "" && receipt.LockDigest != "" && report.LockDigest != receipt.LockDigest {
		stale = true
	}
	if report.Mode != "" && receipt.Mode != "" && report.Mode != receipt.Mode {
		stale = true
	}
	if options.Target != nil && receipt.Target.Adapter != "" && string(*options.Target) != receipt.Target.Adapter {
		stale = true
	}
	if stale {
		report.Findings = append(report.Findings, environment.Finding{
			Code: "RECEIPT_STALE", Severity: environment.SeverityViolation,
			Message:  "receipt identities do not match the current definition/mode/target",
			Evidence: environment.EvidenceGenerated,
			Remedy:   "run `agentpack probe --agent <target>` again after sync/restore",
		})
		return
	}
	if len(receipt.Coverage) > 0 {
		report.Coverage = receipt.Coverage
	}
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
	for _, result := range results {
		switch result.Result {
		case "violated":
			report.Findings = append(report.Findings, environment.Finding{
				Code: result.FindingCode, Severity: environment.SeverityViolation,
				Message: result.Message, Remedy: "adjust the environment or add a scoped allowance with rationale",
				Evidence: environment.EvidenceGenerated,
			})
		case "unknown":
			severity := environment.SeverityWarning
			if unknownMode == "fail" {
				severity = environment.SeverityViolation
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
		return value, nil
	}
	return exec.LookPath(binary)
}

func looksLikeSecret(key string) bool {
	upper := strings.ToUpper(key)
	return strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD")
}
