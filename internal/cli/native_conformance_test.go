package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/cache"
	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
	packSync "github.com/OlegHQ/agentpack/internal/sync"
)

// Independent sentinel expectation exercises real frozen restoration and native observation.
func TestNativeRestoredPackConformance(t *testing.T) {
	if os.Getenv("AGENTPACK_NATIVE_PROBES") != "1" {
		t.Skip("explicit opt-in controlled native metadata test")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	definition, workspace, source := t.TempDir(), t.TempDir(), t.TempDir()
	if err := manifest.WriteStub(definition, "native-restored-pack", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := manifest.ReplaceModes(definition, map[string]mode.Definition{"held-out": {Base: mode.BaseNone}}); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: restored-sentinel\ndescription: Independently authored frozen-pack sentinel.\n---\nDo nothing.\n"
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(skill), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := lockfile.Package{Module: "local/restored-sentinel", Direct: true, Kind: lockfile.PackageSkill, Owner: "path", Repo: "restored-sentinel", URL: cache.FileURL(source), CacheKey: cache.ComputeKey("native-restored-fixture"), Commit: strings.Repeat("1", 40)}
	if ready, err := cache.EnsureLockCached(pkg, nil); err != nil || !ready {
		t.Fatalf("prepare local fixture: %t %v", ready, err)
	}
	entry, err := cache.EntryDir(pkg.CacheKey)
	if err != nil {
		t.Fatal(err)
	}
	pkg.ContentHash, err = cache.TreeDigest(entry)
	if err != nil {
		t.Fatal(err)
	}
	lock := lockfile.EmptyForProject(definition)
	lock.Packages = []lockfile.Package{pkg}
	if err = lock.Save(definition); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.LockPath(definition))
	if err != nil {
		t.Fatal(err)
	}
	service := packSync.NewService()
	for _, target := range []base.Target{base.Claude, base.Codex} {
		if err = service.RestoreFrozenOptions(context.Background(), definition, packSync.SyncOptions{WorkspaceRoot: workspace, Target: &target, StrictExternal: true}); err != nil {
			t.Fatal(err)
		}
		report, err := service.Preflight(definition, packSync.PreflightOptions{WorkspaceRoot: workspace, Target: &target, StrictExternal: true, Policy: environment.PolicyLocal})
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		runner := NewRunner()
		runner.Stdout = &output
		code, probeErr := runner.runProbe(context.Background(), definition, workspace, "default", []string{"--agent", string(target), "--strict-external", "--json"}, false)
		if code != 0 || probeErr != nil {
			t.Fatalf("native CLI probe %s: code=%d err=%v output=%s", target, code, probeErr, output.String())
		}
		var receipt environment.Receipt
		if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
			t.Fatal(err)
		}
		verified, err := service.Preflight(definition, packSync.PreflightOptions{WorkspaceRoot: workspace, Target: &target, StrictExternal: true, Policy: environment.PolicyLocal, ReceiptID: "latest"})
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range verified.Findings {
			if finding.Code == "OBSERVATION_UNAVAILABLE" || finding.Code == "RECEIPT_STALE" || finding.Code == "PROBE_FAILED" {
				t.Fatalf("CLI receipt rejected: %+v", finding)
			}
		}

		found := false
		for _, property := range receipt.Properties {
			if strings.HasSuffix(filepath.ToSlash(property.ArtifactID), "skills/restored-sentinel") && property.Property == "presence" && property.Value == true && property.Scope == "controlled_projection" {
				found = true
			}
		}
		if !found {
			t.Fatalf("restored pack sentinel not observed by %s: %+v", target, receipt)
		}
		t.Logf("restored target=%s native=%s controlled sentinel observed; source=%s", target, receipt.Target.NativeVersion, report.SourceIdentity)
		// Held-out mode omission is independently specified rather than inferred
		// from the native output. Partial catalogs must not manufacture absence.
		if err := service.RestoreFrozenOptions(context.Background(), definition, packSync.SyncOptions{Mode: "held-out", WorkspaceRoot: workspace, Target: &target, StrictExternal: true}); err != nil {
			t.Fatal(err)
		}
		output.Reset()
		code, probeErr = runner.runProbe(context.Background(), definition, workspace, "held-out", []string{"--agent", string(target), "--strict-external", "--json"}, false)
		if code != 0 || probeErr != nil {
			t.Fatalf("held-out native probe %s: code=%d err=%v output=%s", target, code, probeErr, output.String())
		}
		var omitted environment.Receipt
		if err := json.Unmarshal(output.Bytes(), &omitted); err != nil {
			t.Fatal(err)
		}
		for _, property := range omitted.Properties {
			if strings.HasSuffix(filepath.ToSlash(property.ArtifactID), "skills/restored-sentinel") && property.Property == "presence" && property.Value == true {
				t.Fatalf("mode-filtered sentinel falsely observed: %+v", property)
			}
		}
		checked, err := service.Preflight(definition, packSync.PreflightOptions{Mode: "held-out", WorkspaceRoot: workspace, Target: &target, StrictExternal: true, Policy: environment.PolicyLocal, ReceiptID: "latest"})
		if err != nil {
			t.Fatal(err)
		}
		required := environment.Contract{SchemaVersion: 1, UnknownRequired: "fail", Requirements: []environment.Requirement{{ID: "held-out-sentinel", Selector: map[string]any{"category": "skill", "name": "restored-sentinel", "scope": "controlled_projection"}, Predicate: "present", MinimumEvidence: "observed", Severity: environment.SeverityError}}}
		outcomes := environment.EvaluateContract(required, checked)
		if len(outcomes) != 1 || outcomes[0].Result != "unknown" {
			t.Fatalf("omitted sentinel should remain unknown under partial native coverage: %+v", outcomes)
		}
		t.Logf("held-out target=%s mode-filtered sentinel has no positive native observation; required observed contract unknown", target)

	}
	after, err := os.ReadFile(paths.LockPath(definition))
	if err != nil || string(before) != string(after) {
		t.Fatal("frozen restore changed lock")
	}
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 0 {
		t.Fatalf("restoration/probes wrote checkout: %v %v", entries, err)
	}
}
