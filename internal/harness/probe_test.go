package harness

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProbeHelper(t *testing.T) {
	if os.Getenv("AGENTPACK_PROBE_TEST_HELPER") != "1" {
		return
	}
	switch os.Getenv("AGENTPACK_PROBE_TEST_CASE") {
	case "exit":
		fmt.Print("misleading successful payload")
		os.Exit(7)
	case "flood":
		fmt.Print(strings.Repeat("x", 300<<10))
		os.Exit(0)
	case "wait":
		time.Sleep(time.Minute)
		os.Exit(0)
	default:
		fmt.Print("fixture version")
		os.Exit(0)
	}
}

func helperPlan(testCase string) ProbePlan {
	return ProbePlan{Executable: os.Args[0], Argv: []string{"-test.run=^TestProbeHelper$"}, Env: append(os.Environ(), "AGENTPACK_PROBE_TEST_HELPER=1", "AGENTPACK_PROBE_TEST_CASE="+testCase), Timeout: 5 * time.Second}
}

func TestProbeFailureAndBoundedOutput(t *testing.T) {
	result, err := RunProbe(context.Background(), helperPlan("exit"))
	if err == nil || result.ExitCode != 7 || !strings.Contains(result.Stdout, "misleading") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	result, err = RunProbe(context.Background(), helperPlan("flood"))
	if err == nil || !result.Truncated || len(result.Stdout) != 256<<10 {
		t.Fatalf("length=%d truncated=%t err=%v", len(result.Stdout), result.Truncated, err)
	}
}

func TestProbeCancellation(t *testing.T) {
	plan := helperPlan("wait")
	plan.Timeout = 100 * time.Millisecond
	result, err := RunProbe(context.Background(), plan)
	if err == nil || !result.TimedOut || result.Duration > 3*time.Second {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = RunProbe(ctx, helperPlan("wait"))
	if err == nil || !result.Canceled {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestProbeEvidenceIsPrivateAndRejectsTraversal(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	workspace := t.TempDir()
	path, err := SaveProbeEvidence(workspace, "receipt-1", "version", ProbeResult{Stdout: "private fixture value", ExitCode: 7})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
		t.Fatalf("private evidence permissions: %v %v", info, err)
	}
	if _, err = SaveProbeEvidence(workspace, "../escape", "version", ProbeResult{}); err == nil {
		t.Fatal("evidence traversal accepted")
	}
	if _, err = SaveProbeEvidence(workspace, "receipt-1", "version", ProbeResult{}); err == nil {
		t.Fatal("existing evidence overwritten")
	}
}
