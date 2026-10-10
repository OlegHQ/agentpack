package sync

import (
	"context"
	"github.com/OlegHQ/agentpack/internal/mode"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func TestPreflightDoesNotMutateLockOrCheckout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	definition := t.TempDir()
	workspace := t.TempDir()
	if err := manifest.WriteStub(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.ReadFile(paths.LockPath(definition))
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "untouched.txt")
	if err := os.WriteFile(marker, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	target := base.Claude
	report, err := NewService().Preflight(definition, PreflightOptions{
		Target:         &target,
		Policy:         environment.PolicyLocal,
		StrictExternal: true,
		WorkspaceRoot:  workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.DefinitionRoot == "" || report.WorkspaceRoot != workspace {
		t.Fatalf("report roots: %#v", report)
	}

	lockAfter, err := os.ReadFile(paths.LockPath(definition))
	if err != nil {
		t.Fatal(err)
	}
	if string(lockBefore) != string(lockAfter) {
		t.Fatal("preflight mutated pack.lock")
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "untouched.txt" {
		t.Fatalf("workspace changed: %#v", entries)
	}
}

func TestPreflightPlannedWritesMatchStrictSyncRefusal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	definition := t.TempDir()
	workspace := t.TempDir()
	if err := manifest.WriteStub(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	target := base.Cursor
	report, err := NewService().Preflight(definition, PreflightOptions{
		Target: &target, Policy: environment.PolicyLocal, StrictExternal: true, WorkspaceRoot: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.PlannedWrites) == 0 {
		t.Fatal("expected planned overlay write")
	}
	_, syncErr := NewService().Sync(context.Background(), definition, SyncOptions{
		Target: &target, WorkspaceRoot: workspace, StrictExternal: true,
	})
	if syncErr == nil {
		t.Fatal("strict sync should refuse cursor overlay")
	}
	for _, write := range report.PlannedWrites {
		if !strings.Contains(syncErr.Error(), "workspace overlay") && !strings.Contains(syncErr.Error(), "strict-external") {
			t.Fatalf("sync error %q should mention strict overlay refusal (planned %v)", syncErr, report.PlannedWrites)
		}
		_ = write
	}
}

func TestPreflightStrictExternalFlagsCursorOverlay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	definition := t.TempDir()
	if err := manifest.WriteStub(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	target := base.Cursor
	report, err := NewService().Preflight(definition, PreflightOptions{
		Target:         &target,
		Policy:         environment.PolicyLocal,
		StrictExternal: true,
		WorkspaceRoot:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatal("expected blocking finding for cursor under strict-external")
	}
	found := false
	for _, finding := range report.Findings {
		if finding.Code == "WORKSPACE_WRITE_REQUIRED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing WORKSPACE_WRITE_REQUIRED: %#v", report.Findings)
	}
}

func TestRestoreFrozenLeavesLockBytesUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	definition := t.TempDir()
	if err := manifest.WriteStub(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.LockPath(definition))
	if err != nil {
		t.Fatal(err)
	}
	if err := NewService().RestoreFrozen(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(paths.LockPath(definition))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("restore mutated pack.lock")
	}
}

func TestRestoreFrozenFailsMissingInputWithoutLockChange(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	definition := t.TempDir()
	if err := manifest.WriteStub(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	lock := lockfile.PackLock{LockfileVersion: 2, Packages: []lockfile.Package{{
		Module: "missing-local", Direct: true, Kind: lockfile.PackageSkill,
		Owner: "path", Repo: "missing", Path: "skill",
		Commit: "local", CacheKey: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		URL: "file:///no/such/locked/skill/source",
	}}}
	if err := lock.Save(definition); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.LockPath(definition))
	if err != nil {
		t.Fatal(err)
	}
	err = NewService().RestoreFrozen(context.Background(), definition)
	if err == nil {
		t.Fatal("expected missing-input failure")
	}
	after, err := os.ReadFile(paths.LockPath(definition))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed restore mutated pack.lock")
	}
}

func TestRelocatedDefinitionsShareSourceIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	first := t.TempDir()
	if err := manifest.WriteStub(first, "portable", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(first, "portable", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	ws1, ws2 := t.TempDir(), t.TempDir()
	report1, err := NewService().Preflight(first, PreflightOptions{WorkspaceRoot: ws1})
	if err != nil {
		t.Fatal(err)
	}
	second := t.TempDir()
	for _, name := range []string{paths.ManifestName, paths.LockfileName} {
		data, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(second, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report2, err := NewService().Preflight(second, PreflightOptions{WorkspaceRoot: ws2})
	if err != nil {
		t.Fatal(err)
	}
	if report1.SourceIdentity == "" || report1.SourceIdentity != report2.SourceIdentity {
		t.Fatalf("source identity not portable: %q vs %q", report1.SourceIdentity, report2.SourceIdentity)
	}
	if report1.LockDigest == "" || report1.LockDigest != report2.LockDigest {
		t.Fatalf("lock digest not portable: %q vs %q", report1.LockDigest, report2.LockDigest)
	}
	if err := environment.SaveBinding(environment.Binding{
		Environment: "portable", DefinitionRoot: second, WorkspaceRoot: ws2,
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(ws2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("relocated bind wrote checkout: %#v", entries)
	}
}

func TestPreflightFlagsAmbientUserSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	t.Setenv("HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	definition := t.TempDir()
	if err := manifest.WriteStub(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	cacheKey := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	lock := lockfile.PackLock{LockfileVersion: 2, Packages: []lockfile.Package{{
		Module: "github.com/example/skills/helper", Direct: true, Kind: lockfile.PackageSkill,
		Owner: "example", Repo: "skills", Path: "helper", Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CacheKey: cacheKey,
	}}}
	if err := lock.Save(definition); err != nil {
		t.Fatal(err)
	}
	cacheSkill := filepath.Join(home, "cache", cacheKey)
	if err := os.MkdirAll(cacheSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheSkill, "SKILL.md"), []byte("---\nname: helper\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(home, ".claude", "skills", "helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# helper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := NewService().Preflight(definition, PreflightOptions{
		Policy: environment.PolicyCI, StrictExternal: true, WorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range report.Findings {
		if finding.Code == "CONFIG_SHADOWED" && finding.Source == "helper" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing CONFIG_SHADOWED: %#v", report.Findings)
	}
	if report.OverallStatus != "violated" {
		t.Fatalf("status=%s findings=%#v", report.OverallStatus, report.Findings)
	}
}

func TestSourceIdentityStableAcrossRelocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	first := t.TempDir()
	if err := manifest.WriteStub(first, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(first, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	lock, err := lockfile.Load(first)
	if err != nil {
		t.Fatal(err)
	}
	id1, err := sourceIdentity(first, lock)
	if err != nil {
		t.Fatal(err)
	}
	second := t.TempDir()
	for _, name := range []string{paths.ManifestName, paths.LockfileName} {
		data, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(second, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id2, err := sourceIdentity(second, lock)
	if err != nil {
		t.Fatal(err)
	}
	if id1 == "" || id1 != id2 {
		t.Fatalf("identity not portable: %q vs %q", id1, id2)
	}
}

func TestPreflightFlagsProjectAmbientSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	t.Setenv("HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	cacheKey := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	definition := t.TempDir()
	if err := manifest.WriteStub(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	lock := lockfile.PackLock{LockfileVersion: 2, Packages: []lockfile.Package{{
		Module: "github.com/example/skills/review", Direct: true, Kind: lockfile.PackageSkill,
		Owner: "example", Repo: "skills", Path: "review", Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CacheKey: cacheKey,
	}}}
	if err := lock.Save(definition); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "cache", cacheKey), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "cache", cacheKey, "SKILL.md"), []byte("---\nname: review\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	projectSkill := filepath.Join(workspace, ".claude", "skills", "review")
	if err := os.MkdirAll(projectSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectSkill, "SKILL.md"), []byte("# review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := NewService().Preflight(definition, PreflightOptions{
		Policy: environment.PolicyCI, WorkspaceRoot: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range report.Findings {
		if finding.Code == "AMBIENT_ARTIFACT" && finding.Source == "review" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing AMBIENT_ARTIFACT: %#v", report.Findings)
	}
}

func TestPreflightReceiptStaleOnLockDigestMismatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	definition := t.TempDir()
	workspace := t.TempDir()
	if err := manifest.WriteStub(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	receipt := environment.Receipt{
		SchemaVersion: 1, Phase: "probe", ReceiptID: "stale-demo",
		LockDigest: "sha256:deadbeef", Mode: "default",
		Target:        environment.ReceiptTarget{Adapter: "claude"},
		Coverage:      []environment.CoverageNote{{Category: "skill", Scope: "native_catalog", Completeness: "complete"}},
		OverallStatus: "ready",
	}
	if _, err := environment.SaveReceipt(workspace, receipt); err != nil {
		t.Fatal(err)
	}
	target := base.Claude
	report, err := NewService().Preflight(definition, PreflightOptions{
		Target: &target, WorkspaceRoot: workspace, ReceiptID: "stale-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range report.Findings {
		if finding.Code == "RECEIPT_STALE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing RECEIPT_STALE: %#v", report.Findings)
	}
}

func TestPreflightContractNoWorkspaceWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		t.Fatal(err)
	}
	definition := t.TempDir()
	if err := manifest.WriteStub(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	contractPath := filepath.Join(definition, "contract.json")
	if err := os.WriteFile(contractPath, []byte(`{
		"schema_version": 1,
		"unknown_required": "fail",
		"requirements": [{
			"id": "no-checkout-config-writes",
			"target": "*",
			"selector": {"category": "planned_write", "property": "workspace_configuration"},
			"predicate": "no_workspace_write",
			"minimum_evidence": "generated",
			"severity": "error"
		}],
		"allowances": []
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	target := base.Claude
	report, err := NewService().Preflight(definition, PreflightOptions{
		Target: &target, Policy: environment.PolicyLocal, StrictExternal: true, WorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ContractResults) != 1 || report.ContractResults[0].Result != "satisfied" {
		t.Fatalf("contract %#v", report.ContractResults)
	}
}

func TestReceiptFreshnessRejectsInputsPolicyBinaryAndFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTPACK_HOME", filepath.Join(home, "state"))
	definition, workspace := t.TempDir(), t.TempDir()
	if err := manifest.WriteStub(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(home, "claude")
	if err := os.WriteFile(executable, []byte("fake-native-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_PATH", executable)
	target := base.Claude
	options := PreflightOptions{Target: &target, WorkspaceRoot: workspace, Policy: environment.PolicyLocal}
	report, err := NewService().Preflight(definition, options)
	if err != nil {
		t.Fatal(err)
	}
	receipt := environment.Receipt{SchemaVersion: 1, Phase: "probe", ReceiptID: "fresh", CreatedAt: time.Now().UTC(), SourceIdentity: report.SourceIdentity, LockDigest: report.LockDigest, WorkspaceDigest: report.WorkspaceDigest, GenerationID: report.GenerationID, PolicyDigest: report.PolicyDigest, Mode: report.Mode, Target: report.ExpectedTarget, Termination: environment.Termination{Status: "completed"}, OverallStatus: "ready", Properties: []environment.ObservedProperty{{Category: "skill", ArtifactID: "bundle/skills/review", Property: "presence", Value: true, Evidence: environment.EvidenceObserved, Method: "native_catalog"}}}
	receipt.Target.NativeVersion = "test-version"
	evidence := filepath.Join(home, "native-evidence.json")
	if err := os.WriteFile(evidence, []byte(`{"inventory":"review"}`), 0600); err != nil {
		t.Fatal(err)
	}
	evidenceDigest, err := environment.ExecutableIdentity(evidence)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Properties[0].EvidenceRef = evidence
	receipt.Properties[0].EvidenceDigest = evidenceDigest

	if _, err := environment.SaveReceipt(workspace, receipt); err != nil {
		t.Fatal(err)
	}
	options.ReceiptID = "fresh"
	check := func(wantCode string) {
		t.Helper()
		report, err := NewService().Preflight(definition, options)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range report.Findings {
			found = found || f.Code == wantCode
		}
		if !found {
			t.Fatalf("missing %s: %#v", wantCode, report.Findings)
		}
	}
	check("NATIVE_OBSERVED")
	options.Policy = environment.PolicyCI
	check("RECEIPT_STALE")
	options.Policy = environment.PolicyLocal
	if err := os.WriteFile(executable, []byte("fake-native-b"), 0o755); err != nil {
		t.Fatal(err)
	}
	check("RECEIPT_STALE")
	if err := os.WriteFile(executable, []byte("fake-native-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "CLAUDE.md"), []byte("changed native instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	check("RECEIPT_STALE")
	if err := os.Remove(filepath.Join(workspace, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidence, []byte(`{"inventory":"modified"}`), 0600); err != nil {
		t.Fatal(err)
	}
	check("OBSERVATION_UNAVAILABLE")
	if err := os.WriteFile(evidence, []byte(`{"inventory":"review"}`), 0600); err != nil {
		t.Fatal(err)
	}
	receipt.Termination.NativeExitCode = 17
	receipt.OverallStatus = "error"
	if _, err := environment.SaveReceipt(workspace, receipt); err != nil {
		t.Fatal(err)
	}
	check("PROBE_FAILED")
}

func TestStrictCICleanHomeDoesNotViolateInheritanceBoilerplate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTPACK_HOME", filepath.Join(home, "state"))
	definition := t.TempDir()
	if err := manifest.WriteStub(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(definition, "demo", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(home, "claude")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_PATH", exe)
	target := base.Claude
	options := PreflightOptions{Target: &target, WorkspaceRoot: t.TempDir(), Policy: environment.PolicyCI, StrictExternal: true}
	report, err := NewService().Preflight(definition, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("clean CI falsely blocked: %#v", report.Findings)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"custom":"setting"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err = NewService().Preflight(definition, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatal("existing undeclared configuration did not block CI")
	}
}

func TestEffectivePlanFiltersModesAndUsesNativeRenderedProperties(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTPACK_HOME", home)
	key := strings.Repeat("a", 64)
	root := filepath.Join(home, "cache", key)
	if err := os.MkdirAll(filepath.Join(root, "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := []byte("---\nname: review\n---\nReview changes.\n")
	if err := os.WriteFile(filepath.Join(root, "skills", "review", "SKILL.md"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "commands", "check.md"), []byte("Check code"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := lockfile.PackLock{Packages: []lockfile.Package{{Module: "github.com/team/pack", Kind: lockfile.PackagePlugin, CacheKey: key, Name: "pack"}}}
	selected, err := mode.NewEffective("review", mode.Definition{Base: mode.BaseAll, Disable: []string{"package-path:github.com/team/pack:skills/review/SKILL.md"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	target := base.Codex
	records, err := plannedArtifacts(lock, selected, t.TempDir(), &target)
	if err != nil {
		t.Fatal(err)
	}
	disabled, converted := false, false
	for _, record := range records {
		if record.Name == "review" {
			disabled = record.Omitted
		}
		if record.Name == "check" {
			converted = record.Kind == "skill" && record.OutputPath == "skills/check/SKILL.md" && record.Properties["rendered_digest"] != nil && record.Properties["source_digest"] != record.Properties["rendered_digest"]
		}
	}
	if !disabled || !converted {
		t.Fatalf("mode/render plan mismatch: %#v", records)
	}
}

func TestContractWarningSeverityIsNonBlocking(t *testing.T) {
	report := environment.Report{}
	applyContract(&report, environment.Contract{SchemaVersion: 1, Requirements: []environment.Requirement{{ID: "optional", Predicate: "present", Selector: map[string]any{"category": "skill", "name": "missing"}, Severity: environment.SeverityWarning}}})
	if got := overallStatus(report); got != "ready_with_warnings" {
		t.Fatalf("warning status=%s", got)
	}
}

func TestPreflightDetectsMaterializationInputsChangedAfterSync(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTPACK_HOME", filepath.Join(home, "state"))
	t.Setenv("AGENTPACK_STAGING_ROOT", t.TempDir())
	definition, workspace := t.TempDir(), t.TempDir()
	if err := manifest.WriteStub(definition, "materialized", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Init(definition, "materialized", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(home, "claude")
	if err := os.WriteFile(exe, []byte("fake-native"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_PATH", exe)
	target := base.Claude
	options := PreflightOptions{Target: &target, Policy: environment.PolicyLocal, WorkspaceRoot: workspace}
	if _, err := NewService().Sync(context.Background(), definition, SyncOptions{Target: &target, WorkspaceRoot: workspace, Frozen: true}); err != nil {
		t.Fatal(err)
	}
	stale := func(want bool) {
		t.Helper()
		report, err := NewService().Preflight(definition, options)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, finding := range report.Findings {
			found = found || finding.Code == "STAGING_INPUTS_STALE"
		}
		if found != want {
			t.Fatalf("stale=%t want %t findings=%#v", found, want, report.Findings)
		}
	}
	stale(false)
	original, err := os.ReadFile(paths.ManifestPath(definition))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ManifestPath(definition), append(original, []byte("\n[modes.default]\nbase=\"none\"\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	stale(true)
	if err := os.WriteFile(paths.ManifestPath(definition), original, 0600); err != nil {
		t.Fatal(err)
	}
	stale(false)
	options.WorkspaceRoot = t.TempDir()
	stale(true)
	options.WorkspaceRoot = workspace
	if err := os.MkdirAll(filepath.Join(workspace, ".agents", "skills", "review"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".agents", "skills", "review", "SKILL.md"), []byte("Review changes"), 0600); err != nil {
		t.Fatal(err)
	}
	stale(true)
}
