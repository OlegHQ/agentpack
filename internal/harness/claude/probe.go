package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/OlegHQ/agentpack/internal/environment"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/paths"
)

const (
	capabilityRevision = "claude-probe-v4"
	adapterRevision    = "claude-observe-2026-10"
)

// Capability describes validated Claude observation support for this revision.
func Capability() map[string]string {
	return map[string]string{
		"adapter_revision":    adapterRevision,
		"capability_revision": capabilityRevision,
		"validated_versions":  "2.1.295, 2.1.296",
		"plugin_presence":     "observed via `claude --bare --plugin-dir <bundle> plugin list --json`",
		"skill_presence":      "observed via `claude plugin details <id>` inventory (names only; no source bytes)",
		"skill_source_digest": "unknown — native interface does not attribute source bytes",
		"command_agent":       "2.1.296: slash commands and subagents are observed the same way as skills, from the same init event",
		"skill_winner":        "2.1.296: observed in scope workspace_session from the session init catalog (checkout as cwd, empty user home); package when only the namespaced bundle copy carries the name, ambient when another skill holds the bare name",
		"ambient_user_skills": "unknown — probes run with an empty user home",
		"effects":             "isolated HOME and CLAUDE_CONFIG_DIR; no model inference requested; native network/other effects unmeasured",
	}
}

