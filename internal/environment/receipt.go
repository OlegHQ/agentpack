package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/OlegHQ/agentpack/internal/paths"
)

// Receipt is a local native observation record (not a publisher attestation).
type Receipt struct {
	SchemaVersion   int                `json:"schema_version"`
	Phase           string             `json:"phase"`
	ReceiptID       string             `json:"receipt_id"`
	SourceIdentity  string             `json:"source_id,omitempty"`
	LockDigest      string             `json:"lock_digest,omitempty"`
	GenerationID    string             `json:"generation_id,omitempty"`
	Mode            string             `json:"mode,omitempty"`
	PolicyDigest    string             `json:"policy_digest,omitempty"`
	Target          ReceiptTarget      `json:"target"`
	CreatedAt       time.Time          `json:"created_at"`
	Coverage        []CoverageNote     `json:"coverage"`
	Properties      []ObservedProperty `json:"properties"`
	ContractResults []ContractResult   `json:"contract_results,omitempty"`
	Findings        []Finding          `json:"findings"`
	Effects         ProbeEffects       `json:"effects"`
	Termination     Termination        `json:"termination"`
	OverallStatus   string             `json:"overall_status"`
}

// ReceiptTarget identifies the native adapter under test.
type ReceiptTarget struct {
	Adapter            string `json:"adapter"`
	AdapterRevision    string `json:"adapter_revision,omitempty"`
	ExecutableIdentity string `json:"executable_identity,omitempty"`
	NativeVersion      string `json:"native_version,omitempty"`
	CapabilityRevision string `json:"capability_revision,omitempty"`
}

// ObservedProperty is one graded native observation.
type ObservedProperty struct {
	ArtifactID  string        `json:"artifact_id,omitempty"`
	Property    string        `json:"property"`
	Value       any           `json:"value"`
	Evidence    EvidenceLevel `json:"evidence"`
	Method      string        `json:"method,omitempty"`
	EvidenceRef string        `json:"evidence_ref,omitempty"`
	Reason      string        `json:"reason,omitempty"`
}

// ProbeEffects declares measured/configured side effects.
type ProbeEffects struct {
	ModelCalls bool   `json:"model_calls"`
	Network    string `json:"network"`
	WriteScope string `json:"write_scope"`
}

// Termination records process outcome separately from policy status.
type Termination struct {
	Status         string `json:"status"`
	NativeExitCode int    `json:"native_exit_code"`
}

// ReceiptsRoot is $AGENTPACK_HOME/projects/<hash>/probe-receipts.
func ReceiptsRoot(workspaceRoot string) (string, error) {
	return paths.ProjectStateFile(workspaceRoot, "probe-receipts")
}

// SaveReceipt writes a receipt JSON file and returns its path.
func SaveReceipt(workspaceRoot string, receipt Receipt) (string, error) {
	root, err := ReceiptsRoot(workspaceRoot)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	if receipt.ReceiptID == "" {
		return "", fmt.Errorf("receipt_id is required")
	}
	path := filepath.Join(root, receipt.ReceiptID+".json")
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// LoadReceipt loads a receipt by ID or absolute path.
func LoadReceipt(workspaceRoot, idOrPath string) (Receipt, error) {
	path := idOrPath
	if !filepath.IsAbs(path) && !regularFile(path) {
		root, err := ReceiptsRoot(workspaceRoot)
		if err != nil {
			return Receipt{}, err
		}
		path = filepath.Join(root, idOrPath)
		if filepath.Ext(path) == "" {
			path += ".json"
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return Receipt{}, fmt.Errorf("parse receipt: %w", err)
	}
	if receipt.SchemaVersion != 1 {
		return Receipt{}, fmt.Errorf("unsupported receipt schema_version %d", receipt.SchemaVersion)
	}
	return receipt, nil
}
