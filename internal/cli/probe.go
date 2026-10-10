package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/harness/claude"
	"github.com/OlegHQ/agentpack/internal/harness/codex"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/OlegHQ/agentpack/internal/staging"
	packSync "github.com/OlegHQ/agentpack/internal/sync"
)

func (runner Runner) runProbe(ctx context.Context, definitionRoot, workspaceRoot, mode string, arguments []string, quiet bool) (int, error) {
	args, jsonOut := takeBool(arguments, "--json")
	args, strict := takeBool(args, "--strict-external")
	policyName, args, _, err := takeFlag(args, "--policy")
	if err != nil {
		return 2, err
	}
	policy, err := environment.ParsePolicy(policyName)
	if err != nil {
		return 2, err
	}
	agent, args, _, err := takeFlag(args, "--agent")
	if err != nil {
		return 2, err
	}
	if agent == "" {
		agent, args, _, err = takeFlag(args, "--target")
		if err != nil {
			return 2, err
		}
	}
	if agent == "" {
		return 2, fmt.Errorf("probe requires --agent claude|codex")
	}
	requestedMode, args, _, err := takeFlag(args, "--mode")
	if err != nil {
		return 2, err
	}
	if requestedMode != "" {
		mode = requestedMode
	}
	if err := noArgs(args); err != nil {
		return 2, err
	}
	target, err := base.ParseTarget(agent)
	if err != nil {
		return 2, err
	}
	if agent == "agent" {
		target = base.Cursor
	}
	switch target {
	case base.Claude, base.Codex:
	default:
		return 2, fmt.Errorf("probe currently supports claude and codex only (got %s)", target)
	}

	report, err := runner.Service.Preflight(definitionRoot, packSync.PreflightOptions{
		Mode: mode, Target: &target, Policy: policy, WorkspaceRoot: workspaceRoot, StrictExternal: strict,
	})
	if err != nil {
		return 5, err
	}
	if mode == "" {
		mode = report.Mode
	}

	lease, err := staging.AcquireLaunchShared(definitionRoot, mode)
	if err != nil {
		return 5, err
	}
	defer lease.Unlock()
	report, err = runner.Service.Preflight(definitionRoot, packSync.PreflightOptions{Mode: mode, Target: &target, Policy: policy, WorkspaceRoot: workspaceRoot, StrictExternal: strict})
	if err != nil {
		return 5, err
	}
	for _, finding := range report.Findings {
		if finding.Severity == environment.SeverityError || (finding.Severity == environment.SeverityViolation && finding.Code != "CONTRACT_MISMATCH" && finding.Code != "OBSERVATION_UNAVAILABLE") || finding.Code == "STAGING_NOT_RECORDED" || finding.Code == "STAGING_INPUTS_UNKNOWN" {
			if jsonOut {
				if err := writeJSON(runner.Stdout, report); err != nil {
					return 5, err
				}
			}
			return 5, fmt.Errorf("probe cannot observe current staging: %s; restore/sync and resolve preflight findings first", finding.Code)
		}
	}
	var receipt environment.Receipt
	switch target {
	case base.Claude:
		plugins, err := paths.StagingPluginsDirForMode(definitionRoot, mode)
		if err != nil {
			return 5, err
		}
		bundle := filepath.Join(plugins, paths.StagedAgentpackBundleName)
		if _, err := os.Stat(filepath.Join(bundle, ".claude-plugin", "plugin.json")); err != nil {
			return 5, fmt.Errorf("staged Claude bundle missing; run agentpack sync first: %w", err)
		}
		receipt, err = claude.ProbeBundle(ctx, bundle, workspaceRoot, report.SourceIdentity, report.LockDigest, mode)
		if err != nil {
			return 5, err
		}
	case base.Codex:
		codexHome, homeErr := codex.CurrentHome(definitionRoot, mode)
		if homeErr != nil {
			return 5, homeErr
		}
		receipt, err = codex.ProbeHome(ctx, codexHome, workspaceRoot, report.SourceIdentity, report.LockDigest, mode)
		if err != nil {
			return 5, err
		}
	}

	if receipt.Coverage == nil {
		receipt.Coverage = []environment.CoverageNote{}
	}
	if receipt.Properties == nil {
		receipt.Properties = []environment.ObservedProperty{}
	}
	if receipt.Findings == nil {
		receipt.Findings = []environment.Finding{}
	}
	for i := range receipt.Properties {
		property := &receipt.Properties[i]
		if property.Evidence == environment.EvidenceObserved {
			property.EvidenceDigest, err = environment.ExecutableIdentity(property.EvidenceRef)
			if err != nil {
				return 5, fmt.Errorf("retain native observation evidence: %w", err)
			}
		}
	}
	after, err := runner.Service.Preflight(definitionRoot, packSync.PreflightOptions{Mode: mode, Target: &target, Policy: policy, WorkspaceRoot: workspaceRoot, StrictExternal: strict})
	if err != nil {
		return 5, err
	}
	if after.SourceIdentity != report.SourceIdentity || after.LockDigest != report.LockDigest || after.WorkspaceDigest != report.WorkspaceDigest || after.GenerationID != report.GenerationID || after.PolicyDigest != report.PolicyDigest {
		receipt.OverallStatus = "error"
		receipt.Termination.Status = "failed"
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: "INPUTS_CHANGED", Severity: environment.SeverityError, Evidence: environment.EvidenceUnknown, Message: "environment inputs changed during native observation", Remedy: "restore and probe again"})
	}
	receipt.WorkspaceDigest = report.WorkspaceDigest
	receipt.GenerationID = report.GenerationID
	receipt.PolicyDigest = report.PolicyDigest
	path, err := environment.SaveReceipt(workspaceRoot, receipt)
	if err != nil {
		return 1, err
	}
	if jsonOut {
		if err := writeJSON(runner.Stdout, receipt); err != nil {
			return 1, err
		}
	} else if !quiet {
		fmt.Fprintf(runner.Stdout, "probe status=%s receipt=%s\n", receipt.OverallStatus, path)
		fmt.Fprintf(runner.Stdout, "target\t%s\tversion=%s\n", receipt.Target.Adapter, receipt.Target.NativeVersion)
		for _, note := range receipt.Coverage {
			fmt.Fprintf(runner.Stdout, "coverage\t%s\t%s\t%s\n", note.Category, note.Completeness, note.Reason)
		}
		for _, finding := range receipt.Findings {
			fmt.Fprintf(runner.Stdout, "%s\t%s\t%s\n", finding.Severity, finding.Code, finding.Message)
		}
	}
	switch receipt.OverallStatus {
	case "ready", "ready_with_warnings":
		return 0, nil
	case "violated":
		return 3, fmt.Errorf("probe policy violated")
	case "unknown":
		return 4, fmt.Errorf("probe observation unknown")
	case "error":
		return 5, fmt.Errorf("probe operational failure")
	default:
		return 1, fmt.Errorf("probe failed")
	}
}
