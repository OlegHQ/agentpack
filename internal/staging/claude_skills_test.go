package staging

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSkill(t *testing.T, root, name, contents string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReconcileClaudeSkillsCopiesMissingSkillBothWays(t *testing.T) {
	project := t.TempDir()
	agentsSkills := filepath.Join(project, ".agents", "skills")
	claudeSkills := filepath.Join(project, ".claude", "skills")
	writeSkill(t, agentsSkills, "only-agents", "---\nname: only-agents\ndescription: agents only\n---\n")
	writeSkill(t, claudeSkills, "only-claude", "---\nname: only-claude\ndescription: claude only\n---\n")

	report, err := ReconcileClaudeSkills(project, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.CopiedToClaude) != 1 || report.CopiedToClaude[0] != "only-agents" {
		t.Fatalf("CopiedToClaude = %v", report.CopiedToClaude)
	}
	if len(report.CopiedToAgents) != 1 || report.CopiedToAgents[0] != "only-claude" {
		t.Fatalf("CopiedToAgents = %v", report.CopiedToAgents)
	}
	if _, err := os.Stat(filepath.Join(claudeSkills, "only-agents", "SKILL.md")); err != nil {
		t.Fatalf("missing copy into .claude/skills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agentsSkills, "only-claude", "SKILL.md")); err != nil {
		t.Fatalf("missing copy into .agents/skills: %v", err)
	}
}

func TestReconcileClaudeSkillsPrefersNewerContentOnConflict(t *testing.T) {
	project := t.TempDir()
	agentsSkills := filepath.Join(project, ".agents", "skills")
	claudeSkills := filepath.Join(project, ".claude", "skills")
	agentsPath := writeSkill(t, agentsSkills, "shared", "old contents")
	claudePath := writeSkill(t, claudeSkills, "shared", "new contents")

	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	if err := os.Chtimes(agentsPath, older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(claudePath, newer, newer); err != nil {
		t.Fatal(err)
	}

	report, err := ReconcileClaudeSkills(project, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Updated) != 1 || report.Updated[0].Winner != "claude" {
		t.Fatalf("Updated = %+v", report.Updated)
	}
	data, err := os.ReadFile(filepath.Join(agentsSkills, "shared", "SKILL.md"))
	if err != nil || string(data) != "new contents" {
		t.Fatalf("agents SKILL.md = %s, err=%v", data, err)
	}
}

func TestReconcileClaudeSkillsIdenticalSkillsReportInSync(t *testing.T) {
	project := t.TempDir()
	agentsSkills := filepath.Join(project, ".agents", "skills")
	claudeSkills := filepath.Join(project, ".claude", "skills")
	writeSkill(t, agentsSkills, "same", "identical")
	writeSkill(t, claudeSkills, "same", "identical")

	report, err := ReconcileClaudeSkills(project, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.InSync) != 1 || report.InSync[0] != "same" {
		t.Fatalf("InSync = %v", report.InSync)
	}
	if len(report.CopiedToClaude) != 0 || len(report.CopiedToAgents) != 0 || len(report.Updated) != 0 {
		t.Fatalf("unexpected changes: %+v", report)
	}
}

func TestReconcileClaudeSkillsDryRunMakesNoChanges(t *testing.T) {
	project := t.TempDir()
	agentsSkills := filepath.Join(project, ".agents", "skills")
	claudeSkills := filepath.Join(project, ".claude", "skills")
	writeSkill(t, agentsSkills, "only-agents", "content")

	report, err := ReconcileClaudeSkills(project, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.CopiedToClaude) != 1 {
		t.Fatalf("CopiedToClaude = %v", report.CopiedToClaude)
	}
	if _, err := os.Stat(filepath.Join(claudeSkills, "only-agents", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write files, err=%v", err)
	}
}
