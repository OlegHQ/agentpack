package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Contract is a versioned requirement set attached to an external definition.
type Contract struct {
	SchemaVersion   int           `json:"schema_version"`
	Requirements    []Requirement `json:"requirements"`
	Allowances      []Allowance   `json:"allowances"`
	UnknownRequired string        `json:"unknown_required"` // fail | warn | allow
}

// Requirement is one evaluable check. Predicates are closed; never evaluate pack-supplied expressions.
type Requirement struct {
	ID              string         `json:"id"`
	Target          string         `json:"target"`
	Selector        map[string]any `json:"selector"`
	Predicate       string         `json:"predicate"`
	Expected        any            `json:"expected,omitempty"`
	MinimumEvidence string         `json:"minimum_evidence"`
	Severity        Severity       `json:"severity"`
}

// Allowance scopes an exception with a rationale (no blanket ignore-all).
type Allowance struct {
	RequirementID string `json:"requirement_id"`
	Rationale     string `json:"rationale"`
}

// ContractResult is one requirement outcome.
type ContractResult struct {
	RequirementID string `json:"requirement_id"`
	Result        string `json:"result"` // satisfied | violated | unknown | not_applicable
	FindingCode   string `json:"finding_code,omitempty"`
	Message       string `json:"message,omitempty"`
}

// LoadContract reads a JSON contract file.
func LoadContract(path string) (Contract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Contract{}, err
	}
	var contract Contract
	if err := json.Unmarshal(data, &contract); err != nil {
		return Contract{}, fmt.Errorf("parse contract: %w", err)
	}
	if err := ValidateContract(contract); err != nil {
		return Contract{}, err
	}
	return contract, nil
}

// ValidateContract rejects unknown schema majors and invalid requirement shapes.
func ValidateContract(contract Contract) error {
	if contract.SchemaVersion != 1 {
		return fmt.Errorf("unsupported contract schema_version %d (want 1)", contract.SchemaVersion)
	}
	switch strings.ToLower(strings.TrimSpace(contract.UnknownRequired)) {
	case "", "fail", "warn", "allow":
	default:
		return fmt.Errorf("unknown_required must be fail, warn, or allow")
	}
	seen := map[string]bool{}
	for _, req := range contract.Requirements {
		if strings.TrimSpace(req.ID) == "" {
			return fmt.Errorf("requirement id is required")
		}
		if seen[req.ID] {
			return fmt.Errorf("duplicate requirement id %q", req.ID)
		}
		seen[req.ID] = true
		switch strings.ToLower(req.Predicate) {
		case "present", "absent", "equals", "source_allowed", "native_property", "no_workspace_write", "coverage_complete":
		default:
			return fmt.Errorf("requirement %q: unsupported predicate %q", req.ID, req.Predicate)
		}
		switch strings.ToLower(req.MinimumEvidence) {
		case "", "declared", "generated", "observed":
		default:
			return fmt.Errorf("requirement %q: unsupported minimum_evidence %q", req.ID, req.MinimumEvidence)
		}
	}
	return nil
}

// EvaluateContract scores requirements against a preflight report.
// Observed-only predicates that lack receipts stay unknown (never forged from generated digests).
func EvaluateContract(contract Contract, report Report) []ContractResult {
	allow := map[string]bool{}
	for _, item := range contract.Allowances {
		allow[item.RequirementID] = true
	}
	var results []ContractResult
	for _, req := range contract.Requirements {
		result := evaluateRequirement(req, report)
		if allow[req.ID] && result.Result == "violated" {
			result.Result = "satisfied"
			result.FindingCode = "ALLOWED"
			result.Message = "scoped allowance accepted: " + result.Message
		}
		results = append(results, result)
	}
	return results
}