// ProbeBundle runs a bounded Claude plugin/skill catalog probe against a staged bundle.
// packaged names the artifacts of the plan, so one that staging dropped is still reported.
func ProbeBundle(ctx context.Context, bundleDir, workspace, sourceID, lockDigest, mode string, packaged ...base.PackagedNames) (environment.Receipt, error) {
	var plan base.PackagedNames
	if len(packaged) > 0 {
		plan = packaged[0]
	}
	expected := plan.Skills
	exe, err := base.LookPathEnv("CLAUDE_CODE_PATH", "claude")
	if err != nil {
		return environment.Receipt{}, err
	}
	version, versionResult, versionErr := nativeVersion(ctx, exe)
	identity, identityErr := environment.ExecutableIdentity(exe)
	if identityErr != nil {
		return environment.Receipt{}, identityErr
	}
	receipt := environment.Receipt{
		SchemaVersion:  1,
		Phase:          "probe",
		ReceiptID:      fmt.Sprintf("claude-%d", time.Now().UTC().UnixNano()),
		SourceIdentity: sourceID,
		LockDigest:     lockDigest,
		Mode:           mode,
		Target: environment.ReceiptTarget{
			Adapter:            "claude",
			AdapterRevision:    adapterRevision,
			ExecutableIdentity: identity,
			NativeVersion:      version,
			CapabilityRevision: capabilityRevision,
		},
		CreatedAt: time.Now().UTC(),
		Effects: environment.ProbeEffects{
			ModelCalls: false,
			Network:    "not_measured",
			WriteScope: "managed_diagnostic_home; native_other_effects_unmeasured",
		},
		Termination: environment.Termination{Status: "completed", NativeExitCode: versionResult.ExitCode},
	}
	versionEvidence, saveErr := base.SaveProbeEvidence(workspace, receipt.ReceiptID, "version", versionResult)
	if saveErr != nil {
		return receipt, saveErr
	}
	receipt.DiagnosticRefs = append(receipt.DiagnosticRefs, versionEvidence)

	if versionErr != nil {
		err = versionErr
	}
	if err != nil {
		receipt.Termination.Status = "failed"
		receipt.Findings = append(receipt.Findings, environment.Finding{
			Code: "PROBE_FAILED", Severity: environment.SeverityError, Target: "claude",
			Message: err.Error(), Evidence: environment.EvidenceUnknown,
			Remedy: "install claude or set CLAUDE_CODE_PATH; re-run agentpack probe --agent claude",
		})
		receipt.OverallStatus = "error"
		return receipt, nil
	}

	if version != "2.1.295 (Claude Code)" && version != "2.1.295" && version != "2.1.296 (Claude Code)" && version != "2.1.296" {
		receipt.Coverage = []environment.CoverageNote{{Category: "plugin", Scope: "controlled_projection", Completeness: "none", Reason: "native version is outside validated parser coverage"}}
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: "OBSERVATION_UNAVAILABLE", Severity: environment.SeverityWarning, Target: "claude", Message: "Claude native version is not validated: " + version, Evidence: environment.EvidenceUnknown, Remedy: "use the documented validated native version or validate and update parser fixtures"})
		receipt.OverallStatus = "unknown"
		return receipt, nil
	}

	diagnosticRoot, err := paths.ProjectStateFile(workspace, "probe-diagnostics")
	if err != nil {
		return receipt, err
	}
	if err = os.MkdirAll(diagnosticRoot, 0700); err != nil {
		return receipt, err
	}
	diagnostic, err := os.MkdirTemp(diagnosticRoot, "claude-")
	if err != nil {
		return receipt, err
	}
	defer os.RemoveAll(diagnostic)
	diagnosticEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + diagnostic, "CLAUDE_CONFIG_DIR=" + filepath.Join(diagnostic, "claude"), "XDG_CONFIG_HOME=" + filepath.Join(diagnostic, "config"), "XDG_DATA_HOME=" + filepath.Join(diagnostic, "data"), "XDG_CACHE_HOME=" + filepath.Join(diagnostic, "cache")}
	if os.Getenv("SystemRoot") != "" {
		diagnosticEnv = append(diagnosticEnv, "SystemRoot="+os.Getenv("SystemRoot"))
	}

	listPlan := base.ProbePlan{
		Executable: exe,
		Argv:       []string{"--bare", "--plugin-dir", bundleDir, "plugin", "list", "--json"},
		Cwd:        workspace,
		Env:        diagnosticEnv,
		Timeout:    45 * time.Second,
		Categories: []string{"plugin"},
		Network:    "none_required",
		WriteScope: "managed_diagnostic_storage",
		ModelCalls: false,
	}
	listResult, err := base.RunProbe(ctx, listPlan)
	listEvidence, saveErr := base.SaveProbeEvidence(workspace, receipt.ReceiptID, "plugin-list", listResult)
	if saveErr != nil {
		return receipt, saveErr
	}
	receipt.DiagnosticRefs = append(receipt.DiagnosticRefs, listEvidence)
	receipt.Termination.NativeExitCode = listResult.ExitCode
	if err != nil {
		receipt.Termination.Status = "failed"
		receipt.OverallStatus = "error"
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: "PROBE_FAILED", Severity: environment.SeverityError, Target: "claude", Message: err.Error(), Evidence: environment.EvidenceUnknown, Remedy: "verify native CLI and staged plugin; rerun explicit probe"})
		return receipt, nil
	}

	var plugins []pluginEntry
	if err := json.Unmarshal([]byte(listResult.Stdout), &plugins); err != nil || plugins == nil || invalidPluginEntries(plugins) {
		receipt.Coverage = []environment.CoverageNote{{
			Category: "plugin", Scope: "controlled_projection", Completeness: "partial",
			Reason: "plugin list JSON could not be parsed",
		}}
		receipt.Findings = append(receipt.Findings, environment.Finding{
			Code: "OBSERVATION_PARTIAL", Severity: environment.SeverityError, Target: "claude",
			Message: "native plugin inventory schema is invalid or incomplete", Evidence: environment.EvidenceUnknown,
		})
		receipt.OverallStatus = "unknown"
		return receipt, nil
	}

	receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "controlled_projection", Category: "plugin", Property: "catalog_complete", Value: true, Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: listEvidence})
	pluginPresent := false
	pluginID := ""
	for _, plugin := range plugins {
		if plugin.ID == "agentpack-bundle" || strings.HasPrefix(plugin.ID, "agentpack-bundle@") {
			pluginPresent = true
			pluginID = plugin.ID
			break
		}
	}
	receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "controlled_projection",
		Category: "plugin", ArtifactID: "agentpack-bundle", Property: "presence", Value: pluginPresent,
		Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: listEvidence,
	})
	if !pluginPresent {
		receipt.Coverage = []environment.CoverageNote{{
			Category: "plugin", Scope: "controlled_projection", Completeness: "complete",
			Reason: "plugin list returned successfully; agentpack-bundle not present",
		}, {
			Category: "skill", Scope: "controlled_projection", Completeness: "none",
			Reason: "skill inventory requires a discovered plugin id",
		}}
		receipt.Findings = append(receipt.Findings, environment.Finding{
			Code: "OBSERVATION_UNAVAILABLE", Severity: environment.SeverityWarning, Target: "claude",
			Message:  "agentpack-bundle was not listed by the native plugin catalog",
			Evidence: environment.EvidenceObserved,
			Remedy:   "run agentpack sync then probe again with the staged Claude bundle",
		})
		if sessionValidated(version) {
			// Staging can drop every packaged skill that a project skill replaces; the session still shows who holds the names.
			plan.Skills = expected
			observeSession(ctx, exe, bundleDir, workspace, diagnosticEnv, plan, &receipt)
		}
		receipt.OverallStatus = "ready_with_warnings"
		return receipt, nil
	}

	detailsPlan := base.ProbePlan{
		Executable: exe,
		Argv:       []string{"--bare", "--plugin-dir", bundleDir, "plugin", "details", pluginID},
		Cwd:        workspace,
		Env:        diagnosticEnv,
		Timeout:    45 * time.Second,
		Categories: []string{"skill"},
		Network:    "none_required",
		WriteScope: "managed_diagnostic_storage",
	}
	details, err := base.RunProbe(ctx, detailsPlan)
	detailsEvidence, saveErr := base.SaveProbeEvidence(workspace, receipt.ReceiptID, "plugin-details", details)
	if saveErr != nil {
		return receipt, saveErr
	}
	receipt.DiagnosticRefs = append(receipt.DiagnosticRefs, detailsEvidence)
	if err != nil || details.ExitCode != 0 {
		receipt.Termination.Status = "failed"
		receipt.Termination.NativeExitCode = details.ExitCode
		receipt.Coverage = append(receipt.Coverage, environment.CoverageNote{
			Category: "skill", Scope: "controlled_projection", Completeness: "partial",
			Reason: "plugin details failed; skill inventory incomplete",
		})
		receipt.Findings = append(receipt.Findings, environment.Finding{
			Code: "OBSERVATION_PARTIAL", Severity: environment.SeverityWarning, Target: "claude",
			Message:  "plugin present but skill inventory unavailable",
			Evidence: environment.EvidenceUnknown,
		})
		receipt.OverallStatus = "unknown"
		return receipt, nil
	}
	receipt.Termination.NativeExitCode = details.ExitCode
	skills, parseErr := parseSkillInventory(details.Stdout)
	if parseErr != nil {
		receipt.Coverage = []environment.CoverageNote{{Category: "skill", Scope: "controlled_projection", Completeness: "partial", Reason: parseErr.Error()}}
		if !sessionValidated(version) {
			receipt.Findings = append(receipt.Findings, environment.Finding{Code: "OBSERVATION_PARTIAL", Severity: environment.SeverityError, Target: "claude", Message: parseErr.Error(), Evidence: environment.EvidenceUnknown})
			receipt.OverallStatus = "unknown"
			return receipt, nil
		}
		// A very large bundle abbreviates its inventory line. The plugin listing is then
		// unusable, but the session catalog still names every skill.
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: "OBSERVATION_PARTIAL", Severity: environment.SeverityWarning, Target: "claude", Message: "plugin inventory unreadable (" + parseErr.Error() + "); using the session catalog only", Evidence: environment.EvidenceUnknown})
		plan.Skills = expected
		observeSession(ctx, exe, bundleDir, workspace, diagnosticEnv, plan, &receipt)
		receipt.OverallStatus = "ready_with_warnings"
		return receipt, nil
	}
	receipt.Coverage = []environment.CoverageNote{{
		Category: "plugin", Scope: "controlled_projection", Completeness: "complete",
	}, {
		Category: "skill", Scope: "controlled_projection", Completeness: "partial",
		Reason: "names observed in isolated plugin inventory; ambient user plugins and skills excluded; source bytes/digests not attributed by Claude",
	}}
	for _, skill := range skills {
		receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "controlled_projection",
			Category: "skill", ArtifactID: filepath.Join("agentpack-bundle", "skills", skill),
			Property: "presence", Value: true,
			Evidence: environment.EvidenceObserved, Method: "native_catalog", EvidenceRef: detailsEvidence,
		})
		receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "controlled_projection",
			Category: "skill", ArtifactID: filepath.Join("agentpack-bundle", "skills", skill),
			Property: "source_digest", Value: nil,
			Evidence: environment.EvidenceUnknown, Reason: "Native interface does not attribute source bytes.",
		})
	}
	if sessionValidated(version) {
		plan.Skills = append([]string{}, skills...)
		for _, name := range expected {
			if !slices.Contains(plan.Skills, name) {
				plan.Skills = append(plan.Skills, name)
			}
		}
		observeSession(ctx, exe, bundleDir, workspace, diagnosticEnv, plan, &receipt)
	} else {
		receipt.Coverage = append(receipt.Coverage, environment.CoverageNote{Category: "skill", Scope: "workspace_session", Completeness: "none", Reason: "session catalog parser is validated for 2.1.296 only"})
	}
	receipt.OverallStatus = "ready_with_warnings"
	return receipt, nil
}

