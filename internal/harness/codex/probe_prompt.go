package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/paths"
)

// probePromptSkills copies static inputs only: native auth/history/hooks never enter diagnostics.
func probePromptSkills(ctx context.Context, exe, sourceHome, workspace string) (base.ProbeResult, []promptSkill, error) {
	root, err := paths.ProjectStateFile(workspace, "probe-diagnostics")
	if err != nil {
		return base.ProbeResult{}, nil, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return base.ProbeResult{}, nil, err
	}
	diagnostic, err := os.MkdirTemp(root, "codex-")
	if err != nil {
		return base.ProbeResult{}, nil, err
	}
	defer os.RemoveAll(diagnostic)
	home := filepath.Join(diagnostic, "home")
	if err = os.MkdirAll(home, 0700); err != nil {
		return base.ProbeResult{}, nil, err
	}
	if sourceHome != "" {
		sourceSkills := filepath.Join(sourceHome, "skills")
		if info, statErr := os.Lstat(sourceSkills); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return base.ProbeResult{}, nil, fmt.Errorf("static skills root is a symlink; controlled probe refuses host-linked inputs")
			}
			if err = os.CopyFS(filepath.Join(home, "skills"), os.DirFS(sourceSkills)); err != nil {
				return base.ProbeResult{}, nil, fmt.Errorf("copy static skills without symlinks: %w", err)
			}
		} else if !os.IsNotExist(statErr) {
			return base.ProbeResult{}, nil, statErr
		}
	}
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\ncloud_skill_enabled = false\n"), 0600); err != nil {
		return base.ProbeResult{}, nil, err
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + diagnostic, "CODEX_HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(diagnostic, "config"), "XDG_DATA_HOME=" + filepath.Join(diagnostic, "data"), "XDG_CACHE_HOME=" + filepath.Join(diagnostic, "cache")}
	if os.Getenv("SystemRoot") != "" {
		env = append(env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	// Override private-state paths and cloud discovery; debug builds inputs without model inference.
	argv := []string{"--no-daemon", "-c", `cli_auth_credentials_store="file"`, "-c", "cloud_skill_enabled=false", "-c", "sqlite_home=" + fmt.Sprintf("%q", home), "debug", "prompt-input"}
	result, err := base.RunProbe(ctx, base.ProbePlan{Executable: exe, Argv: argv, Cwd: workspace, Env: env, Timeout: 30 * time.Second, Categories: []string{"skill"}, Network: "not_measured", WriteScope: "managed_diagnostic_storage; native_other_effects_unmeasured"})
	if err != nil {
		return result, nil, err
	}
	entries, err := parsePromptEntries(result.Stdout)
	if err != nil {
		return result, nil, err
	}
	// Classify while the diagnostic home still exists: its skills root holds the staged copies.
	managed, _ := filepath.EvalSymlinks(filepath.Join(home, "skills"))
	for i := range entries {
		if root, rootErr := filepath.EvalSymlinks(entries[i].Root); rootErr == nil {
			entries[i].Managed = managed != "" && root == managed
		}
	}
	return result, entries, nil
}

// promptSkill is one entry of the native prompt's skill inventory.
type promptSkill struct {
	Name    string
	Root    string
	Managed bool
}

var promptRootLine = regexp.MustCompile("(?m)^- `(r[0-9]+)` = `([^`\\n]+)`$")

var promptSkillLine = regexp.MustCompile(`(?m)^- ([A-Za-z0-9_.-]+): [^\n]*\(file: (r[0-9]+)/[^\n]+/SKILL\.md\)$`)

// parsePromptSkills returns the distinct skill names of the inventory.
func parsePromptSkills(output string) ([]string, error) {
	entries, err := parsePromptEntries(output)
	if err != nil {
		return nil, err
	}
	var names []string
	seen := map[string]bool{}
	for _, entry := range entries {
		if !seen[entry.Name] {
			seen[entry.Name] = true
			names = append(names, entry.Name)
		}
	}
	return names, nil
}

// parsePromptEntries keeps every inventory entry: the native prompt can list one name several
// times when the checkout and the home both provide it.
func parsePromptEntries(output string) ([]promptSkill, error) {
	var messages []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(output), &messages); err != nil || messages == nil {
		return nil, fmt.Errorf("native prompt input JSON is malformed")
	}
	var inventory string
	for _, message := range messages {
		if message.Type != "message" || message.Role != "developer" {
			continue
		}
		for _, content := range message.Content {
			if content.Type != "input_text" || !strings.HasPrefix(content.Text, "<skills_instructions>\n") {
				continue
			}
			if inventory != "" {
				return nil, fmt.Errorf("multiple native skills instruction inventories")
			}
			inventory = content.Text
		}
	}
	if inventory == "" || !strings.HasSuffix(inventory, "</skills_instructions>") || !strings.Contains(inventory, "### Available skills\n") {
		return nil, fmt.Errorf("native skills instruction inventory is incomplete")
	}
	roots := map[string]string{}
	for _, match := range promptRootLine.FindAllStringSubmatch(inventory, -1) {
		roots[match[1]] = match[2]
	}
	var skills []promptSkill
	for _, match := range promptSkillLine.FindAllStringSubmatch(inventory, -1) {
		skills = append(skills, promptSkill{Name: match[1], Root: roots[match[2]]})
	}
	if len(skills) == 0 {
		return nil, fmt.Errorf("native positive skill discovery is unavailable; absence is not confirmed")
	}
	return skills, nil
}

