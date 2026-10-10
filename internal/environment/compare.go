package environment

import (
	"fmt"
	"strings"
)

// Comparison is a structured delta between two receipts.
type Comparison struct {
	Compatible bool     `json:"compatible"`
	LeftID     string   `json:"left_id"`
	RightID    string   `json:"right_id"`
	Deltas     []string `json:"deltas"`
	Unknowns   []string `json:"unknowns,omitempty"`
}

// CompareReceipts diffs compatible receipt categories. Unobserved fields stay unknown.
func CompareReceipts(left, right Receipt) Comparison {
	out := Comparison{
		Compatible: true,
		LeftID:     left.ReceiptID,
		RightID:    right.ReceiptID,
	}
	if left.Target.Adapter != "" && right.Target.Adapter != "" && left.Target.Adapter != right.Target.Adapter {
		out.Compatible = false
		out.Deltas = append(out.Deltas, fmt.Sprintf("adapter: %s vs %s", left.Target.Adapter, right.Target.Adapter))
		return out
	}
	if left.Target.CapabilityRevision != "" && right.Target.CapabilityRevision != "" &&
		left.Target.CapabilityRevision != right.Target.CapabilityRevision {
		out.Compatible = false
		out.Deltas = append(out.Deltas, fmt.Sprintf("capability_revision: %s vs %s", left.Target.CapabilityRevision, right.Target.CapabilityRevision))
		return out
	}
	compareField := func(name, a, b string) {
		if a == "" || b == "" {
			if a != b {
				out.Unknowns = append(out.Unknowns, name+" unobserved on one side")
			}
			return
		}
		if a != b {
			out.Deltas = append(out.Deltas, fmt.Sprintf("%s: %s vs %s", name, a, b))
		}
	}
	compareField("source_id", left.SourceIdentity, right.SourceIdentity)
	compareField("lock_digest", left.LockDigest, right.LockDigest)
	compareField("mode", left.Mode, right.Mode)
	compareField("native_version", left.Target.NativeVersion, right.Target.NativeVersion)
	leftProps := propertyMap(left.Properties)
	rightProps := propertyMap(right.Properties)
	for key, lv := range leftProps {
		rv, ok := rightProps[key]
		if !ok {
			out.Unknowns = append(out.Unknowns, key+" missing on right")
			continue
		}
		if lv.Evidence == EvidenceUnknown || rv.Evidence == EvidenceUnknown {
			out.Unknowns = append(out.Unknowns, key+" unobserved")
			continue
		}
		if fmt.Sprint(lv.Value) != fmt.Sprint(rv.Value) {
			out.Deltas = append(out.Deltas, fmt.Sprintf("%s: %v vs %v", key, lv.Value, rv.Value))
		}
	}
	for key := range rightProps {
		if _, ok := leftProps[key]; !ok {
			out.Unknowns = append(out.Unknowns, key+" missing on left")
		}
	}
	return out
}

func propertyMap(props []ObservedProperty) map[string]ObservedProperty {
	out := make(map[string]ObservedProperty, len(props))
	for _, prop := range props {
		key := strings.TrimSpace(prop.ArtifactID) + "|" + prop.Property
		out[key] = prop
	}
	return out
}
