package environment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/paths"
)

// Receipt is a local native observation record (not a publisher attestation).
type Receipt struct {
	DiagnosticRefs  []string           `json:"diagnostic_refs,omitempty"`
	SchemaVersion   int                `json:"schema_version"`
	Phase           string             `json:"phase"`
	ReceiptID       string             `json:"receipt_id"`
	SourceIdentity  string             `json:"source_id,omitempty"`
	LockDigest      string             `json:"lock_digest,omitempty"`
	WorkspaceDigest string             `json:"workspace_digest,omitempty"`
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
	EvidenceDigest string        `json:"evidence_digest,omitempty"`
	Scope          string        `json:"scope,omitempty"`
	Category       string        `json:"category,omitempty"`
	ArtifactID     string        `json:"artifact_id,omitempty"`
	Property       string        `json:"property"`
	Value          any           `json:"value"`
	Evidence       EvidenceLevel `json:"evidence"`
	Method         string        `json:"method,omitempty"`
	EvidenceRef    string        `json:"evidence_ref,omitempty"`
	Reason         string        `json:"reason,omitempty"`
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
	if receipt.Coverage == nil {
		receipt.Coverage = []CoverageNote{}
	}
	if receipt.Properties == nil {
		receipt.Properties = []ObservedProperty{}
	}
	if receipt.Findings == nil {
		receipt.Findings = []Finding{}
	}
	root, err := ReceiptsRoot(workspaceRoot)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("receipt storage must be a managed directory")
	}
	if err := os.Chmod(root, 0700); err != nil {
		return "", err
	}
	if receipt.ReceiptID == "" || filepath.Base(receipt.ReceiptID) != receipt.ReceiptID || strings.ContainsAny(receipt.ReceiptID, `/\`) {
		return "", fmt.Errorf("receipt_id is required")
	}
	path := filepath.Join(root, receipt.ReceiptID+".json")
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return "", fmt.Errorf("receipt destination must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	file, err := os.CreateTemp(root, ".receipt-")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporary, path); err != nil {
		return "", err
	}
	return path, nil
}

// LoadReceipt loads a receipt by ID or absolute path.
func LoadReceipt(workspaceRoot, idOrPath string) (Receipt, error) {
	path := idOrPath
	if idOrPath == "latest" {
		root, err := ReceiptsRoot(workspaceRoot)
		if err != nil {
			return Receipt{}, err
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return Receipt{}, err
		}
		var newest Receipt
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			candidate, err := LoadReceipt(workspaceRoot, filepath.Join(root, entry.Name()))
			if err != nil || candidate.Termination.Status != "completed" || candidate.Termination.NativeExitCode != 0 || (candidate.OverallStatus != "ready" && candidate.OverallStatus != "ready_with_warnings") {
				continue
			}
			if candidate.CreatedAt.After(newest.CreatedAt) {
				newest = candidate
			}
		}
		if newest.ReceiptID == "" {
			return Receipt{}, fmt.Errorf("no successful current-format probe receipt; run probe")
		}
		return newest, nil
	}
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
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// ValidateReceipt checks record shape; unsuccessful records remain loadable for diagnostics.
func ValidateReceipt(receipt Receipt) error {
	if receipt.SchemaVersion != 1 {
		return fmt.Errorf("unsupported receipt schema_version %d", receipt.SchemaVersion)
	}
	if receipt.Phase != "probe" || receipt.ReceiptID == "" || receipt.CreatedAt.IsZero() {
		return fmt.Errorf("receipt missing probe identity or creation time; re-run probe")
	}
	if receipt.SourceIdentity == "" || receipt.LockDigest == "" || receipt.GenerationID == "" || receipt.WorkspaceDigest == "" || receipt.Mode == "" || receipt.PolicyDigest == "" {
		return fmt.Errorf("receipt missing required freshness identities; re-run probe")
	}
	target := receipt.Target
	if target.Adapter == "" || target.AdapterRevision == "" || target.ExecutableIdentity == "" || target.CapabilityRevision == "" {
		return fmt.Errorf("receipt missing native adapter identity; re-run probe")
	}
	switch receipt.Termination.Status {
	case "completed", "failed", "cancelled", "timed_out":
	default:
		return fmt.Errorf("receipt missing process termination")
	}
	switch receipt.OverallStatus {
	case "ready", "ready_with_warnings", "violated", "unknown", "error":
	default:
		return fmt.Errorf("receipt has invalid overall status")
	}
	seenCoverage := map[string]bool{}
	for _, note := range receipt.Coverage {
		key := note.Category + "\x00" + note.Scope
		if seenCoverage[key] {
			return fmt.Errorf("receipt has duplicate coverage identity")
		}
		seenCoverage[key] = true
		if note.Category == "" || note.Scope == "" {
			return fmt.Errorf("receipt has incomplete coverage identity")
		}
		switch note.Completeness {
		case "complete", "partial", "none":
		default:
			return fmt.Errorf("receipt has invalid coverage completeness")
		}
	}
	seenProperties := map[string]bool{}
	for _, prop := range receipt.Properties {
		key := prop.Category + "\x00" + prop.Scope + "\x00" + prop.ArtifactID + "\x00" + prop.Property
		if seenProperties[key] {
			return fmt.Errorf("receipt has duplicate property identity")
		}
		seenProperties[key] = true
		if prop.Property == "" {
			return fmt.Errorf("receipt property name is required")
		}
		switch prop.Evidence {
		case EvidenceDeclared, EvidenceGenerated, EvidenceObserved, EvidenceUnknown:
		default:
			return fmt.Errorf("receipt has invalid evidence level")
		}
		if prop.Evidence == EvidenceObserved && (prop.Method == "" || prop.Value == nil) {
			return fmt.Errorf("observed property requires a method and value")
		}
	}
	return nil
}

// ExecutableIdentity hashes native bytes without running the executable.
func ExecutableIdentity(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("identity input must be a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
