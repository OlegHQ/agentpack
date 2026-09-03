package sync

import (
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/manifest"
)

func TestResolveRemoveSpecSupportsShorthandAndBlobURL(t *testing.T) {
	t.Parallel()
	module := "github.com/anthropics/plugins/plugins/code-simplifier"
	project := manifestWithKeys(module)
	for _, spec := range []string{
		"anthropics/plugins/plugins/code-simplifier",
		"https://github.com/anthropics/plugins/blob/main/plugins/code-simplifier/agents/code.md",
		"code-simplifier",
	} {
		got, err := ResolveRemoveSpec(t.TempDir(), spec, project)
		if err != nil || got != module {
			t.Fatalf("ResolveRemoveSpec(%q) = %q, %v", spec, got, err)
		}
	}
}

func TestResolveRemoveSpecReportsDeterministicAmbiguity(t *testing.T) {
	t.Parallel()
	project := manifestWithKeys("github.com/z/repo/demo", "github.com/a/repo/demo")
	_, err := ResolveRemoveSpec(t.TempDir(), "demo", project)
	if err == nil || !strings.Contains(err.Error(), "github.com/a/repo/demo, github.com/z/repo/demo") {
		t.Fatalf("error = %v", err)
	}
}

func manifestWithKeys(keys ...string) *manifest.Manifest {
	dependencies := make(map[string]manifest.Dependency, len(keys))
	for _, key := range keys {
		dependencies[key] = shortDependency("")
	}
	return &manifest.Manifest{Dependencies: dependencies}
}

func shortDependency(value string) manifest.Dependency { return manifest.Dependency{Short: &value} }
