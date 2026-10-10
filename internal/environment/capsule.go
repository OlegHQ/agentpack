package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SupportCapsule is a redacted, opt-in diagnostic export. It never includes
// credentials, raw environment values, or absolute private path contents.
type SupportCapsule struct {
	SchemaVersion int                `json:"schema_version"`
	ExportedAt    time.Time          `json:"exported_at"`
	Scope         string             `json:"scope"`
	ReceiptID     string             `json:"receipt_id"`
	Adapter       string             `json:"adapter,omitempty"`
	NativeVersion string             `json:"native_version,omitempty"`
	SourceID      string             `json:"source_id_prefix,omitempty"`
	LockDigest    string             `json:"lock_digest_prefix,omitempty"`
	Mode          string             `json:"mode,omitempty"`
	Coverage      []CoverageNote     `json:"coverage,omitempty"`
	Findings      []Finding          `json:"findings,omitempty"`
	Properties    []ObservedProperty `json:"properties,omitempty"`
	OverallStatus string             `json:"overall_status,omitempty"`
	Notes         []string           `json:"notes"`
}

// ExportSupportCapsule builds a redacted capsule from a receipt.
func ExportSupportCapsule(receipt Receipt) SupportCapsule {
	capsule := SupportCapsule{
		SchemaVersion: 1,
		ExportedAt:    time.Now().UTC(),
		Scope:         "local-diagnostic; no credentials; paths pseudonymized",
		ReceiptID:     receipt.ReceiptID,
		Adapter:       receipt.Target.Adapter,
		NativeVersion: receipt.Target.NativeVersion,
		SourceID:      prefixDigest(receipt.SourceIdentity, 16),
		LockDigest:    prefixDigest(receipt.LockDigest, 24),
		Mode:          receipt.Mode,
		Coverage:      receipt.Coverage,
		OverallStatus: receipt.OverallStatus,
		Notes: []string{
			"Private names and absolute paths are redacted.",
			"This is not a signed attestation.",
			"Do not upload automatically; review before sharing.",
		},
	}
	for _, finding := range receipt.Findings {
		capsule.Findings = append(capsule.Findings, redactFinding(finding))
	}
	for _, prop := range receipt.Properties {
		prop.ArtifactID = redactPath(prop.ArtifactID)
		if prop.Property == "source_digest" {
			prop.Value = nil
		}
		capsule.Properties = append(capsule.Properties, prop)
	}
	return capsule
}

// WriteSupportCapsule writes the capsule JSON to destination.
func WriteSupportCapsule(destination string, capsule SupportCapsule) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(capsule, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(destination, append(data, '\n'), 0o644)
}

func prefixDigest(value string, n int) string {
	value = strings.TrimPrefix(value, "sha256:")
	if value == "" {
		return ""
	}
	if len(value) > n {
		value = value[:n]
	}
	return "sha256:" + value + "…"
}

func redactFinding(finding Finding) Finding {
	finding.Source = redactPath(finding.Source)
	finding.Message = redactPath(finding.Message)
	finding.Remedy = redactPath(finding.Remedy)
	return finding
}

func redactPath(value string) string {
	if value == "" {
		return value
	}
	// Drop absolute home-like prefixes; keep the leaf identity.
	if strings.HasPrefix(value, "/") || strings.Contains(value, ":\\") || strings.HasPrefix(value, "~") {
		return "<path>/" + filepath.Base(value)
	}
	if strings.Count(value, "/") >= 3 && (strings.HasPrefix(value, "github.com/") || strings.Contains(value, "/home/") || strings.Contains(value, "/Users/")) {
		return fmt.Sprintf("<module>/…/%s", filepath.Base(value))
	}
	return value
}
