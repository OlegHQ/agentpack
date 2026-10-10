package environment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
		ReceiptID:     "<receipt>",
		Adapter:       receipt.Target.Adapter,
		NativeVersion: publicVersion(receipt.Target.NativeVersion),
		SourceID:      prefixDigest(receipt.SourceIdentity, 16),
		LockDigest:    prefixDigest(receipt.LockDigest, 24),
		Mode:          "<mode>",
		OverallStatus: receipt.OverallStatus,
		Notes: []string{
			"Private names and absolute paths are redacted.",
			"This is not a signed attestation.",
			"Do not upload automatically; review before sharing.",
		},
	}
	for _, note := range receipt.Coverage {
		note.Reason = ""
		capsule.Coverage = append(capsule.Coverage, note)
	}
	for _, finding := range receipt.Findings {
		capsule.Findings = append(capsule.Findings, redactFinding(finding))
	}
	for _, prop := range receipt.Properties {
		prop.ArtifactID = redactPath(prop.ArtifactID)
		prop.Reason, prop.EvidenceRef, prop.EvidenceDigest = "", "", ""
		if prop.Method != "" {
			prop.Method = "<method>"
		}
		switch value := prop.Value.(type) {
		case bool:
			if prop.Property != "present" && prop.Property != "enabled" && prop.Property != "loaded" {
				prop.Value = nil
			}
		case string:
			if prop.Property == "native_version" {
				prop.Value = publicVersion(value)
			} else {
				prop.Value = nil
			}
		default:
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
	return os.WriteFile(destination, append(data, '\n'), 0o600)
}

func prefixDigest(value string, n int) string {
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) != 64 {
		return ""
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return ""
		}
	}
	if value == "" {
		return ""
	}
	if len(value) > n {
		value = value[:n]
	}
	return "sha256:" + value + "…"
}

// Free-form native diagnostics can contain arbitrary secrets, not just recognizable paths.
func redactFinding(finding Finding) Finding {
	finding.Source = redactPath(finding.Source)
	finding.Winner = redactPath(finding.Winner)
	finding.Message = "Diagnostic text omitted; consult the local receipt."
	finding.Remedy = ""
	return finding
}

func redactPath(value string) string {
	if value == "" {
		return ""
	}
	return "<private>"
}

var publicVersionPattern = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9 _.-]* )?(v?[0-9]+\.[0-9]+\.[0-9]+)$`)

func publicVersion(value string) string {
	match := publicVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) == 2 {
		return match[1]
	}
	return ""
}
