package environment

import "testing"

func TestCompareReceiptsMarksUnknownNotEqual(t *testing.T) {
	t.Parallel()
	left := Receipt{
		Termination: Termination{Status: "completed"}, ReceiptID: "a", Target: ReceiptTarget{Adapter: "claude", CapabilityRevision: "v1", NativeVersion: "2.1.295"},
		Properties: []ObservedProperty{{ArtifactID: "s", Property: "source_digest", Evidence: EvidenceUnknown}},
	}
	right := Receipt{
		Termination: Termination{Status: "completed"}, ReceiptID: "b", Target: ReceiptTarget{Adapter: "claude", CapabilityRevision: "v1", NativeVersion: "2.1.295"},
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
		Receipt{Termination: Termination{Status: "completed"}, ReceiptID: "a", Target: ReceiptTarget{Adapter: "claude"}},
		Receipt{Termination: Termination{Status: "completed"}, ReceiptID: "b", Target: ReceiptTarget{Adapter: "codex"}},
	)
	if got.Compatible {
		t.Fatal("expected incompatible")
	}
}

func TestCompareReceiptsSeparatesScopeCategoryAndJSONTypes(t *testing.T) {
	left := currentReceipt()
	right := currentReceipt()
	left.Target.NativeVersion = "1.0.0"
	right.Target.NativeVersion = "1.0.0"
	left.Properties = []ObservedProperty{{Category: "skill", Scope: "controlled_projection", ArtifactID: "review", Property: "presence", Value: true, Evidence: EvidenceObserved}}
	right.Properties = []ObservedProperty{{Category: "skill", Scope: "native_catalog", ArtifactID: "review", Property: "presence", Value: true, Evidence: EvidenceObserved}}
	if got := CompareReceipts(left, right); got.Compatible || len(got.Unknowns) == 0 {
		t.Fatalf("different scopes compared as equal: %#v", got)
	}
	right.Properties[0].Scope = "controlled_projection"
	right.Properties[0].Category = "plugin"
	if got := CompareReceipts(left, right); got.Compatible {
		t.Fatalf("different categories compatible: %#v", got)
	}
	right.Properties[0].Category = "skill"
	right.Properties[0].Value = "true"
	if got := CompareReceipts(left, right); !got.Compatible || len(got.Deltas) != 1 {
		t.Fatalf("JSON boolean and string compared as equal: %#v", got)
	}
}
