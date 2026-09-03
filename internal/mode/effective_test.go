package mode

import "testing"

func TestEffectiveSpecificPathOverridesDisabledPackage(t *testing.T) {
	t.Parallel()
	effective, err := NewEffective("test", Definition{Base: BaseAll, Enable: []string{"package-path:github.com/acme/repo:hooks/hooks.json"}, Disable: []string{"package:github.com/acme/repo"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := effective.AllowsPackagePath("github.com/acme/repo", "hooks/hooks.json"); err != nil || !allowed {
		t.Fatalf("hook = %v, %v", allowed, err)
	}
	if allowed, err := effective.AllowsPackagePath("github.com/acme/repo", "agents/foo.md"); err != nil || allowed {
		t.Fatalf("agent = %v, %v", allowed, err)
	}
}

func TestEffectiveBaseNoneAndDisableTie(t *testing.T) {
	t.Parallel()
	effective, err := NewEffective("test", Definition{Base: BaseNone, Enable: []string{"mcp:filesystem", ".agents:rules"}, Disable: []string{".agents:rules"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !effective.AllowsMCP("filesystem") || effective.AllowsMCP("github") {
		t.Fatal("MCP decision mismatch")
	}
	if allowed, _ := effective.AllowsDotAgentsPath("rules/a.mdc"); allowed {
		t.Fatal("disable must win equal-specificity tie")
	}
}
