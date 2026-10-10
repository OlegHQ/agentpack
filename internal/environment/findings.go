package environment

import (
	"fmt"
	"strings"

	base "github.com/OlegHQ/agentpack/internal/harness"
)

// Severity ranks a preflight finding for CI policy.
type Severity string

const (
	SeverityInfo      Severity = "info"
	SeverityWarning   Severity = "warning"
	SeverityError     Severity = "error"
	SeverityViolation Severity = "violation"
)

// Policy selects how inheritance and conversion findings are treated.
type Policy string

const (
	// PolicyLocal allows inherited user config (developer machines).
	PolicyLocal Policy = "local"
	// PolicyCI treats undeclared inheritance and conversion loss as violations.
	PolicyCI Policy = "ci"
)

func ParsePolicy(value string) (Policy, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "local", "inherit":
		return PolicyLocal, nil
	case "ci", "controlled":
		return PolicyCI, nil
	default:
		return "", fmt.Errorf("unknown preflight policy %q (want local or ci)", value)
	}
}

// EvidenceLevel grades how a property was established.
type EvidenceLevel string

const (
	EvidenceDeclared  EvidenceLevel = "declared"
	EvidenceGenerated EvidenceLevel = "generated"
	EvidenceObserved  EvidenceLevel = "observed"
	EvidenceUnknown   EvidenceLevel = "unknown"
)

// Finding is a stable, actionable preflight record.
type Finding struct {
	Code     string        `json:"code"`
	Severity Severity      `json:"severity"`
	Source   string        `json:"source,omitempty"`
	Target   string        `json:"target,omitempty"`
	Winner   string        `json:"winner,omitempty"`
	Message  string        `json:"message"`
	Remedy   string        `json:"remedy,omitempty"`
	Evidence EvidenceLevel `json:"evidence,omitempty"`
	Accepted bool          `json:"accepted,omitempty"`
}

// ArtifactRecord is a planned package artifact identity for effective-plan reports.
type ArtifactRecord struct {
	ID       string        `json:"id"`
	Kind     string        `json:"kind"`
	Name     string        `json:"name"`
	Module   string        `json:"module,omitempty"`
	Winner   string        `json:"winner,omitempty"` // package | user | project | plugin
	Omitted  bool          `json:"omitted,omitempty"`
	Reason   string        `json:"reason,omitempty"`
	Evidence EvidenceLevel `json:"evidence,omitempty"`
}

// CoverageNote records observation completeness for a category.
type CoverageNote struct {
	Category     string `json:"category"`
	Scope        string `json:"scope"`
	Completeness string `json:"completeness"` // complete | partial | none
	Reason       string `json:"reason,omitempty"`
}

// Report is the shared human/JSON preflight payload.
type Report struct {
	SchemaVersion   int               `json:"schema_version"`
	Phase           string            `json:"phase"`
	Environment     string            `json:"environment,omitempty"`
	DefinitionRoot  string            `json:"definition_root"`
	WorkspaceRoot   string            `json:"workspace_root"`
	Mode            string            `json:"mode"`
	Target          string            `json:"target,omitempty"`
	Policy          Policy            `json:"policy"`
	StrictExternal  bool              `json:"strict_external"`
	SourceIdentity  string            `json:"source_identity,omitempty"`
	LockDigest      string            `json:"lock_digest,omitempty"`
	Skills          int               `json:"skills"`
	Plugins         int               `json:"plugins"`
	Shadowed        int               `json:"shadowed"`
	Inheritance     []string          `json:"inheritance,omitempty"`
	Capabilities    map[string]string `json:"capabilities,omitempty"`
	PlannedWrites   []string          `json:"planned_writes,omitempty"`
	Artifacts       []ArtifactRecord  `json:"artifacts,omitempty"`
	Coverage        []CoverageNote    `json:"coverage,omitempty"`
	ContractResults []ContractResult  `json:"contract_results,omitempty"`
	Findings        []Finding         `json:"findings"`
	OverallStatus   string            `json:"overall_status"`
	OK              bool              `json:"ok"`
}

// HasObservedCoverage reports whether a category has complete observed coverage.
func (report Report) HasObservedCoverage(category string) bool {
	for _, note := range report.Coverage {
		if strings.EqualFold(note.Category, category) && note.Completeness == "complete" && note.Scope == "native_catalog" {
			return true
		}
	}
	return false
}

// ExternalCapability describes whether an adapter can run without checkout writes.
func ExternalCapability(target base.Target) (status, note string) {
	switch target {
	case base.Claude:
		return "external", "uses --plugin-dir and --settings; no workspace overlay"
	case base.OpenCode:
		return "external", "uses OPENCODE_CONFIG_DIR; no workspace overlay"
	case base.Codex:
		return "external-home", "uses CODEX_HOME; native project instructions may still apply"
	case base.Grok:
		return "external-home", "uses GROK_HOME; also reads real ~/.claude and ~/.grok"
	case base.Cursor:
		return "workspace-write", "writes .cursor/agents when agent artifacts exist"
	case base.Agy:
		return "workspace-write", "writes .agents/plugins/agentpack-bundle"
	default:
		return "unknown", ""
	}
}
