package opencode

import "testing"

func TestParseSkillList(t *testing.T) {
	skills, err := parseSkillList(`[{"name":"review","location":"/staged/skills/review/SKILL.md","content":"x"},{"name":"built-in","location":"<built-in>"}]`)
	if err != nil || len(skills) != 2 || skills[0].Location != "/staged/skills/review/SKILL.md" {
		t.Fatalf("skills=%+v err=%v", skills, err)
	}
	for _, output := range []string{"", "null", "log line\n[]x", `[{"name":"a","location":"/x"},{"name":"a","location":"/y"}]`, `[{"name":"a"}]`} {
		if _, err := parseSkillList(output); err == nil {
			t.Fatalf("accepted %q", output)
		}
	}
}

func TestWinnerNeedsEveryRunToAgree(t *testing.T) {
	cases := []struct {
		counts map[string]int
		want   string
	}{
		{map[string]int{"package": sampleRuns}, "package"},
		{map[string]int{"ambient": sampleRuns}, "ambient"},
		{map[string]int{"package": sampleRuns - 1, "ambient": 1}, "unstable"},
		{map[string]int{"package": sampleRuns - 1}, "unstable"},
		{map[string]int{}, ""},
	}
	for _, c := range cases {
		if got := winnerOf(c.counts); got != c.want {
			t.Fatalf("%v: got %q, want %q", c.counts, got, c.want)
		}
	}
}

func TestCapabilityNamesValidatedVersion(t *testing.T) {
	if caps := Capability(); caps["validated_versions"] == "" || caps["capability_revision"] == "" {
		t.Fatalf("incomplete capability record: %#v", caps)
	}
}
