package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ProbePlan is a fixed, non-shell native diagnostic invocation.
type ProbePlan struct {
	Executable string
	Argv       []string
	Cwd        string
	Env        []string // full env if non-nil; otherwise process env
	Timeout    time.Duration
	Categories []string
	Network    string
	WriteScope string
	ModelCalls bool
}

// ProbeResult is bounded process output.
type ProbeResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
	Duration time.Duration
}

// RunProbe executes a plan with cancellation and stdout/stderr caps.
func RunProbe(ctx context.Context, plan ProbePlan) (ProbeResult, error) {
	if plan.Executable == "" {
		return ProbeResult{}, fmt.Errorf("probe executable is required")
	}
	timeout := plan.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, plan.Executable, plan.Argv...)
	cmd.Dir = plan.Cwd
	if plan.Env != nil {
		cmd.Env = plan.Env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	result := ProbeResult{
		Stdout:   truncate(stdout.String(), 256<<10),
		Stderr:   truncate(stderr.String(), 64<<10),
		Duration: time.Since(start),
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		result.ExitCode = -1
		return result, fmt.Errorf("probe timed out after %s", timeout)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}
	result.ExitCode = 0
	return result, nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n…truncated…"
}

// LookPathEnv returns an executable path from env override or PATH.
func LookPathEnv(envName, binary string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return value, nil
	}
	return exec.LookPath(binary)
}
