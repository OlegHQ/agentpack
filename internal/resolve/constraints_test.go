package resolve

import (
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/manifest"
)

type staticTagLister []Tag

func (tags staticTagLister) ListTags(string, string, bool) ([]Tag, error) { return tags, nil }

func TestConstraintsFromRefClassifiesCommitSemverAndTag(t *testing.T) {
	t.Parallel()
	commit := strings.Repeat("A", 40)
	constraints, err := ConstraintsFromRef(commit, true)
	if err != nil || constraints.Exact != strings.ToLower(commit) {
		t.Fatalf("commit = %+v, %v", constraints, err)
	}
	constraints, err = ConstraintsFromRef("^1.2", true)
	if err != nil || len(constraints.Semver) != 1 {
		t.Fatalf("semver = %+v, %v", constraints, err)
	}
	constraints, err = ConstraintsFromRef("v1.2.3", true)
	if err != nil || constraints.Tag != "v1.2.3" {
		t.Fatalf("tag = %+v, %v", constraints, err)
	}
}

func TestBareVersionUsesCaretSemantics(t *testing.T) {
	t.Parallel()
	constraints, err := ConstraintsFromRef("1.2.3", true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := constraints.PickGitRef(staticTagLister{{Name: "v2.0.0"}, {Name: "v1.9.0"}, {Name: "v1.2.3"}}, "o", "r", false)
	if err != nil || got != "v1.9.0" {
		t.Fatalf("PickGitRef() = %q, %v", got, err)
	}
}

func TestMergedSemverRequirementsAllApply(t *testing.T) {
	t.Parallel()
	left, _ := ConstraintsFromRef(">=1.2.0", true)
	right, _ := ConstraintsFromRef("<2.0.0", true)
	if err := left.Merge(right); err != nil {
		t.Fatal(err)
	}
	got, err := left.PickGitRef(staticTagLister{{Name: "v2.1.0"}, {Name: "v1.8.0"}, {Name: "v1.1.0"}}, "o", "r", false)
	if err != nil || got != "v1.8.0" {
		t.Fatalf("PickGitRef() = %q, %v", got, err)
	}
}

func TestMergeRejectsConflictingPins(t *testing.T) {
	t.Parallel()
	left := ModuleConstraints{Branch: "main"}
	if err := left.Merge(ModuleConstraints{Branch: "dev"}); err == nil || !strings.Contains(err.Error(), "conflicting branches") {
		t.Fatalf("Merge() error = %v", err)
	}
}

func TestDependencyTableAllowsOnePin(t *testing.T) {
	t.Parallel()
	commit, tag := "abc", "v1"
	_, err := ConstraintsFromTable(manifest.DependencyTable{Commit: &commit, Tag: &tag}, "", false)
	if err == nil {
		t.Fatal("ConstraintsFromTable() accepted multiple pins")
	}
	path := "../local"
	_, err = ConstraintsFromTable(manifest.DependencyTable{Path: &path}, "", false)
	if err == nil {
		t.Fatal("ConstraintsFromTable() accepted path dependency")
	}
}
