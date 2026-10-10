package environment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	if err := decodeContract(data, &contract); err != nil {
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
		if req.Target != "" && req.Target != "*" {
			switch req.Target {
			case "claude", "codex", "opencode", "grok", "cursor", "agy":
			default:
				return fmt.Errorf("requirement %q: unknown target %q", req.ID, req.Target)
			}
		}
		for key := range req.Selector {
			switch key {
			case "category", "name", "property", "scope":
			default:
				return fmt.Errorf("requirement %q: unsupported selector %q", req.ID, key)
			}
		}
		category, ok := req.Selector["category"].(string)
		if !ok || category == "" {
			return fmt.Errorf("requirement %q: selector category is required", req.ID)
		}
		property, _ := req.Selector["property"].(string)
		for _, key := range []string{"name", "property", "scope"} {
			if value, exists := req.Selector[key]; exists {
				if _, ok := value.(string); !ok {
					return fmt.Errorf("requirement %q: selector %s must be a string", req.ID, key)
				}
			}
		}
		name, _ := req.Selector["name"].(string)
		scope, _ := req.Selector["scope"].(string)
		if scope != "" && scope != "native_catalog" && scope != "controlled_projection" {
			return fmt.Errorf("requirement %q: unsupported observation scope %q", req.ID, scope)
		}
		if strings.ContainsAny(name, "*?") {
			return fmt.Errorf("requirement %q: artifact names are exact, wildcards unsupported", req.ID)
		}
		if (req.Predicate == "equals" || req.Predicate == "native_property" || req.Predicate == "source_allowed") && property == "" {
			return fmt.Errorf("requirement %q: selector property is required", req.ID)
		}
		if req.Predicate == "source_allowed" && property != "source" && property != "module" && property != "winner" {
			return fmt.Errorf("requirement %q: source_allowed requires source, module, or winner", req.ID)
		}

		properties := map[string][]string{
			"skill":   {"presence", "source_digest", "rendered_digest", "source", "module", "winner", "output_path", "scope_preserved", "source_kind", "dropped_fields"},
			"plugin":  {"presence", "source_digest", "source", "module", "winner"},
			"command": {"presence", "source_digest", "rendered_digest", "source", "module", "winner", "output_path", "scope_preserved", "source_kind", "dropped_fields"},
			"agent":   {"presence", "source_digest", "rendered_digest", "source", "module", "winner", "output_path", "scope_preserved", "source_kind", "dropped_fields"},
			"rule":    {"presence", "source_digest", "rendered_digest", "source", "module", "winner", "output_path", "scope_preserved", "source_kind", "dropped_fields"},
			"runtime": {"native_version"}, "planned_write": {"workspace_configuration"},
		}
		allowed, known := properties[category]
		if !known {
			return fmt.Errorf("requirement %q: unknown category %q", req.ID, category)
		}
		if property != "" {
			valid := false
			for _, value := range allowed {
				valid = valid || value == property
			}
			if !valid {
				return fmt.Errorf("requirement %q: unknown property %q for %s", req.ID, property, category)
			}
		}
		switch req.Severity {
		case "", SeverityInfo, SeverityWarning, SeverityError, SeverityViolation:
		default:
			return fmt.Errorf("requirement %q: invalid severity", req.ID)
		}
		if (req.Predicate == "equals" || req.Predicate == "native_property" || req.Predicate == "source_allowed") && req.Expected == nil {
			return fmt.Errorf("requirement %q: expected value is required", req.ID)
		}
		if req.Predicate == "source_allowed" {
			valid := false
			switch expected := req.Expected.(type) {
			case string:
				valid = strings.TrimSpace(expected) != ""
			case []any:
				valid = len(expected) > 0
				for _, value := range expected {
					allowed, ok := value.(string)
					valid = valid && ok && strings.TrimSpace(allowed) != ""
				}
			case []string:
				valid = len(expected) > 0
				for _, value := range expected {
					valid = valid && strings.TrimSpace(value) != ""
				}
			}
			if !valid {
				return fmt.Errorf("requirement %q: source_allowed expected must be a string or nonempty string list", req.ID)
			}
		}
		if category == "planned_write" && req.Predicate != "no_workspace_write" && req.Predicate != "present" && req.Predicate != "absent" && req.Predicate != "equals" {
			return fmt.Errorf("requirement %q: incompatible planned_write predicate", req.ID)
		}
		if (req.Predicate == "present" || req.Predicate == "absent") && property != "" && property != "presence" && !(category == "planned_write" && property == "workspace_configuration") {
			return fmt.Errorf("requirement %q: presence predicate requires a presence property", req.ID)
		}
		if req.Predicate == "no_workspace_write" && category != "planned_write" {
			return fmt.Errorf("requirement %q: no_workspace_write requires planned_write category", req.ID)
		}
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
	for index, left := range contract.Requirements {
		for _, right := range contract.Requirements[index+1:] {
			opposite := (left.Predicate == "present" && right.Predicate == "absent") || (left.Predicate == "absent" && right.Predicate == "present")
			if opposite && left.Target == right.Target && left.MinimumEvidence == right.MinimumEvidence && valuesEqual(left.Selector, right.Selector) {
				return fmt.Errorf("requirements %q and %q contradict one another", left.ID, right.ID)
			}
		}
	}
	for _, allowance := range contract.Allowances {
		if !seen[allowance.RequirementID] || strings.TrimSpace(allowance.Rationale) == "" {
			return fmt.Errorf("allowance requires a known requirement id and rationale")
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
	result := ContractResult{RequirementID: req.ID}
	finish := func(outcome, code, message string) ContractResult {
		result.Result, result.FindingCode, result.Message = outcome, code, message
		return result
	}
	unknown := func() ContractResult {
		return finish("unknown", "OBSERVATION_UNAVAILABLE", "required property evidence is unavailable or incomplete")
	}
	target := strings.ToLower(strings.TrimSpace(req.Target))
	if target != "" && target != "*" {
		if report.Target == "" {
			return unknown()
		}
		if target != report.Target {
			return finish("not_applicable", "", "target out of scope")
		}
	}
	category, _ := req.Selector["category"].(string)
	name, _ := req.Selector["name"].(string)
	property, _ := req.Selector["property"].(string)
	scope, _ := req.Selector["scope"].(string)
	if scope == "" {
		scope = "native_catalog"
	}
	predicate := strings.ToLower(req.Predicate)
	minimum := EvidenceLevel(strings.ToLower(req.MinimumEvidence))
	if minimum == "" {
		minimum = EvidenceGenerated
	}
	if category == "planned_write" || predicate == "no_workspace_write" {
		if minimum == EvidenceObserved {
			return unknown()
		}
		if report.Target == "" {
			return unknown()
		}
		present := len(report.PlannedWrites) > 0
		if predicate == "no_workspace_write" {
			if !present {
				return finish("satisfied", "", "")
			}
			return finish("violated", "WORKSPACE_WRITE_REQUIRED", "planned workspace writes: "+strings.Join(report.PlannedWrites, ", "))
		}
		return compareProperty(req, present)
	}
	if predicate == "coverage_complete" {
		if !report.HasObservedCoverageScope(category, scope) {
			return unknown()
		}
		return finish("satisfied", "", "")
	}
	if property == "" {
		property = "presence"
	}
	observed := minimum == EvidenceObserved || predicate == "native_property"
	if observed {
		for _, prop := range report.Properties {
			if prop.Evidence != EvidenceObserved || prop.Property != property || !propertyMatches(prop, category, name) || (prop.Scope != scope && !(prop.Scope == "" && scope == "native_catalog")) {
				continue
			}
			if prop.Value == nil {
				return unknown()
			}
			if (predicate == "present" || predicate == "absent") && valuesEqual(prop.Value, false) && !report.HasObservedCoverageScope(category, scope) {
				return unknown()
			}
			return compareProperty(req, prop.Value)
		}
		if (predicate == "absent" || predicate == "present") && report.HasObservedCoverageScope(category, scope) {
			return compareProperty(req, false)
		}
		return unknown()
	}
	if property == "presence" {
		for _, finding := range report.Findings {
			if (finding.Code == "CONFIG_SHADOWED" || finding.Code == "AMBIENT_ARTIFACT") && finding.Source == name {
				return compareProperty(req, true)
			}
		}
	}
	insufficient := false
	for _, artifact := range report.Artifacts {
		if artifact.Omitted || artifact.Kind != category || (name != "" && name != artifact.Name && name != artifact.ID) {
			continue
		}
		if !evidenceAtLeast(artifact.Evidence, minimum) {
			insufficient = true
			continue
		}
		if property == "presence" {
			if artifact.Omitted {
				continue
			}
			return compareProperty(req, true)
		}
		var value any
		switch property {
		case "source", "module":
			value = artifact.Module
		case "winner":
			value = artifact.Winner
		default:
			value = artifact.Properties[property]
		}
		if value == nil {
			return unknown()
		}
		return compareProperty(req, value)
	}
	if insufficient {
		return unknown()
	}
	if predicate == "present" || predicate == "absent" {
		return compareProperty(req, false)
	}
	return unknown()
}

func propertyMatches(prop ObservedProperty, category, name string) bool {
	propCategory := prop.Category
	if propCategory == "" {
		switch {
		case prop.ArtifactID == "":
			propCategory = "runtime"
		case strings.Contains(filepath.ToSlash(prop.ArtifactID), "/skills/"):
			propCategory = "skill"
		default:
			propCategory = "plugin"
		}
	}
	if propCategory != category {
		return false
	}
	return name == "" || prop.ArtifactID == name || filepath.Base(prop.ArtifactID) == name
}

func compareProperty(req Requirement, value any) ContractResult {
	matched := false
	switch strings.ToLower(req.Predicate) {
	case "present":
		flag, ok := value.(bool)
		matched = ok && flag
	case "absent":
		flag, ok := value.(bool)
		matched = ok && !flag
	case "source_allowed":
		switch expected := req.Expected.(type) {
		case string:
			matched = valuesEqual(value, expected)
		case []any:
			for _, allowed := range expected {
				matched = matched || valuesEqual(value, allowed)
			}
		case []string:
			for _, allowed := range expected {
				matched = matched || valuesEqual(value, allowed)
			}
		}
	default:
		matched = valuesEqual(value, req.Expected)
	}
	result := ContractResult{RequirementID: req.ID, Result: "satisfied"}
	if !matched {
		result.Result = "violated"
		result.FindingCode = "CONTRACT_MISMATCH"
		result.Message = fmt.Sprintf("requirement %q: property does not match %s", req.ID, req.Predicate)
	}
	return result
}

func valuesEqual(left, right any) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

func evidenceAtLeast(actual, minimum EvidenceLevel) bool {
	ranks := map[EvidenceLevel]int{EvidenceDeclared: 1, EvidenceGenerated: 2, EvidenceObserved: 3}
	return ranks[actual] != 0 && ranks[actual] >= ranks[minimum]
}

func decodeContract(data []byte, contract *Contract) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(contract); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("contract must contain one JSON object")
	}
	return nil
}
