package environment

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func currentReceipt() Receipt {
	return Receipt{SchemaVersion: 1, Phase: "probe", ReceiptID: "r", CreatedAt: time.Now().UTC(), SourceIdentity: "source", LockDigest: "lock", GenerationID: "generation", WorkspaceDigest: "workspace", PolicyDigest: "policy", Mode: "default", Target: ReceiptTarget{Adapter: "claude", AdapterRevision: "adapter", CapabilityRevision: "capability", ExecutableIdentity: "exe"}, Termination: Termination{Status: "completed"}, OverallStatus: "ready"}
}

func TestReceiptValidationRejectsIncompleteAndUnknownEvidence(t *testing.T) {
	if err := ValidateReceipt(Receipt{SchemaVersion: 1}); err == nil {
		t.Fatal("accepted empty identities")
	}
	receipt := currentReceipt()
	if err := ValidateReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Properties = []ObservedProperty{{Property: "presence", Value: true, Evidence: EvidenceObserved}}
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("accepted observation without method")
	}
	receipt = currentReceipt()
	receipt.Termination.Status = "failed"
	receipt.OverallStatus = "error"
	if err := ValidateReceipt(receipt); err != nil {
		t.Fatalf("diagnostic failure should remain readable: %v", err)
	}
}

func TestExecutableIdentityTracksBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native")
	if err := os.WriteFile(path, []byte("version-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := ExecutableIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("version-b"), 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := ExecutableIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("binary change did not invalidate identity")
	}
}

func TestReceiptIDsCannotEscapeManagedStorage(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	receipt := currentReceipt()
	receipt.ReceiptID = "../../escape"
	if _, err := SaveReceipt(t.TempDir(), receipt); err == nil {
		t.Fatal("accepted traversal receipt id")
	}
}

func TestReceiptsArePrivateAndNeverFollowDestinationSymlinks(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	workspace := t.TempDir()
	receipt := currentReceipt()
	root, err := ReceiptsRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	path, err := SaveReceipt(workspace, receipt)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, check := range []struct {
			path string
			mode os.FileMode
		}{{root, 0700}, {path, 0600}} {
			info, err := os.Stat(check.path)
			if err != nil || info.Mode().Perm() != check.mode {
				t.Fatalf("private receipt permissions: path=%s info=%v err=%v", check.path, info, err)
			}
		}
	}
	target := filepath.Join(t.TempDir(), "untouched")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := SaveReceipt(workspace, receipt); err == nil {
		t.Fatal("followed receipt destination symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "keep" {
		t.Fatal("symlink target changed")
	}
}

func TestReceiptRejectsConflictingDuplicateObservations(t *testing.T) {
	receipt := currentReceipt()
	receipt.Properties = []ObservedProperty{{Category: "skill", ArtifactID: "review", Property: "presence", Value: true, Evidence: EvidenceObserved, Method: "native_catalog"}, {Category: "skill", ArtifactID: "review", Property: "presence", Value: false, Evidence: EvidenceObserved, Method: "native_catalog"}}
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("conflicting observed properties accepted")
	}
	receipt.Properties = nil
	receipt.Coverage = []CoverageNote{{Category: "skill", Scope: "native_catalog", Completeness: "complete"}, {Category: "skill", Scope: "native_catalog", Completeness: "partial"}}
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("conflicting coverage accepted")
	}
}
