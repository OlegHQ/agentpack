package main

import (
	"context"
	"fmt"
	"os"

	"github.com/OlegHQ/agentpack/internal/cli"
)

func main() {
	code, err := cli.NewRunner().Run(context.Background(), os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentpack:", err)
	}
	os.Exit(code)
}
