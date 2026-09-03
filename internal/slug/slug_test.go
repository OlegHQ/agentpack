package slug

import "testing"

func TestDashedLower(t *testing.T) {
	t.Parallel()
	if got, want := DashedLower("  Design/UI  "), "design-ui"; got != want {
		t.Fatalf("DashedLower() = %q, want %q", got, want)
	}
}

func TestDashedDoesNotCollapseOrTrim(t *testing.T) {
	t.Parallel()
	if got, want := Dashed(" a::B "), "-a--B-"; got != want {
		t.Fatalf("Dashed() = %q, want %q", got, want)
	}
}
