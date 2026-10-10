package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseSkillInventory(t *testing.T) {
	t.Parallel()
	text := "agentpack-bundle 0.0.1\n\nComponent inventory\n  Skills (1)  probe-sentinel\n  Agents (0)\n"
	got, err := parseSkillInventory(text)
	if err != nil || len(got) != 1 || got[0] != "probe-sentinel" {
		t.Fatalf("got %#v", got)
	}
	if got, err := parseSkillInventory("Skills (0)\n"); err != nil || len(got) != 0 {
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

func TestSkillInventoryFailsClosed(t *testing.T) {
	for _, output := range []string{"", "Skills (1)\n", "Skills (0) invented\n", "Skills (2) a a\n", "Skills (1) ../escape\n", "Skills (0)\nSkills (0)\n"} {
		if _, err := parseSkillInventory(output); err == nil {
			t.Fatalf("accepted %q", output)
		}
	}
}

func TestProbeRejectsUnexpectedNativeCatalog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic Unix executable fixture")
	}
	exe := filepath.Join(t.TempDir(), "claude-fixture")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '2.1.295 (Claude Code)\\n'; else printf '[{}]\\n'; fi\n"
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_PATH", exe)
	receipt, err := ProbeBundle(context.Background(), t.TempDir(), t.TempDir(), "source", "lock", "default")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.OverallStatus != "unknown" || len(receipt.Properties) != 0 {
		t.Fatalf("malformed catalog became observation: %+v", receipt)
	}
}

func TestNativeClaudeSentinel(t *testing.T) {
	if os.Getenv("AGENTPACK_NATIVE_PROBES") != "1" {
		t.Skip("explicit opt-in controlled native metadata test")
	}
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	bundle := t.TempDir()
	workspace := t.TempDir()
	manifest := filepath.Join(bundle, ".claude-plugin")
	skill := filepath.Join(bundle, "skills", "agentpack-positive-sentinel")
	for _, dir := range []string{manifest, skill} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(manifest, "plugin.json"), []byte(`{"name":"agentpack-bundle","version":"1.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: agentpack-positive-sentinel\ndescription: Benign discovery sentinel.\n---\nDo nothing.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	receipt, err := ProbeBundle(context.Background(), bundle, workspace, "fixture", "fixture-lock", "default")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, property := range receipt.Properties {
		if property.ArtifactID == filepath.Join("agentpack-bundle", "skills", "agentpack-positive-sentinel") && property.Property == "presence" && property.Value == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("known-positive native sentinel missing: %+v", receipt)
	}
	if err := os.RemoveAll(skill); err != nil {
		t.Fatal(err)
	}
	receipt, err = ProbeBundle(context.Background(), bundle, workspace, "fixture", "fixture-lock", "default")
	if err != nil {
		t.Fatal(err)
	}
	for _, property := range receipt.Properties {
		if property.ArtifactID == filepath.Join("agentpack-bundle", "skills", "agentpack-positive-sentinel") {
			t.Fatal("removed sentinel remains observed")
		}
	}
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 0 {
		t.Fatalf("native workspace changed: %v %v", entries, err)
	}
	t.Logf("native version=%s, positive+removed sentinel checked; effects=%+v", receipt.Target.NativeVersion, receipt.Effects)
}
