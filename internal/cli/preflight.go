package cli

import (
	"fmt"

	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
	packSync "github.com/OlegHQ/agentpack/internal/sync"
)

func (runner Runner) runPreflight(definitionRoot, workspaceRoot, mode string, arguments []string, quiet bool) (code int, resultErr error) {
	args, jsonOut := takeBool(arguments, "--json")
	written := false
	defer func() {
		if jsonOut && !written && resultErr != nil {
			report := environment.Report{SchemaVersion: 1, Phase: "preflight", DefinitionRoot: definitionRoot, WorkspaceRoot: workspaceRoot, Mode: mode, OverallStatus: "error", Findings: []environment.Finding{{Code: "PREFLIGHT_ERROR", Severity: environment.SeverityError, Message: resultErr.Error(), Evidence: environment.EvidenceUnknown}}}
			if err := writeJSON(runner.Stdout, report); err != nil {
				resultErr = fmt.Errorf("%w; write JSON: %v", resultErr, err)
			}
		}
	}()

	args, strict := takeBool(args, "--strict-external")
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
	policyName, args, _, err := takeFlag(args, "--policy")
	if err != nil {
		return 2, err
	}
	contractPath, args, _, err := takeFlag(args, "--contract")
	if err != nil {
		return 2, err
	}
	receiptID, args, _, err := takeFlag(args, "--receipt")
	if err != nil {
		return 2, err
	}
	if err := noArgs(args); err != nil {
		return 2, err
	}
	policy, err := environment.ParsePolicy(policyName)
	if err != nil {
		return 2, err
	}
	var target *base.Target
	if agent != "" {
		parsed, err := base.ParseTarget(agent)
		if err != nil {
			return 2, err
		}
		if agent == "agent" {
			parsed = base.Cursor
		}
		target = &parsed
	}
	report, err := runner.Service.Preflight(definitionRoot, packSync.PreflightOptions{
		Mode:           mode,
		Target:         target,
		Policy:         policy,
		StrictExternal: strict,
		WorkspaceRoot:  workspaceRoot,
		ContractPath:   contractPath,
		ReceiptID:      receiptID,
	})
	if err != nil {
		return 5, err
	}
	if jsonOut {
		written = true
		if err := writeJSON(runner.Stdout, report); err != nil {
			return 1, err
		}
	} else if !quiet {
		printPreflight(runner, report)
	}
	switch report.OverallStatus {
	case "ready", "ready_with_warnings":
		return 0, nil
	case "violated":
		return 3, fmt.Errorf("preflight violated policy")
	case "unknown":
		return 4, fmt.Errorf("preflight required observation unknown")
	case "error":
		return 5, fmt.Errorf("preflight operational failure")
	default:
		return 1, fmt.Errorf("preflight failed")
	}
}

func printPreflight(runner Runner, report environment.Report) {
	fmt.Fprintf(runner.Stdout, "preflight status=%s mode=%s policy=%s\n", report.OverallStatus, report.Mode, report.Policy)
	fmt.Fprintf(runner.Stdout, "definition\t%s\n", report.DefinitionRoot)
	fmt.Fprintf(runner.Stdout, "workspace\t%s\n", report.WorkspaceRoot)
	if report.Environment != "" {
		fmt.Fprintf(runner.Stdout, "environment\t%s\n", report.Environment)
	}
	if report.SourceIdentity != "" {
		fmt.Fprintf(runner.Stdout, "source_identity\t%s\n", report.SourceIdentity)
	}
	if report.LockDigest != "" {
		fmt.Fprintf(runner.Stdout, "lock_digest\t%s\n", report.LockDigest)
	}
	if report.Target != "" {
		fmt.Fprintf(runner.Stdout, "target\t%s\n", report.Target)
	}
	fmt.Fprintf(runner.Stdout, "packages\t%d skill(s), %d plugin(s), %d shadowed\n", report.Skills, report.Plugins, report.Shadowed)
	if report.StrictExternal {
		fmt.Fprintln(runner.Stdout, "strict_external\ttrue")
	}
	for _, write := range report.PlannedWrites {
		fmt.Fprintf(runner.Stdout, "planned_write\t%s\n", write)
	}
	for _, finding := range report.Findings {
		line := fmt.Sprintf("%s\t%s\t%s", finding.Severity, finding.Code, finding.Message)
		if finding.Source != "" {
			line += "\tsource=" + finding.Source
		}
		if finding.Target != "" {
			line += "\ttarget=" + finding.Target
		}
		if finding.Evidence != "" {
			line += "\tevidence=" + string(finding.Evidence)
		}
		if finding.Remedy != "" {
			line += "\tremedy=" + finding.Remedy
		}
		fmt.Fprintln(runner.Stdout, line)
	}
}
