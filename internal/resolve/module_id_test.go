package resolve

import "testing"

func TestParseModuleIDCanonicalizesCoordinatesAndPreservesPathCase(t *testing.T) {
	t.Parallel()
	module, err := ParseModuleID("github.com/Anthropics/Skills/skills/PDF-Tools")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(module), "github.com/anthropics/skills/skills/PDF-Tools"; got != want {
		t.Fatalf("ParseModuleID() = %q, want %q", got, want)
	}
	owner, repo, path := module.OwnerRepoPath()
	if owner != "anthropics" || repo != "skills" || path != "skills/PDF-Tools" {
		t.Fatalf("OwnerRepoPath() = %q, %q, %q", owner, repo, path)
	}
}

func TestParseModuleIDToleratesLegacyQuotes(t *testing.T) {
	t.Parallel()
	module, err := ParseModuleID("\"github.com/o/r/p\"")
	if err != nil || module != "github.com/o/r/p" {
		t.Fatalf("ParseModuleID() = %q, %v", module, err)
	}
}

func TestSplitModuleAtRef(t *testing.T) {
	t.Parallel()
	module, ref, ok := SplitModuleAtRef("github.com/o/r/p@v1.2.3")
	if !ok || module != "github.com/o/r/p" || ref != "v1.2.3" {
		t.Fatalf("SplitModuleAtRef() = %q, %q, %v", module, ref, ok)
	}
	module, ref, ok = SplitModuleAtRef("github.com/o/r")
	if ok || module != "github.com/o/r" || ref != "" {
		t.Fatalf("SplitModuleAtRef(no ref) = %q, %q, %v", module, ref, ok)
	}
}
