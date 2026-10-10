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
