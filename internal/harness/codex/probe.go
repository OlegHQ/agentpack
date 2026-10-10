package codex

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
)

const (
	capabilityRevision = "codex-probe-v1"
	adapterRevision    = "codex-observe-2026-10"
)

// Capability describes validated Codex observation support for this revision.
func Capability() map[string]string {
	return map[string]string{
		"adapter_revision":    adapterRevision,
		"capability_revision": capabilityRevision,
		"validated_versions":  "0.159.3",
		"native_version":      "observed via `codex --version`",
		"skill_presence":      "unknown — no stable non-model skill catalog API in 0.159.3",
		"skill_source_digest": "unknown",
		"effects":             "version probe only; no model inference",
	}
}

// ProbeHome records Codex version observations. Skill catalogs stay unknown.
func ProbeHome(ctx context.Context, codexHome, workspace, sourceID, lockDigest, mode string) (environment.Receipt, error) {
	exe, err := base.LookPathEnv("CODEX_PATH", "codex")
	if err != nil {
		return environment.Receipt{}, err
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
			ExecutableIdentity: exe,
			CapabilityRevision: capabilityRevision,
		},
		CreatedAt: time.Now().UTC(),
		Coverage: []environment.CoverageNote{{
			Category: "skill", Scope: "native_catalog", Completeness: "none",
			Reason: "Codex 0.159.3 has no validated non-model skill catalog probe",
		}},
		Effects: environment.ProbeEffects{
			ModelCalls: false,
			Network:    "none_required",
			WriteScope: "managed_diagnostic_storage",
		},
		Termination: environment.Termination{Status: "completed"},
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
	receipt.Properties = append(receipt.Properties, environment.ObservedProperty{
		Property: "native_version", Value: receipt.Target.NativeVersion,
		Evidence: environment.EvidenceObserved, Method: "codex --version",
	})
	receipt.Findings = append(receipt.Findings, environment.Finding{
		Code: "OBSERVATION_UNAVAILABLE", Severity: environment.SeverityWarning, Target: "codex",
		Message:  "skill discovery cannot be confirmed without a native catalog API",
		Evidence: environment.EvidenceUnknown,
		Remedy:   "use generated-configuration contracts, or narrow required evidence below observed",
	})
	receipt.OverallStatus = "ready_with_warnings"
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