func evaluateRequirement(req Requirement, report Report) ContractResult {
	target := strings.ToLower(strings.TrimSpace(req.Target))
	if target != "" && target != "*" && report.Target != "" && !strings.EqualFold(target, report.Target) {
		return ContractResult{RequirementID: req.ID, Result: "not_applicable", Message: "target out of scope"}
	}
	category, _ := req.Selector["category"].(string)
	name, _ := req.Selector["name"].(string)
	property, _ := req.Selector["property"].(string)
	predicate := strings.ToLower(req.Predicate)
	minEvidence := strings.ToLower(req.MinimumEvidence)
	if minEvidence == "" {
		minEvidence = "generated"
	}

	switch predicate {
	case "no_workspace_write":
		if category != "" && category != "planned_write" {
			return ContractResult{RequirementID: req.ID, Result: "not_applicable", Message: "selector category not planned_write"}
		}
		if property != "" && property != "workspace_configuration" {
			return ContractResult{RequirementID: req.ID, Result: "not_applicable", Message: "selector property not workspace_configuration"}
		}
		if len(report.PlannedWrites) == 0 {
			return ContractResult{RequirementID: req.ID, Result: "satisfied"}
		}
		return ContractResult{
			RequirementID: req.ID,
			Result:        "violated",
			FindingCode:   "WORKSPACE_WRITE_REQUIRED",
			Message:       "planned workspace writes: " + strings.Join(report.PlannedWrites, ", "),
		}
	case "absent":
		if category == "skill" && (property == "presence" || property == "") {
			if hasFindingCode(report, "CONFIG_SHADOWED", name) || hasFindingCode(report, "AMBIENT_ARTIFACT", name) {
				return ContractResult{
					RequirementID: req.ID,
					Result:        "violated",
					FindingCode:   "AMBIENT_ARTIFACT",
					Message:       fmt.Sprintf("ambient skill %q is present", name),
				}
			}
			if minEvidence == "observed" && !report.HasObservedCoverage("skill") {
				return ContractResult{
					RequirementID: req.ID,
					Result:        "unknown",
					FindingCode:   "OBSERVATION_UNAVAILABLE",
					Message:       "absence of ambient skill requires complete native skill coverage",
				}
			}
			return ContractResult{RequirementID: req.ID, Result: "satisfied"}
		}
	case "present":
		if category == "skill" {
			if report.Skills > 0 || hasPackagedSkill(report, name) {
				if minEvidence == "observed" && !report.HasObservedCoverage("skill") {
					return ContractResult{
						RequirementID: req.ID,
						Result:        "unknown",
						FindingCode:   "OBSERVATION_UNAVAILABLE",
						Message:       "presence observed evidence unavailable; package listing is not native discovery",
					}
				}
				return ContractResult{RequirementID: req.ID, Result: "satisfied"}
			}
			return ContractResult{
				RequirementID: req.ID,
				Result:        "violated",
				FindingCode:   "ENV_NOT_FOUND",
				Message:       fmt.Sprintf("packaged skill %q not present in lock", name),
			}
		}
	case "equals", "source_allowed", "native_property", "coverage_complete":
		if minEvidence == "observed" && !report.HasObservedCoverage(category) {
			return ContractResult{
				RequirementID: req.ID,
				Result:        "unknown",
				FindingCode:   "OBSERVATION_UNAVAILABLE",
				Message:       "required observed evidence is unavailable",
			}
		}
		return ContractResult{
			RequirementID: req.ID,
			Result:        "unknown",
			FindingCode:   "OBSERVATION_UNAVAILABLE",
			Message:       "predicate not yet backed by native observation",
		}
	}
	return ContractResult{
		RequirementID: req.ID,
		Result:        "unknown",
		FindingCode:   "OBSERVATION_UNAVAILABLE",
		Message:       "requirement could not be evaluated",
	}
}

func hasFindingCode(report Report, code, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, finding := range report.Findings {
		if finding.Code != code {
			continue
		}
		if name == "" {
			return true
		}
		if strings.EqualFold(finding.Source, name) || strings.Contains(strings.ToLower(finding.Message), name) {
			return true
		}
	}
	return false
}

func hasPackagedSkill(report Report, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return report.Skills > 0
	}
	for _, artifact := range report.Artifacts {
		if artifact.Kind == "skill" && strings.EqualFold(artifact.Name, name) && !artifact.Omitted {
			return true
		}
	}
	return false
}
