package environment

import "testing"

func TestCompareReceiptsMarksUnknownNotEqual(t *testing.T) {
	t.Parallel()
	left := Receipt{
		ReceiptID: "a", Target: ReceiptTarget{Adapter: "claude", CapabilityRevision: "v1"},
		Properties: []ObservedProperty{{ArtifactID: "s", Property: "source_digest", Evidence: EvidenceUnknown}},
	}
	right := Receipt{
		ReceiptID: "b", Target: ReceiptTarget{Adapter: "claude", CapabilityRevision: "v1"},
		Properties: []ObservedProperty{{ArtifactID: "s", Property: "source_digest", Evidence: EvidenceUnknown}},
	}
	got := CompareReceipts(left, right)
	if !got.Compatible || len(got.Deltas) != 0 || len(got.Unknowns) == 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestCompareReceiptsIncompatibleAdapters(t *testing.T) {
	t.Parallel()
	got := CompareReceipts(
		Receipt{ReceiptID: "a", Target: ReceiptTarget{Adapter: "claude"}},
		Receipt{ReceiptID: "b", Target: ReceiptTarget{Adapter: "codex"}},
	)
	if got.Compatible {
		t.Fatal("expected incompatible")
	}
}
