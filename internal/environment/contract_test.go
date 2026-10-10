package environment

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateAndEvaluateContract(t *testing.T) {
	t.Parallel()
	contract := Contract{
		SchemaVersion:   1,
		UnknownRequired: "fail",
		Requirements: []Requirement{
			{
				ID: "no-writes", Target: "*", Predicate: "no_workspace_write",
				Selector:        map[string]any{"category": "planned_write", "property": "workspace_configuration"},
				MinimumEvidence: "generated", Severity: SeverityError,
			},
			{
				ID: "no-ambient", Target: "claude", Predicate: "absent",
				Selector:        map[string]any{"category": "skill", "name": "helper", "property": "presence"},
				MinimumEvidence: "observed", Severity: SeverityError,
			},
		},
	}
	if err := ValidateContract(contract); err != nil {
		t.Fatal(err)
	}
	report := Report{Target: "claude", Skills: 1}
	results := EvaluateContract(contract, report)
	if len(results) != 2 || results[0].Result != "satisfied" {
		t.Fatalf("results[0]=%#v", results)
	}
	if results[1].Result != "unknown" || results[1].FindingCode != "OBSERVATION_UNAVAILABLE" {
		t.Fatalf("absence without observed coverage must be unknown: %#v", results[1])
	}

	report.PlannedWrites = []string{"/tmp/ws/.cursor/agents"}
	results = EvaluateContract(contract, report)
	if results[0].Result != "violated" {
		t.Fatalf("planned writes should violate: %#v", results[0])
	}
}

func TestLoadContractRejectsBadSchema(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "contract.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":99,"requirements":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadContract(path); err == nil {
		t.Fatal("expected schema error")
	}
}

func TestContractUsesNamedEffectiveArtifactsAndPropertyEvidence(t *testing.T) {
	report := Report{Target: "claude", Skills: 7, Artifacts: []ArtifactRecord{{ID: "pkg:review", Kind: "skill", Name: "review", Module: "github.com/team/pack", Winner: "package", Evidence: EvidenceGenerated, Properties: map[string]any{"source_digest": "sha256:review"}}, {Kind: "skill", Name: "disabled", Omitted: true, Evidence: EvidenceGenerated}}}
	cases := []struct {
		name, predicate, property, evidence string
		expected                            any
		want                                string
	}{
		{"missing", "present", "presence", "generated", nil, "violated"},
		{"disabled", "present", "presence", "generated", nil, "violated"},
		{"review", "present", "presence", "generated", nil, "satisfied"},
		{"review", "equals", "source_digest", "generated", "sha256:review", "satisfied"},
		{"review", "equals", "source_digest", "observed", "sha256:review", "unknown"},
		{"review", "source_allowed", "source", "generated", []any{"github.com/team/pack"}, "satisfied"},
		{"review", "source_allowed", "source", "generated", []any{"github.com/other/pack"}, "violated"},
	}
	for _, tc := range cases {
		t.Run(tc.name+tc.predicate+tc.evidence, func(t *testing.T) {
			req := Requirement{ID: "check", Target: "claude", Predicate: tc.predicate, Selector: map[string]any{"category": "skill", "name": tc.name, "property": tc.property}, MinimumEvidence: tc.evidence, Expected: tc.expected}
			result := evaluateRequirement(req, report)
			if result.Result != tc.want {
				t.Fatalf("got %#v want %s", result, tc.want)
			}
		})
	}
}

