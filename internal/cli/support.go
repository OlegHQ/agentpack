package cli

import (
	"fmt"

	"github.com/OlegHQ/agentpack/internal/environment"
)

func (runner Runner) runSupport(workspace string, arguments []string, quiet bool) (int, error) {
	if len(arguments) == 0 {
		return 2, fmt.Errorf("support requires an action (export)")
	}
	switch arguments[0] {
	case "export":
		return runner.supportExport(workspace, arguments[1:], quiet)
	default:
		return 2, fmt.Errorf("unknown support action %q", arguments[0])
	}
}

func (runner Runner) supportExport(workspace string, arguments []string, quiet bool) (int, error) {
	output, args, _, err := takeFlag(arguments, "--output")
	if err != nil {
		return 2, err
	}
	args, jsonOut := takeBool(args, "--json")
	if len(args) != 1 {
		return 2, fmt.Errorf("support export requires RECEIPT_ID")
	}
	if output == "" {
		return 2, fmt.Errorf("support export requires --output PATH")
	}
	receipt, err := environment.LoadReceipt(workspace, args[0])
	if err != nil {
		return 5, err
	}
	capsule := environment.ExportSupportCapsule(receipt)
	if err := environment.WriteSupportCapsule(output, capsule); err != nil {
		return 1, err
	}
	if jsonOut {
		if err := writeJSON(runner.Stdout, capsule); err != nil {
			return 1, err
		}
	} else if !quiet {
		fmt.Fprintf(runner.Stdout, "Wrote redacted support capsule to %s\n", output)
		fmt.Fprintln(runner.Stdout, "Preview scope:", capsule.Scope)
		for _, note := range capsule.Notes {
			fmt.Fprintf(runner.Stdout, "note\t%s\n", note)
		}
	}
	return 0, nil
}
