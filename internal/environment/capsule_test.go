package environment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportSupportCapsuleRedactsPaths(t *testing.T) {
	t.Parallel()
	receipt := Receipt{
		DiagnosticRefs: []string{"/private/diagnostics/SENTINEL-PRIVATE"}, SchemaVersion: 1, ReceiptID: "r1", SourceIdentity: strings.Repeat("a", 64),
		LockDigest: "sha256:" + strings.Repeat("b", 64),
		Target:     ReceiptTarget{Adapter: "claude", NativeVersion: "2.1.295"},
		Findings: []Finding{{
			Code: "CONFIG_SHADOWED", Message: "see /home/secret/user/.claude/skills/helper",
			Source: "/home/secret/user/.claude/skills/helper",
		}},
		Properties: []ObservedProperty{{
			ArtifactID: "/tmp/private/skills/review", Property: "source_digest",
			EvidenceDigest: "sha256:SENTINEL-DIAGNOSTIC-DIGEST", Value: "sha256:should-not-leak", Evidence: EvidenceUnknown,
		}},
	}
	capsule := ExportSupportCapsule(receipt)
	path := filepath.Join(t.TempDir(), "capsule.json")
	if err := WriteSupportCapsule(path, capsule); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, leak := range []string{"SENTINEL-PRIVATE", "SENTINEL-DIAGNOSTIC-DIGEST", "/home/secret", "should-not-leak", strings.Repeat("a", 40)} {
		if strings.Contains(text, leak) {
			t.Fatalf("capsule leaked %q in %s", leak, text)
		}
	}
	if !strings.Contains(text, `<private>`) && !strings.Contains(text, `\u003cprivate\u003e`) {
		t.Fatalf("expected redacted path, got %s", text)
	}
}
