package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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

func TestProbeRejectsNativeNonzeroAndEmptyVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic Unix executable fixture")
	}
	for _, fixture := range []string{"#!/bin/sh\nprintf 'codex-cli 0.159.3\\n'\nexit 7\n", "#!/bin/sh\nexit 0\n"} {
		exe := filepath.Join(t.TempDir(), "codex-fixture")
		if err := os.WriteFile(exe, []byte(fixture), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CODEX_PATH", exe)
		receipt, err := ProbeHome(context.Background(), t.TempDir(), t.TempDir(), "source", "lock", "default")
		if err != nil {
			t.Fatal(err)
		}
		if receipt.OverallStatus != "error" || len(receipt.Properties) != 0 || receipt.Termination.Status != "failed" {
			t.Fatalf("false native success: %+v", receipt)
		}
	}
}

func TestPromptSkillsParserRequiresNativeDeveloperInventory(t *testing.T) {
	valid := `[{"type":"message","role":"developer","content":[{"type":"input_text","text":"<skills_instructions>\n### Available skills\n- sentinel: benign (file: r0/sentinel/SKILL.md)\n</skills_instructions>"}]}]`
	names, err := parsePromptSkills(valid)
	if err != nil || len(names) != 1 || names[0] != "sentinel" {
		t.Fatalf("names=%v err=%v", names, err)
	}
	for _, output := range []string{"null", "[]", `[{"type":"message","role":"user","content":[{"type":"input_text","text":"<skills_instructions>\n### Available skills\n- sentinel: benign (file: r0/sentinel/SKILL.md)\n</skills_instructions>"}]}]`, strings.Replace(valid, "</skills_instructions>", "", 1)} {
		if _, err := parsePromptSkills(output); err == nil {
			t.Fatalf("accepted incomplete output %s", output)
		}
	}
}

func TestNativeCodexSentinel(t *testing.T) {
	if os.Getenv("AGENTPACK_NATIVE_PROBES") != "1" {
		t.Skip("explicit opt-in controlled native metadata test")
	}
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	source := t.TempDir()
	workspace := t.TempDir()
	skill := filepath.Join(source, "skills", "agentpack-positive-sentinel")
	if err := os.MkdirAll(skill, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: agentpack-positive-sentinel\ndescription: Benign discovery sentinel.\n---\nDo nothing.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	receipt, err := ProbeHome(context.Background(), source, workspace, "fixture", "fixture-lock", "default")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, property := range receipt.Properties {
		if property.ArtifactID == filepath.Join("skills", "agentpack-positive-sentinel") && property.Property == "presence" && property.Value == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("known-positive native sentinel missing: %+v", receipt)
	}
	if err := os.RemoveAll(skill); err != nil {
		t.Fatal(err)
	}
	receipt, err = ProbeHome(context.Background(), source, workspace, "fixture", "fixture-lock", "default")
	if err != nil {
		t.Fatal(err)
	}
	for _, property := range receipt.Properties {
		if property.ArtifactID == filepath.Join("skills", "agentpack-positive-sentinel") {
			t.Fatal("removed sentinel remains observed")
		}
	}
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 0 {
		t.Fatalf("native workspace changed: %v %v", entries, err)
	}
	t.Logf("native version=%s, positive+removed sentinel checked; effects=%+v", receipt.Target.NativeVersion, receipt.Effects)
}

func TestDiagnosticRejectsSymlinkInputsBeforeNativeExecution(t *testing.T) {
	t.Setenv("AGENTPACK_HOME", t.TempDir())
	source := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(source, "skills")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, _, err := probePromptSkills(context.Background(), "nonexistent-native-executable", source, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("host-linked input not refused before execution: %v", err)
	}
}
