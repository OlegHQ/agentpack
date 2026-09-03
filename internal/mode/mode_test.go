package mode

import (
	"slices"
	"strings"
	"testing"
)

func TestDefinitionSortAndDeduplicate(t *testing.T) {
	t.Parallel()
	definition := Definition{Enable: []string{"z", "a", "a"}, Disable: []string{"b", "b"}}
	definition.SortAndDeduplicate()
	if !slices.Equal(definition.Enable, []string{"a", "z"}) || !slices.Equal(definition.Disable, []string{"b"}) {
		t.Fatalf("unexpected definition: %+v", definition)
	}
}

func TestValidateName(t *testing.T) {
	t.Parallel()
	if got, err := ValidateName(" design "); err != nil || got != "design" {
		t.Fatalf("ValidateName() = %q, %v", got, err)
	}
	if _, err := ValidateName("design review"); err == nil {
		t.Fatal("ValidateName() accepted whitespace")
	}
}

func TestParseAndMatchPackagePathSelector(t *testing.T) {
	t.Parallel()
	selector, err := ParseSelector("package-path:github.com/acme/repo:hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := selector.CanonicalString(), "package-path:github.com/acme/repo:hooks/hooks.json"; got != want {
		t.Fatalf("CanonicalString() = %q, want %q", got, want)
	}
	specificity, matched, err := selector.MatchesPackagePath("github.com/acme/repo", "hooks/hooks.json/child")
	if err != nil || !matched || specificity != 12 {
		t.Fatalf("MatchesPackagePath() = %d, %v, %v", specificity, matched, err)
	}
}

func TestPackageSelectorAllowsEmptyPathQuery(t *testing.T) {
	t.Parallel()
	selector, err := ParseSelector("package:github.com/acme/repo")
	if err != nil {
		t.Fatal(err)
	}
	_, matched, err := selector.MatchesPackagePath("github.com/acme/repo", "")
	if err != nil || !matched {
		t.Fatalf("MatchesPackagePath() = %v, %v", matched, err)
	}
}

func TestDotAgentsSelectorRejectsParentTraversal(t *testing.T) {
	t.Parallel()
	_, err := ParseSelector(".agents:../secret")
	if err == nil || !strings.Contains(err.Error(), "parent traversal") {
		t.Fatalf("ParseSelector() error = %v", err)
	}
}