func sessionValidated(version string) bool {
	return version == "2.1.296 (Claude Code)" || version == "2.1.296"
}

// observeSession starts a print-mode session in the checkout with the staged bundle and an empty
// user home, and reads the listings of its init event. Without credentials the session ends
// before any model request. A packaged skill, command or subagent is listed as
// agentpack-bundle:<name>; anything else holding the bare <name> is what a user or the model
// reaches under that name.
func observeSession(ctx context.Context, exe, bundleDir, workspace string, env []string, packaged base.PackagedNames, receipt *environment.Receipt) {
	unavailable := func(reason string) {
		receipt.Coverage = append(receipt.Coverage, environment.CoverageNote{Category: "skill", Scope: "workspace_session", Completeness: "none", Reason: reason})
		receipt.Findings = append(receipt.Findings, environment.Finding{Code: "OBSERVATION_PARTIAL", Severity: environment.SeverityWarning, Target: "claude", Message: "session catalog unavailable: " + reason, Evidence: environment.EvidenceUnknown})
	}
	result, _ := base.RunProbe(ctx, base.ProbePlan{
		Executable: exe,
		Argv:       []string{"-p", "agentpack session catalog probe", "--output-format", "stream-json", "--verbose", "--plugin-dir", bundleDir, "--strict-mcp-config", "--settings", `{"disableAllHooks":true}`},
		Cwd:        workspace,
		Env:        env,
		Timeout:    45 * time.Second,
		Categories: []string{"skill"},
		Network:    "not_measured",
		WriteScope: "managed_diagnostic_storage; native_other_effects_unmeasured",
	})
	evidence, saveErr := base.SaveProbeEvidence(workspace, receipt.ReceiptID, "session-init", result)
	if saveErr != nil {
		unavailable(saveErr.Error())
		return
	}
	receipt.DiagnosticRefs = append(receipt.DiagnosticRefs, evidence)
	if result.TimedOut || result.Canceled || result.Truncated {
		unavailable("session did not complete within bounds")
		return
	}
	init, err := parseSessionInit(result.Stdout)
	if err != nil {
		unavailable(err.Error())
		return
	}
	if init.credentials {
		// The diagnostic home is empty, so this means the environment supplied credentials.
		receipt.Effects.ModelCalls = true
	}
	for _, kind := range []struct {
		category, directory string
		listing, names      []string
	}{
		{"skill", "skills", init.skills, packaged.Skills},
		{"command", "commands", init.commands, packaged.Commands},
		{"agent", "agents", init.agents, packaged.Agents},
	} {
		if len(kind.names) == 0 {
			continue
		}
		if kind.listing == nil {
			receipt.Coverage = append(receipt.Coverage, environment.CoverageNote{Category: kind.category, Scope: "workspace_session", Completeness: "none", Reason: "the session init event carries no " + kind.category + " listing"})
			continue
		}
		listed := map[string]bool{}
		for _, name := range kind.listing {
			listed[name] = true
		}
		receipt.Coverage = append(receipt.Coverage, environment.CoverageNote{Category: kind.category, Scope: "workspace_session", Completeness: "partial", Reason: "session started in the checkout with the staged bundle and an empty user home; user-level configuration excluded"})
		for _, name := range kind.names {
			id := filepath.Join("agentpack-bundle", kind.directory, name)
			bundled := listed["agentpack-bundle:"+name]
			receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "workspace_session", Category: kind.category, ArtifactID: id, Property: "presence", Value: bundled, Evidence: environment.EvidenceObserved, Method: "session_init", EvidenceRef: evidence})
			winner := "package"
			switch {
			case listed[name]:
				winner = "ambient"
			case !bundled:
				continue
			}
			receipt.Properties = append(receipt.Properties, environment.ObservedProperty{Scope: "workspace_session", Category: kind.category, ArtifactID: id, Property: "winner", Value: winner, Evidence: environment.EvidenceObserved, Method: "session_init", EvidenceRef: evidence})
		}
	}
}

