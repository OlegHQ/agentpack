package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/paths"
)

const (
	capabilityRevision = "claude-probe-v2"
	adapterRevision    = "claude-observe-2026-10"
)

// Capability describes validated Claude observation support for this revision.
func Capability() map[string]string {
	return map[string]string{
		"adapter_revision":    adapterRevision,
		"capability_revision": capabilityRevision,
		"validated_versions":  "2.1.295, 2.1.296",
		"plugin_presence":     "observed via `claude --bare --plugin-dir <bundle> plugin list --json`",
		"skill_presence":      "observed via `claude plugin details <id>` inventory (names only; no source bytes)",
		"skill_source_digest": "unknown — native interface does not attribute source bytes",
		"ambient_user_skills": "unknown — plugin list does not enumerate ~/.claude/skills",
		"effects":             "isolated HOME and CLAUDE_CONFIG_DIR; no model inference requested; native network/other effects unmeasured",
	}
}

// ProbeBundle runs a bounded Claude plugin/skill catalog probe against a staged bundle.
func ProbeBundle(ctx context.Context, bundleDir, workspace, sourceID, lockDigest, mode string) (environment.Receipt, error) {
	exe, err := base.LookPathEnv("CLAUDE_CODE_PATH", "claude")
	if err != nil {
		return environment.Receipt{}, err
	}
	version, versionResult, versionErr := nativeVersion(ctx, exe)
	identity, identityErr := environment.ExecutableIdentity(exe)
	if identityErr != nil {
		return environment.Receipt{}, identityErr
	}
	receipt := environment.Receipt{
		SchemaVersion:  1,
		Phase:          "probe",
		ReceiptID:      fmt.Sprintf("claude-%d", time.Now().UTC().UnixNano()),
		SourceIdentity: sourceID,
		LockDigest:     lockDigest,
		Mode:           mode,
		Target: environment.ReceiptTarget{
			Adapter:            "claude",
			AdapterRevision:    adapterRevision,
			ExecutableIdentity: identity,
			NativeVersion:      version,
			CapabilityRevision: capabilityRevision,
		},
		CreatedAt: time.Now().UTC(),
		Effects: environment.ProbeEffects{
			ModelCalls: false,
			Network:    "not_measured",
			WriteScope: "managed_diagnostic_home; native_other_effects_unmeasured",
		},
		Termination: environment.Termination{Status: "completed", NativeExitCode: versionResult.ExitCode},
	}
	versionEvidence, saveErr := base.SaveProbeEvidence(workspace, receipt.ReceiptID, "version", versionResult)
	if saveErr != nil {
		return receipt, saveErr
	}
	receipt.DiagnosticRefs = append(receipt.DiagnosticRefs, versionEvidence)

	if versionErr != nil {
		err = versionErr
	}
	if err != nil {
		receipt.Termination.Status = "failed"
		receipt.Findings = append(receipt.Findings, environment.Finding{
			Code: "PROBE_FAILED", Severity: environment.SeverityError, Target: "claude",
			Message: err.Error(), Evidence: environment.EvidenceUnknown,
			Remedy: "install claude or set CLAUDE_CODE_PATH; re-run agentpack probe --agent claude",
		})
		receipt.OverallStatus = "error"
		return receipt, nil
	}

	if version != "2.1.295 (Claude Code)" && version != "2.1.295" && version != "2.1.296 (Claude Code)" && version != "2.1.296" {
		receipt.Coverage = []environment.CoverageNote{{Category: "plugin", Scope: "controlled_projection", Completeness: "none", Reason: "native version is outside validated parser coverage"}}
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: "OBSERVATION_UNAVAILABLE", Severity: environment.SeverityWarning, Target: "claude", Message: "Claude native version is not validated: " + version, Evidence: environment.EvidenceUnknown, Remedy: "use the documented validated native version or validate and update parser fixtures"})
		receipt.OverallStatus = "unknown"
		return receipt, nil
	}

	diagnosticRoot, err := paths.ProjectStateFile(workspace, "probe-diagnostics")
	if err != nil {
		return receipt, err
	}
	if err = os.MkdirAll(diagnosticRoot, 0700); err != nil {
		return receipt, err
	}
	diagnostic, err := os.MkdirTemp(diagnosticRoot, "claude-")
	if err != nil {
		return receipt, err
	}
	defer os.RemoveAll(diagnostic)
	diagnosticEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + diagnostic, "CLAUDE_CONFIG_DIR=" + filepath.Join(diagnostic, "claude"), "XDG_CONFIG_HOME=" + filepath.Join(diagnostic, "config"), "XDG_DATA_HOME=" + filepath.Join(diagnostic, "data"), "XDG_CACHE_HOME=" + filepath.Join(diagnostic, "cache")}
	if os.Getenv("SystemRoot") != "" {
		diagnosticEnv = append(diagnosticEnv, "SystemRoot="+os.Getenv("SystemRoot"))
	}

	listPlan := base.ProbePlan{
		Executable: exe,
		Argv:       []string{"--bare", "--plugin-dir", bundleDir, "plugin", "list", "--json"},
		Cwd:        workspace,
		Env:        diagnosticEnv,
		Timeout:    45 * time.Second,
		Categories: []string{"plugin"},
		Network:    "none_required",
		WriteScope: "managed_diagnostic_storage",
		ModelCalls: false,
	}
	listResult, err := base.RunProbe(ctx, listPlan)
	listEvidence, saveErr := base.SaveProbeEvidence(workspace, receipt.ReceiptID, "plugin-list", listResult)
	if saveErr != nil {
		return receipt, saveErr
	}
	receipt.DiagnosticRefs = append(receipt.DiagnosticRefs, listEvidence)
	receipt.Termination.NativeExitCode = listResult.ExitCode
	if err != nil {
		receipt.Termination.Status = "failed"
		receipt.OverallStatus = "error"
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: "PROBE_FAILED", Severity: environment.SeverityError, Target: "claude", Message: err.Error(), Evidence: environment.EvidenceUnknown, Remedy: "verify native CLI and staged plugin; rerun explicit probe"})
		return receipt, nil
	}

	var plugins []pluginEntry
	if err := json.Unmarshal([]byte(listResult.Stdout), &plugins); err != nil || plugins == nil || invalidPluginEntries(plugins) {
		receipt.Coverage = []environment.CoverageNote{{
			Category: "plugin", Scope: "controlled_projection", Completeness: "partial",
			Reason: "plugin list JSON could not be parsed",
		}}
		receipt.Findings = append(receipt.Findings, environment.Finding{
			Code: "OBSERVATION_PARTIAL", Severity: environment.SeverityError, Target: "claude",
			Message: "native plugin inventory schema is invalid or incomplete", Evidence: environment.EvidenceUnknown,
		})
		receipt.OverallStatus = "unknown"
		return receipt, nil
	}

	receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "controlled_projection", Category: "plugin", Property: "catalog_complete", Value: true, Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: listEvidence})
	pluginPresent := false
	pluginID := ""
	for _, plugin := range plugins {
		if plugin.ID == "agentpack-bundle" || strings.HasPrefix(plugin.ID, "agentpack-bundle@") {
			pluginPresent = true
			pluginID = plugin.ID
			break
		}
	}
	receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "controlled_projection",
		Category: "plugin", ArtifactID: "agentpack-bundle", Property: "presence", Value: pluginPresent,
		Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: listEvidence,
	})
	if !pluginPresent {
		receipt.Coverage = []environment.CoverageNote{{
			Category: "plugin", Scope: "controlled_projection", Completeness: "complete",
			Reason: "plugin list returned successfully; agentpack-bundle not present",
		}, {
			Category: "skill", Scope: "controlled_projection", Completeness: "none",
			Reason: "skill inventory requires a discovered plugin id",
		}}
		receipt.Findings = append(receipt.Findings, environment.Finding{
			Code: "OBSERVATION_UNAVAILABLE", Severity: environment.SeverityWarning, Target: "claude",
			Message:  "agentpack-bundle was not listed by the native plugin catalog",
			Evidence: environment.EvidenceObserved,
			Remedy:   "run agentpack sync then probe again with the staged Claude bundle",
		})
		receipt.OverallStatus = "ready_with_warnings"
		return receipt, nil
	}

	detailsPlan := base.ProbePlan{
		Executable: exe,
		Argv:       []string{"--bare", "--plugin-dir", bundleDir, "plugin", "details", pluginID},
		Cwd:        workspace,
		Env:        diagnosticEnv,
		Timeout:    45 * time.Second,
		Categories: []string{"skill"},
		Network:    "none_required",
		WriteScope: "managed_diagnostic_storage",
	}
	details, err := base.RunProbe(ctx, detailsPlan)
	detailsEvidence, saveErr := base.SaveProbeEvidence(workspace, receipt.ReceiptID, "plugin-details", details)
	if saveErr != nil {
		return receipt, saveErr
	}
	receipt.DiagnosticRefs = append(receipt.DiagnosticRefs, detailsEvidence)
	if err != nil || details.ExitCode != 0 {
		receipt.Termination.Status = "failed"
		receipt.Termination.NativeExitCode = details.ExitCode
		receipt.Coverage = append(receipt.Coverage, environment.CoverageNote{
			Category: "skill", Scope: "controlled_projection", Completeness: "partial",
			Reason: "plugin details failed; skill inventory incomplete",
		})
		receipt.Findings = append(receipt.Findings, environment.Finding{
			Code: "OBSERVATION_PARTIAL", Severity: environment.SeverityWarning, Target: "claude",
			Message:  "plugin present but skill inventory unavailable",
			Evidence: environment.EvidenceUnknown,
		})
		receipt.OverallStatus = "unknown"
		return receipt, nil
	}
	receipt.Termination.NativeExitCode = details.ExitCode
	skills, parseErr := parseSkillInventory(details.Stdout)
	if parseErr != nil {
		receipt.Coverage = []environment.CoverageNote{{Category: "skill", Scope: "controlled_projection", Completeness: "partial", Reason: parseErr.Error()}}
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: "OBSERVATION_PARTIAL", Severity: environment.SeverityError, Target: "claude", Message: parseErr.Error(), Evidence: environment.EvidenceUnknown})
		receipt.OverallStatus = "unknown"
		return receipt, nil
	}
	receipt.Coverage = []environment.CoverageNote{{
		Category: "plugin", Scope: "controlled_projection", Completeness: "complete",
	}, {
		Category: "skill", Scope: "controlled_projection", Completeness: "partial",
		Reason: "names observed in isolated plugin inventory; ambient user plugins and skills excluded; source bytes/digests not attributed by Claude",
	}}
	for _, skill := range skills {
		receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "controlled_projection",
			Category: "skill", ArtifactID: filepath.Join("agentpack-bundle", "skills", skill),
			Property: "presence", Value: true,
			Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: detailsEvidence,
		})
		receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "controlled_projection",
			Category: "skill", ArtifactID: filepath.Join("agentpack-bundle", "skills", skill),
			Property: "source_digest", Value: nil,
			Evidence: environment.EvidenceUnknown, Reason: "Native interface does not attribute source bytes.",
		})
	}
	receipt.OverallStatus = "ready_with_warnings"
	return receipt, nil
}