func observePromptSkills(ctx context.Context, exe, sourceHome, workspace string, receipt *environment.Receipt) {
	result, skills, err := probePromptSkills(ctx, exe, sourceHome, workspace)
	evidencePath, saveErr := base.SaveProbeEvidence(workspace, receipt.ReceiptID, "prompt-input", result)
	if saveErr != nil {
		receipt.OverallStatus = "error"
		receipt.Termination.Status = "failed"
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: "PROBE_FAILED", Severity: environment.SeverityError, Target: "codex", Message: saveErr.Error(), Evidence: environment.EvidenceUnknown})
		return
	}
	receipt.DiagnosticRefs = append(receipt.DiagnosticRefs, evidencePath)
	receipt.Termination.NativeExitCode = result.ExitCode
	if err != nil {
		receipt.Termination.Status = "failed"
		receipt.OverallStatus = "unknown"
		code := "OBSERVATION_PARTIAL"
		if result.ExitCode != 0 || result.TimedOut || result.Canceled || result.Truncated {
			receipt.OverallStatus = "error"
			code = "PROBE_FAILED"
		}
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: code, Severity: environment.SeverityWarning, Target: "codex", Message: err.Error(), Evidence: environment.EvidenceUnknown, Remedy: "verify static diagnostic inputs and the validated native CLI version"})
		return
	}
	receipt.Coverage = []environment.CoverageNote{{Category: "skill", Scope: "controlled_projection", Completeness: "partial", Reason: "positive names in native prompt input from copied static skills only; excludes user config, auth/history/hooks and cloud skills; no absence or model use proof"}}
	count, managed := map[string]int{}, map[string]bool{}
	var names []string
	for _, skill := range skills {
		if count[skill.Name] == 0 {
			names = append(names, skill.Name)
		}
		count[skill.Name]++
		managed[skill.Name] = managed[skill.Name] || skill.Managed
	}
	receipt.Coverage = append(receipt.Coverage, environment.CoverageNote{Category: "skill", Scope: "workspace_session", Completeness: "partial", Reason: "prompt input built in the checkout with the staged skills and an empty user home; user-level and cloud skills excluded"})
	for _, name := range names {
		id := filepath.Join("skills", name)
		receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "controlled_projection", Category: "skill", ArtifactID: id, Property: "presence", Value: true, Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: evidencePath})
		if !managed[name] {
			continue
		}
		winner := "package"
		if count[name] > 1 {
			winner = "ambiguous"
		}
		receipt.Properties = append(receipt.Properties,
			environment.ObservedProperty{Scope: "workspace_session", Category: "skill", ArtifactID: id, Property: "presence", Value: true, Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: evidencePath},
			environment.ObservedProperty{Scope: "workspace_session", Category: "skill", ArtifactID: id, Property: "winner", Value: winner, Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: evidencePath})
	}
	receipt.OverallStatus = "ready_with_warnings"
}
