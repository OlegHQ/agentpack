package cli

import (
	"fmt"

	"github.com/OlegHQ/agentpack/internal/environment"
)

func (runner Runner) runConfigCompare(workspace string, arguments []string, quiet bool) (int, error) {
	args, jsonOut := takeBool(arguments, "--json")
	if len(args) != 2 {
		return 2, fmt.Errorf("config compare requires RECEIPT_A RECEIPT_B")
	}
	left, err := environment.LoadReceipt(workspace, args[0])
	if err != nil {
		return 5, err
	}
	right, err := environment.LoadReceipt(workspace, args[1])
	if err != nil {
		return 5, err
	}
	comparison := environment.CompareReceipts(left, right)
	if jsonOut {
		if err := writeJSON(runner.Stdout, comparison); err != nil {
			return 1, err
		}
	} else if !quiet {
		fmt.Fprintf(runner.Stdout, "compare compatible=%t left=%s right=%s\n", comparison.Compatible, comparison.LeftID, comparison.RightID)
		for _, delta := range comparison.Deltas {
			fmt.Fprintf(runner.Stdout, "delta\t%s\n", delta)
		}
		for _, unknown := range comparison.Unknowns {
			fmt.Fprintf(runner.Stdout, "unknown\t%s\n", unknown)
		}
	}
	if !comparison.Compatible {
		return 3, fmt.Errorf("receipts are not comparable")
	}
	return 0, nil
}