// sessionInit is the part of the first stream-json event that the probe uses.
type sessionInit struct {
	skills, commands, agents []string
	credentials              bool
}

// parseSessionInit returns the listings of the first stream-json event and whether the session
// had credentials. Anything but a well-formed init event is rejected.
func parseSessionInit(output string) (sessionInit, error) {
	line, _, _ := strings.Cut(output, "\n")
	var event struct {
		Type         string   `json:"type"`
		Subtype      string   `json:"subtype"`
		Skills       []string `json:"skills"`
		Commands     []string `json:"slash_commands"`
		Agents       []string `json:"agents"`
		APIKeySource string   `json:"apiKeySource"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil || event.Type != "system" || event.Subtype != "init" || event.Skills == nil {
		return sessionInit{}, fmt.Errorf("native session init event is missing or malformed")
	}
	for _, listing := range [][]string{event.Skills, event.Commands, event.Agents} {
		seen := map[string]bool{}
		for _, name := range listing {
			if name == "" || seen[name] {
				return sessionInit{}, fmt.Errorf("native session listing has an empty or duplicate name")
			}
			seen[name] = true
		}
	}
	return sessionInit{skills: event.Skills, commands: event.Commands, agents: event.Agents, credentials: event.APIKeySource != "" && event.APIKeySource != "none"}, nil
}

func nativeVersion(ctx context.Context, exe string) (string, base.ProbeResult, error) {
	result, err := base.RunProbe(ctx, base.ProbePlan{
		Executable: exe, Argv: []string{"--version"}, Timeout: 10 * time.Second,
	})
	if err != nil || result.ExitCode != 0 {
		return "", result, fmt.Errorf("Claude version probe failed: %w", err)
	}
	version := strings.TrimSpace(result.Stdout)
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?: \(Claude Code\))?$`).MatchString(version) {
		return "", result, fmt.Errorf("Claude version probe returned an unexpected version format")
	}
	return version, result, nil
}

