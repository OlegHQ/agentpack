package codex

import "testing"

func TestCapabilityRecordsSkillUnknown(t *testing.T) {
	t.Parallel()
	caps := Capability()
	if caps["skill_presence"] == "" || caps["validated_versions"] == "" {
		t.Fatalf("incomplete capability record: %#v", caps)
	}
}

func TestWithCodexHomeOverrides(t *testing.T) {
	t.Parallel()
	got := withCodexHome([]string{"PATH=/bin", "CODEX_HOME=/old"}, "/new")
	found := false
	for _, entry := range got {
		if entry == "CODEX_HOME=/new" {
			found = true
		}
		if entry == "CODEX_HOME=/old" {
			t.Fatal("old CODEX_HOME retained")
		}
	}
	if !found {
		t.Fatalf("got %#v", got)
	}
}
