package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/OlegHQ/agentpack/internal/harness"
)

type SupportKind string

const (
	Native      SupportKind = "native"
	Emulated    SupportKind = "emulated"
	Degraded    SupportKind = "degraded"
	Unsupported SupportKind = "unsupported"
)

type Support struct {
	Kind   SupportKind
	Reason string
}

type RenderedFile struct {
	Path string
	JSON any
	Text string
}

type Diagnostic struct {
	Level, Source, Message string
}

type RenderSummary struct{ Native, Emulated, Degraded, Omitted int }

type RenderOutput struct {
	Files       []RenderedFile
	Diagnostics []Diagnostic
	Summary     RenderSummary
}

type RenderContext struct {
	ProjectRoot    string
	TargetRoot     string
	StagedPackages map[string]string
}

type Renderer interface {
	Target() harness.Target
	Render(Bundle, RenderContext) (RenderOutput, error)
}

type ExecutionSpec struct {
	Target     harness.Target `json:"target"`
	Event      Event          `json:"event"`
	Handler    Handler        `json:"handler"`
	WorkingDir string         `json:"working_dir"`
	Matcher    string         `json:"matcher,omitempty"`
}

func BuildExecutionSpec(target harness.Target, hook Hook, event Event, context RenderContext, output *RenderOutput) (string, error) {
	workingDirectory, found := context.StagedPackages[hook.Origin.PackageKey]
	if !found {
		return "", fmt.Errorf("missing staged hook package root for %s (%s)", hook.Origin.Module, hook.Origin.PackageKey)
	}
	path := SpecPath(target, context.TargetRoot, hook)
	output.Files = append(output.Files, RenderedFile{Path: path, JSON: ExecutionSpec{Target: target, Event: event, Handler: hook.Handler, WorkingDir: workingDirectory, Matcher: hook.Matcher}})
	return path, nil
}

func SpecPath(target harness.Target, root string, hook Hook) string {
	return filepath.Join(HookAssetRoot(target, root), hook.Origin.PackageKey, "specs", fmt.Sprintf("%03d-%03d-%03d.json", hook.Origin.EventIndex, hook.Origin.MatcherGroupIndex, hook.Origin.HookIndex))
}

func HookAssetRoot(target harness.Target, root string) string {
	if target == harness.OpenCode {
		return filepath.Join(root, "plugins", "agentpack-hooks", "assets")
	}
	return filepath.Join(root, "hooks", "_packages")
}

func HookExecCommand(kind HandlerKind, target harness.Target, specPath string) string {
	return fmt.Sprintf("agentpack hook-exec %s --target %s --spec %s", kind, target, shellQuote(specPath))
}

func HookDispatchCommand(target harness.Target, event Event, specsRoot string) string {
	return fmt.Sprintf("agentpack hook-exec dispatch --target %s --event %s --specs-dir %s", target, event, shellQuote(specsRoot))
}

func HandlerObject(hook Hook, includeExtra bool) map[string]any {
	object := map[string]any{"type": string(hook.Handler.Kind)}
	switch hook.Handler.Kind {
	case CommandHandler:
		object["command"] = hook.Handler.Command
		if hook.Handler.Timeout != nil {
			object["timeout_secs"] = *hook.Handler.Timeout
		}
	case HTTPHandler:
		object["url"] = hook.Handler.URL
		if hook.Handler.Method != "" {
			object["method"] = hook.Handler.Method
		}
		if len(hook.Handler.Headers) != 0 {
			object["headers"] = hook.Handler.Headers
		}
		if hook.Handler.Body != nil {
			object["body"] = hook.Handler.Body
		}
	case PromptHandler:
		object["prompt"] = hook.Handler.Prompt
		if hook.Handler.Model != "" {
			object["model"] = hook.Handler.Model
		}
	case AgentHandler:
		object["prompt"] = hook.Handler.Prompt
		if hook.Handler.Agent != "" {
			object["agent"] = hook.Handler.Agent
		}
		if hook.Handler.Model != "" {
			object["model"] = hook.Handler.Model
		}
	}
	if includeExtra {
		for key, value := range hook.RawExtra {
			object[key] = value
		}
	}
	return object
}

func PushDiagnostic(output *RenderOutput, level string, hook Hook, message string) {
	output.Diagnostics = append(output.Diagnostics, Diagnostic{Level: level, Source: fmt.Sprintf("%s (%s)", hook.Origin.Module, hook.Origin.SourceFile), Message: message})
	switch level {
	case "native":
		output.Summary.Native++
	case "emulated":
		output.Summary.Emulated++
	case "degraded":
		output.Summary.Degraded++
	case "omitted":
		output.Summary.Omitted++
	}
}

func CheckSupport(target harness.Target, hook Hook, support Support, output *RenderOutput, nativeMessage, emulatedMessage string) (bool, error) {
	switch support.Kind {
	case Unsupported:
		if hook.Strict() {
			return false, StrictMappingError(target, hook, support.Reason)
		}
		PushDiagnostic(output, "omitted", hook, support.Reason)
		return false, nil
	case Degraded:
		PushDiagnostic(output, "degraded", hook, support.Reason)
	case Native:
		PushDiagnostic(output, "native", hook, nativeMessage)
	case Emulated:
		PushDiagnostic(output, "emulated", hook, emulatedMessage)
	}
	return true, nil
}

func StrictMappingError(target harness.Target, hook Hook, reason string) error {
	return fmt.Errorf("hook %s from %s cannot be rendered safely for %s: %s", hook.Event, hook.Origin.SourceFile, target, reason)
}

func WriteRenderedFiles(output RenderOutput) error {
	for _, file := range output.Files {
		if err := os.MkdirAll(filepath.Dir(file.Path), 0o755); err != nil {
			return err
		}
		if file.JSON != nil {
			data, err := json.MarshalIndent(file.JSON, "", "  ")
			if err != nil {
				return err
			}
			data = append(data, '\n')
			if err := os.WriteFile(file.Path, data, 0o644); err != nil {
				return err
			}
		} else if err := os.WriteFile(file.Path, []byte(file.Text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(value string) string {
	if shellSafe.MatchString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
