package claude

import "testing"

func TestParseSkillInventory(t *testing.T) {
	t.Parallel()
	text := "agentpack-bundle 0.0.1\n\nComponent inventory\n  Skills (1)  probe-sentinel\n  Agents (0)\n"
	got := parseSkillInventory(text)
	if len(got) != 1 || got[0] != "probe-sentinel" {
		t.Fatalf("got %#v", got)
	}
	if parseSkillInventory("Skills (0)\n") != nil {
		t.Fatal("expected empty")
	}
}

func TestCapabilityMentionsValidatedVersion(t *testing.T) {
	t.Parallel()
	caps := Capability()
	if caps["validated_versions"] == "" || caps["skill_source_digest"] == "" {
		t.Fatalf("incomplete capability record: %#v", caps)
	}
}
