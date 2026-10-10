package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/OlegHQ/agentpack/internal/paths"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	Stdout    string
	Stderr    string
	ExitCode  int
	TimedOut  bool
	Canceled  bool
	Truncated bool
	Duration  time.Duration
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
	configureProbeProcess(cmd)
	cmd.WaitDelay = time.Second
	var stdout, stderr = cappedProbeBuffer{limit: 256 << 10}, cappedProbeBuffer{limit: 64 << 10}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	result := ProbeResult{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Truncated: stdout.truncated || stderr.truncated,
		ExitCode:  -1,
		Duration:  time.Since(start),
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		result.ExitCode = -1
		return result, fmt.Errorf("probe timed out after %s", timeout)
	}
	if ctx.Err() != nil {
		result.Canceled = true
		return result, fmt.Errorf("probe canceled: %w", ctx.Err())
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, fmt.Errorf("native probe exited with status %d", result.ExitCode)
		}
		return result, err
	}
	result.ExitCode = 0
	if result.Truncated {
		return result, fmt.Errorf("native probe output exceeded capture limit")
	}
	return result, nil
}

// cappedProbeBuffer drains all output while bounding retained bytes during execution.
type cappedProbeBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedProbeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := b.limit - b.buffer.Len()
	if n > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (b *cappedProbeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// LookPathEnv returns an executable path from env override or PATH.
func LookPathEnv(envName, binary string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {

		return exec.LookPath(value)
	}
	return exec.LookPath(binary)
}

// SaveProbeEvidence retains bounded raw diagnostics privately; support exports must omit these references.
func SaveProbeEvidence(workspace, receiptID, phase string, result ProbeResult) (string, error) {
	if len(result.Stdout) > 256<<10 || len(result.Stderr) > 64<<10 {
		return "", fmt.Errorf("probe evidence exceeds capture limit")
	}
	for _, component := range []string{receiptID, phase} {
		if component == "" || component == "." || component == ".." || filepath.Base(component) != component || strings.ContainsAny(component, "/\\") {
			return "", fmt.Errorf("invalid probe evidence component")
		}
	}
	root, err := paths.ProjectStateFile(workspace, "probe-diagnostics")
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	if info, statErr := os.Lstat(root); statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("probe diagnostic root must be an owned directory")
	}
	directory := filepath.Join(root, receiptID)
	if err = os.Mkdir(directory, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	if info, statErr := os.Lstat(directory); statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("probe evidence directory is invalid")
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	path := filepath.Join(directory, phase+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(append(data, '\n'))
	return path, errors.Join(writeErr, file.Close())
}
