package opencode

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

const (
	capabilityRevision = "opencode-probe-v1"
	adapterRevision    = "opencode-observe-2026-10"
	validatedVersion   = "1.17.11 and later 1.17 releases"
	// OpenCode 1.17.11 lists one skill per name, and with two same-named skills the listed one
	// differs between runs in a few percent of them. One listing cannot show that, so the probe
	// samples. 60 runs miss a 4% effect with probability 0.96^60, about 9%.
	sampleRuns = 60
)

// Capability describes validated OpenCode observation support for this revision.
func Capability() map[string]string {
	return map[string]string{
		"adapter_revision":    adapterRevision,
		"capability_revision": capabilityRevision,
		"validated_versions":  validatedVersion,
		"skill_presence":      fmt.Sprintf("observed via %d runs of `opencode debug skill --pure` with the staged OPENCODE_CONFIG_DIR, the checkout as cwd and an empty user home", sampleRuns),
		"skill_winner":        "observed in scope workspace_session from the file location OpenCode reports: package when every run lists the staged file, ambient when every run lists another file, unstable when runs disagree",
		"effects":             "empty HOME and XDG directories; --pure disables external plugins; no model inference requested; native network/other effects unmeasured",
	}
}

type listedSkill struct {
	Name     string `json:"name"`
	Location string `json:"location"`
}