// Horizontal whitespace only: an empty inventory must not swallow the next line.
var skillLine = regexp.MustCompile(`(?m)^[ \t]*Skills[ \t]*\((\d+)\)[ \t]*(.*)$`)

type pluginEntry struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

func invalidPluginEntries(entries []pluginEntry) bool {
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.ID == "" || seen[entry.ID] {
			return true
		}
		seen[entry.ID] = true
	}
	return false
}

func parseSkillInventory(text string) ([]string, error) {
	matches := skillLine.FindAllStringSubmatch(text, -1)
	if len(matches) != 1 {
		return nil, fmt.Errorf("native skill inventory must contain exactly one Skills count")
	}
	count, err := strconv.Atoi(matches[0][1])
	if err != nil {
		return nil, fmt.Errorf("invalid native skill inventory count")
	}
	names := strings.Fields(strings.ReplaceAll(matches[0][2], ",", " "))
	if len(names) != count {
		return nil, fmt.Errorf("native skill inventory count does not match names")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
			return nil, fmt.Errorf("invalid native skill inventory name %q", name)
		}
		// Claude Code lists a package's commands among its skills, so a
		// command and a skill that the package publishes under one name
		// appear twice. The inventory cannot tell them apart; say which name.
		if seen[name] {
			return nil, fmt.Errorf("native skill inventory lists %q twice (a command and a skill of the same name)", name)
		}
		seen[name] = true
	}
	return names, nil
}
