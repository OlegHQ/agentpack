package main

import (
	"context"
	"os"

	"github.com/OlegHQ/agentpack/internal/cli"
)

func main() {
	code, _ := cli.NewRunner().Execute(context.Background(), os.Args[1:])
	os.Exit(code)
}