func nativeVersion(ctx context.Context, exe string) (string, base.ProbeResult, error) {
	result, err := base.RunProbe(ctx, base.ProbePlan{
		Executable: exe, Argv: []string{"--version"}, Timeout: 10 * time.Second,
	})
	if err != nil || result.ExitCode != 0 {
		return "", result, fmt.Errorf("Claude version probe failed: %w", err)
	}
	version := strings.TrimSpace(result.Stdout)
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?: \(Claude Code\))?$`).MatchString(version) {
		return "", result, fmt.Errorf("Claude version probe returned an unexpected version format")
	}
	return version, result, nil
}

var skillLine = regexp.MustCompile(`(?m)^\s*Skills\s*\((\d+)\)\s*(.*)$`)

type pluginEntry struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

func invalidPluginEntries(entries []pluginEntry) bool {
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.ID == "" || seen[entry.ID] {
			return true
		}
		seen[entry.ID] = true
	}
	return false
}

func parseSkillInventory(text string) ([]string, error) {
	matches := skillLine.FindAllStringSubmatch(text, -1)
	if len(matches) != 1 {
		return nil, fmt.Errorf("native skill inventory must contain exactly one Skills count")
	}
	count, err := strconv.Atoi(matches[0][1])
	if err != nil {
		return nil, fmt.Errorf("invalid native skill inventory count")
	}
	names := strings.Fields(strings.ReplaceAll(matches[0][2], ",", " "))
	if len(names) != count {
		return nil, fmt.Errorf("native skill inventory count does not match names")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if strings.ContainsAny(name, "/\\") || name == "." || name == ".." || seen[name] {
			return nil, fmt.Errorf("invalid or duplicate native skill inventory name")
		}
		seen[name] = true
	}
	return names, nil
}
