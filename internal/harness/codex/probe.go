package codex

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
)

const (
	capabilityRevision = "codex-probe-v2"
	adapterRevision    = "codex-observe-2026-10"
)

// Capability describes validated Codex observation support for this revision.
func Capability() map[string]string {
	return map[string]string{
		"adapter_revision":    adapterRevision,
		"capability_revision": capabilityRevision,
		"validated_versions":  "0.159.2, 0.159.3 (version only)",
		"native_version":      "observed via `codex --version`",
		"skill_presence":      "positive names via controlled debug prompt-input in 0.159.2; partial, no absence or model use proof",
		"skill_source_digest": "unknown",
		"effects":             "0.159.2 controlled static config clone; no auth/history/hooks copied, no model inference requested; native network/global effects unmeasured",
	}
}

// ProbeHome records Codex version observations. Skill catalogs stay unknown.
func ProbeHome(ctx context.Context, codexHome, workspace, sourceID, lockDigest, mode string) (environment.Receipt, error) {
	exe, err := base.LookPathEnv("CODEX_PATH", "codex")
	if err != nil {
		return environment.Receipt{}, err
	}
	identity, identityErr := environment.ExecutableIdentity(exe)
	if identityErr != nil {
		return environment.Receipt{}, identityErr
	}
	env := withCodexHome(os.Environ(), codexHome)
	versionResult, err := base.RunProbe(ctx, base.ProbePlan{
		Executable: exe,
		Argv:       []string{"--version"},
		Cwd:        workspace,
		Env:        env,
		Timeout:    15 * time.Second,
		Categories: []string{"runtime"},
		Network:    "none_required",
		WriteScope: "managed_diagnostic_storage",
	})
	receipt := environment.Receipt{
		SchemaVersion:  1,
		Phase:          "probe",
		ReceiptID:      fmt.Sprintf("codex-%d", time.Now().UTC().UnixNano()),
		SourceIdentity: sourceID,
		LockDigest:     lockDigest,
		Mode:           mode,
		Target: environment.ReceiptTarget{
			Adapter:            "codex",
			AdapterRevision:    adapterRevision,
			ExecutableIdentity: identity,
			CapabilityRevision: capabilityRevision,
		},
		CreatedAt: time.Now().UTC(),
		Coverage: []environment.CoverageNote{{
			Category: "skill", Scope: "controlled_projection", Completeness: "none",
			Reason: "Codex 0.159.3 has no validated non-model skill catalog probe",
		}},
		Effects: environment.ProbeEffects{
			ModelCalls: false,
			Network:    "not_measured",
			WriteScope: "native_runtime_unmeasured; receipt_in_managed_storage",
		},
		Termination: environment.Termination{Status: "completed"},
	}
	versionEvidence, saveErr := base.SaveProbeEvidence(workspace, receipt.ReceiptID, "version", versionResult)
	if saveErr != nil {
		return receipt, saveErr
	}
	receipt.DiagnosticRefs = append(receipt.DiagnosticRefs, versionEvidence)

	receipt.Termination.NativeExitCode = versionResult.ExitCode
	if err == nil && !regexp.MustCompile(`^(?:codex-cli )?[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(strings.TrimSpace(versionResult.Stdout)) {
		err = fmt.Errorf("Codex version probe returned an unexpected version format")
	}
	if err != nil {
		receipt.Termination.Status = "failed"
		receipt.Findings = append(receipt.Findings, environment.Finding{
			Code: "PROBE_FAILED", Severity: environment.SeverityError, Target: "codex",
			Message: err.Error(), Evidence: environment.EvidenceUnknown,
			Remedy: "install codex or set CODEX_PATH",
		})
		receipt.OverallStatus = "error"
		return receipt, nil
	}
	receipt.Termination.NativeExitCode = versionResult.ExitCode
	receipt.Target.NativeVersion = strings.TrimSpace(versionResult.Stdout)
	receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "controlled_projection",
		Category: "runtime", Property: "native_version", Value: receipt.Target.NativeVersion,
		Evidence: environment.EvidenceObserved, Method: "codex --version", EvidenceRef: versionEvidence,
	})
	if receipt.Target.NativeVersion == "codex-cli 0.159.2" || receipt.Target.NativeVersion == "0.159.2" {
		observePromptSkills(ctx, exe, codexHome, workspace, &receipt)
		return receipt, nil
	}
	receipt.Findings = append(receipt.Findings, environment.Finding{
		Code: "OBSERVATION_UNAVAILABLE", Severity: environment.SeverityWarning, Target: "codex",
		Message:  "this native version has no validated configuration discovery parser",
		Evidence: environment.EvidenceUnknown,
		Remedy:   "use generated-configuration contracts, or narrow required evidence below observed",
	})
	if receipt.Target.NativeVersion != "codex-cli 0.159.3" && receipt.Target.NativeVersion != "0.159.3" {
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: "OBSERVATION_UNAVAILABLE", Severity: environment.SeverityWarning, Target: "codex", Message: "native version is outside validated capability coverage", Evidence: environment.EvidenceUnknown})
		receipt.OverallStatus = "unknown"
	} else {
		receipt.OverallStatus = "ready_with_warnings"
	}
	return receipt, nil
}

func withCodexHome(env []string, codexHome string) []string {
	if codexHome == "" {
		return env
	}
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, "CODEX_HOME=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, "CODEX_HOME="+codexHome)
}