// ProbeConfigDir samples which file OpenCode resolves for each packaged skill name in a session
// started in the checkout. expected names the packaged skills of the plan.
func ProbeConfigDir(ctx context.Context, configDir, workspace, sourceID, lockDigest, mode string, expected ...string) (environment.Receipt, error) {
	exe, err := base.LookPathEnv("OPENCODE_PATH", "opencode")
	if err != nil {
		return environment.Receipt{}, err
	}
	identity, err := environment.ExecutableIdentity(exe)
	if err != nil {
		return environment.Receipt{}, err
	}
	receipt := environment.Receipt{
		SchemaVersion:  1,
		Phase:          "probe",
		ReceiptID:      fmt.Sprintf("opencode-%d", time.Now().UTC().UnixNano()),
		SourceIdentity: sourceID,
		LockDigest:     lockDigest,
		Mode:           mode,
		Target:         environment.ReceiptTarget{Adapter: "opencode", AdapterRevision: adapterRevision, ExecutableIdentity: identity, CapabilityRevision: capabilityRevision},
		CreatedAt:      time.Now().UTC(),
		Effects:        environment.ProbeEffects{ModelCalls: false, Network: "not_measured", WriteScope: "managed_diagnostic_home; native_other_effects_unmeasured"},
		Termination:    environment.Termination{Status: "completed"},
	}
	stop := func(status, code, message string) (environment.Receipt, error) {
		receipt.OverallStatus = status
		if status == "error" {
			receipt.Termination.Status = "failed"
		}
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: code, Severity: environment.SeverityWarning, Target: "opencode", Message: message, Evidence: environment.EvidenceUnknown, Remedy: "install the validated OpenCode version or set OPENCODE_PATH"})
		return receipt, nil
	}
	root, err := paths.ProjectStateFile(workspace, "probe-diagnostics")
	if err != nil {
		return receipt, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return receipt, err
	}
	diagnostic, err := os.MkdirTemp(root, "opencode-")
	if err != nil {
		return receipt, err
	}
	defer os.RemoveAll(diagnostic)
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + diagnostic, "OPENCODE_CONFIG_DIR=" + configDir, "XDG_CONFIG_HOME=" + filepath.Join(diagnostic, "config"), "XDG_DATA_HOME=" + filepath.Join(diagnostic, "data"), "XDG_CACHE_HOME=" + filepath.Join(diagnostic, "cache"), "XDG_STATE_HOME=" + filepath.Join(diagnostic, "state")}
	if os.Getenv("SystemRoot") != "" {
		env = append(env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	versionResult, versionErr := base.RunProbe(ctx, base.ProbePlan{Executable: exe, Argv: []string{"--version"}, Env: env, Timeout: 20 * time.Second, Categories: []string{"runtime"}})
	versionEvidence, saveErr := base.SaveProbeEvidence(workspace, receipt.ReceiptID, "version", versionResult)
	if saveErr != nil {
		return receipt, saveErr
	}
	receipt.DiagnosticRefs = append(receipt.DiagnosticRefs, versionEvidence)
	receipt.Termination.NativeExitCode = versionResult.ExitCode
	version := strings.TrimSpace(versionResult.Stdout)
	if versionErr != nil || !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		return stop("error", "PROBE_FAILED", "OpenCode version probe failed or returned an unexpected format")
	}
	receipt.Target.NativeVersion = version
	receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "workspace_session", Category: "runtime", Property: "native_version", Value: version, Evidence: environment.EvidenceObserved, Method: "opencode --version", EvidenceRef: versionEvidence})
	if !base.SeriesFrom(version, "1.17", 11) {
		receipt.Coverage = []environment.CoverageNote{{Category: "skill", Scope: "workspace_session", Completeness: "none", Reason: "native version is outside validated parser coverage"}}
		return stop("unknown", "OBSERVATION_UNAVAILABLE", "OpenCode native version is not validated: "+version)
	}

	staged, _ := filepath.EvalSymlinks(configDir)
	// tally[name] counts runs by where the listed file lives.
	tally := map[string]map[string]int{}
	var summary strings.Builder
	started := time.Now()
	for run := 0; run < sampleRuns; run++ {
		result, runErr := base.RunProbe(ctx, base.ProbePlan{Executable: exe, Argv: []string{"debug", "skill", "--pure"}, Cwd: workspace, Env: env, Timeout: 60 * time.Second, Categories: []string{"skill"}, Network: "not_measured", WriteScope: "managed_diagnostic_storage; native_other_effects_unmeasured"})
		receipt.Termination.NativeExitCode = result.ExitCode
		if runErr != nil {
			return stop("error", "PROBE_FAILED", runErr.Error())
		}
		skills, parseErr := parseSkillList(result.Stdout)
		if parseErr != nil {
			return stop("unknown", "OBSERVATION_PARTIAL", parseErr.Error())
		}
		for _, skill := range skills {
			holder := "ambient"
			if location, locErr := filepath.EvalSymlinks(skill.Location); locErr == nil && staged != "" && strings.HasPrefix(location, staged+string(filepath.Separator)) {
				holder = "package"
			}
			if tally[skill.Name] == nil {
				tally[skill.Name] = map[string]int{}
			}
			tally[skill.Name][holder]++
			fmt.Fprintf(&summary, "%d\t%s\t%s\n", run, skill.Name, skill.Location)
		}
	}
	evidence, saveErr := base.SaveProbeEvidence(workspace, receipt.ReceiptID, "debug-skill-runs", base.ProbeResult{Stdout: summary.String(), Duration: time.Since(started)})
	if saveErr != nil {
		return receipt, saveErr
	}
	receipt.DiagnosticRefs = append(receipt.DiagnosticRefs, evidence)
	receipt.Coverage = []environment.CoverageNote{{Category: "skill", Scope: "workspace_session", Completeness: "partial", Reason: fmt.Sprintf("%d listings in the checkout with the staged configuration and an empty user home; user-level skills excluded; a rarer instability can be missed", sampleRuns)}}
	reported := map[string]bool{}
	for _, name := range expected {
		// A project .agents skill and a packaged skill can share a name; report the name once.
		if reported[name] {
			continue
		}
		reported[name] = true
		id := filepath.Join("skills", name)
		counts := tally[name]
		winner := winnerOf(counts)
		receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "workspace_session", Category: "skill", ArtifactID: id, Property: "presence", Value: counts["package"] > 0, Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: evidence})
		if winner != "" {
			receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "workspace_session", Category: "skill", ArtifactID: id, Property: "winner", Value: winner, Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: evidence})
		}
	}
	receipt.OverallStatus = "ready_with_warnings"
	return receipt, nil
}

// winnerOf names who held a skill name across the sampled runs.
func winnerOf(counts map[string]int) string {
	switch {
	case counts["package"] > 0 && counts["ambient"] > 0:
		return "unstable"
	case counts["package"] == sampleRuns:
		return "package"
	case counts["ambient"] == sampleRuns:
		return "ambient"
	case counts["package"]+counts["ambient"] > 0:
		// Listed in some runs and missing in others.
		return "unstable"
	}
	return ""
}

// parseSkillList reads the JSON array printed by `opencode debug skill`; any other output is rejected.
func parseSkillList(output string) ([]listedSkill, error) {
	var skills []listedSkill
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &skills); err != nil || skills == nil {
		return nil, fmt.Errorf("native skill list is missing or malformed")
	}
	seen := map[string]bool{}
	for _, skill := range skills {
		if skill.Name == "" || skill.Location == "" || seen[skill.Name] {
			return nil, fmt.Errorf("native skill list has an empty or duplicate entry")
		}
		seen[skill.Name] = true
	}
	return skills, nil
}
