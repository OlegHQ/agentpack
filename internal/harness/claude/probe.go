package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
)

const (
	capabilityRevision = "claude-probe-v1"
	adapterRevision    = "claude-observe-2026-10"
)

// Capability describes validated Claude observation support for this revision.
func Capability() map[string]string {
	return map[string]string{
		"adapter_revision":    adapterRevision,
		"capability_revision": capabilityRevision,
		"validated_versions":  "2.1.295",
		"plugin_presence":     "observed via `claude --bare --plugin-dir <bundle> plugin list --json`",
		"skill_presence":      "observed via `claude plugin details <id>` inventory (names only; no source bytes)",
		"skill_source_digest": "unknown — native interface does not attribute source bytes",
		"ambient_user_skills": "unknown — plugin list does not enumerate ~/.claude/skills",
		"effects":             "no model calls with --bare; writes limited to managed diagnostic storage",
	}
}

// ProbeBundle runs a bounded Claude plugin/skill catalog probe against a staged bundle.
func ProbeBundle(ctx context.Context, bundleDir, workspace, sourceID, lockDigest, mode string) (environment.Receipt, error) {
	exe, err := base.LookPathEnv("CLAUDE_CODE_PATH", "claude")
	if err != nil {
		return environment.Receipt{}, err
	}
	version := nativeVersion(ctx, exe)
	listPlan := base.ProbePlan{
		Executable: exe,
		Argv:       []string{"--bare", "--plugin-dir", bundleDir, "plugin", "list", "--json"},
		Cwd:        workspace,
		Timeout:    45 * time.Second,
		Categories: []string{"plugin"},
		Network:    "none_required",
		WriteScope: "managed_diagnostic_storage",
		ModelCalls: false,
	}
	listResult, err := base.RunProbe(ctx, listPlan)
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
			ExecutableIdentity: exe,
			NativeVersion:      version,
			CapabilityRevision: capabilityRevision,
		},
		CreatedAt: time.Now().UTC(),
		Effects: environment.ProbeEffects{
			ModelCalls: false,
			Network:    "none_observed",
			WriteScope: "managed_diagnostic_storage",
		},
		Termination: environment.Termination{Status: "completed", NativeExitCode: listResult.ExitCode},
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
	if listResult.ExitCode != 0 {
		receipt.Findings = append(receipt.Findings, environment.Finding{
			Code: "PROBE_FAILED", Severity: environment.SeverityError, Target: "claude",
			Message:  strings.TrimSpace(listResult.Stderr + "\n" + listResult.Stdout),
			Evidence: environment.EvidenceUnknown,
			Remedy:   "ensure the staged bundle is a valid Claude plugin directory",
		})
		receipt.OverallStatus = "error"
		return receipt, nil
	}

	type pluginEntry struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	var plugins []pluginEntry
	if err := json.Unmarshal([]byte(listResult.Stdout), &plugins); err != nil {
		receipt.Coverage = []environment.CoverageNote{{
			Category: "plugin", Scope: "native_catalog", Completeness: "partial",
			Reason: "plugin list JSON could not be parsed",
		}}
		receipt.Findings = append(receipt.Findings, environment.Finding{
			Code: "OBSERVATION_PARTIAL", Severity: environment.SeverityError, Target: "claude",
			Message: err.Error(), Evidence: environment.EvidenceUnknown,
		})
		receipt.OverallStatus = "unknown"
		return receipt, nil
	}

	pluginPresent := false
	pluginID := ""
	for _, plugin := range plugins {
		if strings.Contains(plugin.ID, "agentpack-bundle") {
			pluginPresent = true
			pluginID = plugin.ID
			break
		}
	}
	receipt.Properties = append(receipt.Properties, environment.ObservedProperty{
		ArtifactID: "agentpack-bundle", Property: "presence", Value: pluginPresent,
		Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: "plugin-list-json",
	})
	if !pluginPresent {
		receipt.Coverage = []environment.CoverageNote{{
			Category: "plugin", Scope: "native_catalog", Completeness: "complete",
			Reason: "plugin list returned successfully; agentpack-bundle not present",
		}, {
			Category: "skill", Scope: "native_catalog", Completeness: "none",
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
		Timeout:    45 * time.Second,
		Categories: []string{"skill"},
		Network:    "none_required",
		WriteScope: "managed_diagnostic_storage",
	}
	details, err := base.RunProbe(ctx, detailsPlan)
	if err != nil || details.ExitCode != 0 {
		receipt.Coverage = append(receipt.Coverage, environment.CoverageNote{
			Category: "skill", Scope: "native_catalog", Completeness: "partial",
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
	skills := parseSkillInventory(details.Stdout)
	receipt.Coverage = []environment.CoverageNote{{
		Category: "plugin", Scope: "native_catalog", Completeness: "complete",
	}, {
		Category: "skill", Scope: "native_catalog", Completeness: "partial",
		Reason: "names observed; source bytes/digests not attributed by Claude",
	}}
	for _, skill := range skills {
		receipt.Properties = append(receipt.Properties, environment.ObservedProperty{
			ArtifactID: filepath.Join("agentpack-bundle", "skills", skill),
			Property:   "presence", Value: true,
			Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: "plugin-details",
		})
		receipt.Properties = append(receipt.Properties, environment.ObservedProperty{
			ArtifactID: filepath.Join("agentpack-bundle", "skills", skill),
			Property:   "source_digest", Value: nil,
			Evidence: environment.EvidenceUnknown, Reason: "Native interface does not attribute source bytes.",
		})
	}
	receipt.OverallStatus = "ready_with_warnings"
	return receipt, nil
}

func nativeVersion(ctx context.Context, exe string) string {
	result, err := base.RunProbe(ctx, base.ProbePlan{
		Executable: exe, Argv: []string{"--version"}, Timeout: 10 * time.Second,
	})
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

var skillLine = regexp.MustCompile(`(?m)^\s*Skills\s*\((\d+)\)\s*(.*)$`)

func parseSkillInventory(text string) []string {
	match := skillLine.FindStringSubmatch(text)
	if match == nil {
		return nil
	}
	rest := strings.TrimSpace(match[2])
	if rest == "" || match[1] == "0" {
		return nil
	}
	var skills []string
	for _, part := range strings.Fields(rest) {
		part = strings.Trim(part, ",")
		if part != "" {
			skills = append(skills, part)
		}
	}
	return skills
}