func TestObservedPresenceRequiresMatchingPropertyAndAbsenceRequiresCatalog(t *testing.T) {
	report := Report{Target: "claude", Coverage: []CoverageNote{{Category: "skill", Scope: "native_catalog", Completeness: "complete"}}, Properties: []ObservedProperty{{Category: "skill", ArtifactID: "bundle/skills/review", Property: "presence", Value: true, Evidence: EvidenceObserved, Method: "native_catalog"}}}
	req := Requirement{ID: "review", Target: "claude", Predicate: "present", Selector: map[string]any{"category": "skill", "name": "review"}, MinimumEvidence: "observed"}
	if result := evaluateRequirement(req, report); result.Result != "satisfied" {
		t.Fatalf("positive observation: %#v", result)
	}
	req.Predicate = "absent"
	req.Selector["name"] = "other"
	if result := evaluateRequirement(req, report); result.Result != "unknown" {
		t.Fatalf("coverage claim alone: %#v", result)
	}
	report.Properties = append(report.Properties, ObservedProperty{Category: "skill", Property: "catalog_complete", Value: true, Evidence: EvidenceObserved, Method: "native_catalog"})
	if result := evaluateRequirement(req, report); result.Result != "satisfied" {
		t.Fatalf("complete observed absence: %#v", result)
	}
	req.Predicate = "coverage_complete"
	if result := evaluateRequirement(req, report); result.Result != "satisfied" {
		t.Fatalf("coverage predicate: %#v", result)
	}
	report.Properties = append(report.Properties, ObservedProperty{Category: "runtime", Property: "native_version", Value: "2.0", Evidence: EvidenceObserved, Method: "version"})
	req = Requirement{ID: "version", Predicate: "native_property", Selector: map[string]any{"category": "runtime", "property": "native_version"}, Expected: "2.0"}
	if result := evaluateRequirement(req, report); result.Result != "satisfied" {
		t.Fatalf("native equals: %#v", result)
	}
}

func TestContractRejectsTyposAndUnscopedAllowance(t *testing.T) {
	for _, req := range []Requirement{
		{ID: "x", Target: "claud", Predicate: "present", Selector: map[string]any{"category": "skill"}},
		{ID: "x", Predicate: "present", Selector: map[string]any{"category": "skills"}},
		{ID: "x", Predicate: "equals", Selector: map[string]any{"category": "skill", "property": "soruce"}, Expected: "x"},
	} {
		if err := ValidateContract(Contract{SchemaVersion: 1, Requirements: []Requirement{req}}); err == nil {
			t.Fatalf("accepted invalid %#v", req)
		}
	}
	if err := ValidateContract(Contract{SchemaVersion: 1, Allowances: []Allowance{{RequirementID: "none", Rationale: ""}}}); err == nil {
		t.Fatal("accepted unknown allowance")
	}
}

func TestControlledObservationRequiresExplicitScope(t *testing.T) {
	report := Report{Target: "codex", Properties: []ObservedProperty{{Category: "skill", Scope: "controlled_projection", ArtifactID: "review", Property: "presence", Value: true, Evidence: EvidenceObserved, Method: "native_catalog"}}}
	req := Requirement{ID: "review", Target: "codex", Predicate: "present", Selector: map[string]any{"category": "skill", "name": "review"}, MinimumEvidence: "observed"}
	if result := evaluateRequirement(req, report); result.Result != "unknown" {
		t.Fatalf("controlled observation misrepresented actual launch: %#v", result)
	}
	req.Selector["scope"] = "controlled_projection"
	if result := evaluateRequirement(req, report); result.Result != "satisfied" {
		t.Fatalf("explicit scope should satisfy positive observation: %#v", result)
	}
	req.Predicate = "absent"
	req.Selector["name"] = "other"
	if result := evaluateRequirement(req, report); result.Result != "unknown" {
		t.Fatalf("positive projection inferred absence: %#v", result)
	}
}

func TestNegativeObservedPresenceRequiresCompleteScopeCoverage(t *testing.T) {
	report := Report{Target: "claude", Properties: []ObservedProperty{{Category: "plugin", Scope: "controlled_projection", ArtifactID: "bundle", Property: "presence", Value: false, Evidence: EvidenceObserved, Method: "native_catalog"}}}
	req := Requirement{ID: "absent", Target: "claude", Predicate: "absent", Selector: map[string]any{"category": "plugin", "name": "bundle", "scope": "controlled_projection"}, MinimumEvidence: "observed"}
	if got := evaluateRequirement(req, report); got.Result != "unknown" {
		t.Fatalf("partial negative observation proved absence: %#v", got)
	}
	report.Coverage = []CoverageNote{{Category: "plugin", Scope: "controlled_projection", Completeness: "complete"}}
	report.Properties = append(report.Properties, ObservedProperty{Category: "plugin", Scope: "controlled_projection", Property: "catalog_complete", Value: true, Evidence: EvidenceObserved, Method: "native_catalog"})
	if got := evaluateRequirement(req, report); got.Result != "satisfied" {
		t.Fatalf("complete catalog negative should satisfy: %#v", got)
	}
}
