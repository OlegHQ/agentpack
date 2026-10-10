package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OlegHQ/agentpack/internal/environment"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
)

func TestExternalCommandsPreserveSelectorAndMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	definition, workspace := t.TempDir(), t.TempDir()
	if err := manifest.WriteStub(definition, "external", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(definition, "external", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	runner := NewRunner()
	runner.Stdout, runner.Stderr = &out, &errs
	invoke := func(args ...string) (int, error) {
		out.Reset()
		errs.Reset()
		return runner.Run(context.Background(), append([]string{"--definition-root", definition, "--workspace", workspace}, args...))
	}
	if code, err := invoke("mode", "create", "review"); code != 0 || err != nil {
		t.Fatalf("mode: %d %v", code, err)
	}
	if code, err := invoke("--mode", "review", "preflight", "--json"); code != 0 || err != nil {
		t.Fatalf("preflight: %d %v", code, err)
	}
	var report environment.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Mode != "review" {
		t.Fatalf("mode=%s", report.Mode)
	}
	if code, err := invoke("--mode", "missing", "preflight", "--json"); code == 0 || err == nil {
		t.Fatal("missing mode accepted")
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("failed preflight is not JSON: %v", err)
	}
	if report.OverallStatus != "error" || report.OK {
		t.Fatal("failed preflight reports success")
	}
	if code, err := invoke("--mode", "review", "env", "restore"); code != 0 || err != nil {
		t.Fatalf("restore: %d %v %s", code, err, errs.String())
	}
	bundle := filepath.Join(t.TempDir(), "external.tar.gz")
	if code, err := invoke("env", "export", bundle); code != 0 || err != nil {
		t.Fatalf("export: %d %v", code, err)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("checkout mutated: %v", entries)
	}
}

func TestStrictLaunchRefusesUnknownObservationWithoutRunningAgent(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Static binary-presence check only; the unknown contract must prevent execution.
	t.Setenv("CLAUDE_CODE_PATH", executable)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	definition, workspace := t.TempDir(), t.TempDir()
	if err := manifest.WriteStub(definition, "strict", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(definition, "strict", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	contract := `{"schema_version":1,"unknown_required":"fail","requirements":[{"id":"observe","target":"claude","selector":{"category":"skill","name":"ambient"},"predicate":"absent","minimum_evidence":"observed"}]}`
	if err := os.WriteFile(filepath.Join(definition, "contract.json"), []byte(contract), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	runner := NewRunner()
	runner.Stdout, runner.Stderr = &out, &errs
	code, err := runner.Run(context.Background(), []string{"--definition-root", definition, "--workspace", workspace, "--strict-external", "claude"})
	if code == 0 || err == nil {
		t.Fatalf("strict launch passed: %d %v", code, err)
	}
	if code != 3 && code != 4 {
		t.Fatalf("unexpected code %d: %v", code, err)
	}
}
