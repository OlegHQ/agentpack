package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/OlegHQ/agentpack/internal/harness"
)

type CommandOutput struct {
	Stdout, Stderr []byte
	ExitCode       int
}

func LoadExecutionSpec(path string) (ExecutionSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ExecutionSpec{}, err
	}
	var spec ExecutionSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return ExecutionSpec{}, fmt.Errorf("parse hook execution spec: %w", err)
	}
	return spec, nil
}

func NormalizeResponse(value any) Result {
	object, ok := value.(map[string]any)
	if !ok {
		return Result{Decision: Allow}
	}
	copy := make(map[string]any, len(object))
	for key, item := range object {
		copy[key] = item
	}
	decisionText := takeString(copy, "decision", "permission", "permissionDecision")
	decision := Allow
	switch decisionText {
	case "deny", "block":
		decision = Deny
	case "ask":
		decision = Ask
	}
	if decisionText == "" {
		if continued, ok := copy["continue"].(bool); ok && !continued {
			decision = Deny
		}
	}
	return Result{
		Decision:          decision,
		Message:           takeString(copy, "message", "permissionDecisionReason", "user_message", "agent_message", "stopReason"),
		AdditionalContext: takeString(copy, "additional_context", "additionalContext"),
		UpdatedInput:      takeValue(copy, "updated_input", "updatedInput"),
		UpdatedToolOutput: takeValue(copy, "updated_tool_output", "updatedToolOutput", "updated_mcp_tool_output", "updatedMCPToolOutput"),
		Metadata:          copy,
	}
}

func ExtractJSONObject(text string) (any, error) {
	var value any
	if json.Unmarshal([]byte(text), &value) == nil {
		return value, nil
	}
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("no JSON object found")
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &value); err != nil {
		return nil, fmt.Errorf("parse JSON object from command output: %w", err)
	}
	return value, nil
}

func RunCommand(ctx context.Context, spec ExecutionSpec, stdin []byte) (CommandOutput, error) {
	if spec.Handler.Kind != CommandHandler {
		return CommandOutput{}, fmt.Errorf("command executor received non-command hook")
	}
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.CommandContext(ctx, "cmd", "/C", spec.Handler.Command)
	} else {
		command = exec.CommandContext(ctx, "/bin/sh", "-c", spec.Handler.Command)
	}
	command.Env = append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+spec.WorkingDir, "AGENTPACK_PLUGIN_ROOT="+spec.WorkingDir)
	command.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	exitCode := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return CommandOutput{}, fmt.Errorf("spawn hook command %q: %w", spec.Handler.Command, err)
		}
		exitCode = exit.ExitCode()
		if exitCode < 0 {
			exitCode = 1
		}
	}
	return CommandOutput{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: exitCode}, nil
}

func RunHTTP(ctx context.Context, client *http.Client, spec ExecutionSpec, stdin []byte) (Result, error) {
	if spec.Handler.Kind != HTTPHandler {
		return Result{}, fmt.Errorf("http executor received non-http hook")
	}
	method := spec.Handler.Method
	if method == "" {
		method = http.MethodPost
	}
	var body io.Reader
	if spec.Handler.Body != nil {
		encoded, err := json.Marshal(spec.Handler.Body)
		if err != nil {
			return Result{}, err
		}
		body = bytes.NewReader(encoded)
	} else if method != http.MethodGet && len(stdin) != 0 {
		body = bytes.NewReader(stdin)
	}
	request, err := http.NewRequestWithContext(ctx, method, spec.Handler.URL, body)
	if err != nil {
		return Result{}, fmt.Errorf("invalid HTTP method: %w", err)
	}
	for key, value := range spec.Handler.Headers {
		request.Header.Set(key, value)
	}
	if body != nil {
		request.Header.Set("content-type", "application/json")
	}
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("send hook HTTP request: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return Result{Decision: Allow}, nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return Result{}, fmt.Errorf("not JSON response: %w", err)
	}
	result := NormalizeResponse(value)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if result.Message == "" {
			result.Message = "hook HTTP request failed with status " + response.Status
		}
	}
	return result, nil
}

func RunPrompt(ctx context.Context, spec ExecutionSpec, stdin []byte) (Result, error) {
	if spec.Handler.Kind != PromptHandler {
		return Result{}, fmt.Errorf("prompt executor received non-prompt hook")
	}
	prompt := fmt.Sprintf("You are executing an agentpack prompt hook. Return ONLY JSON with keys decision, message, additional_context, updated_input, updated_tool_output.\n\nAuthor prompt:\n%s\n\nHook input JSON:\n%s", spec.Handler.Prompt, prettyInput(stdin))
	return runCodexExec(ctx, prompt, spec.Handler.Model, spec.WorkingDir)
}

func RunAgent(ctx context.Context, spec ExecutionSpec, stdin []byte) (Result, error) {
	if spec.Handler.Kind != AgentHandler {
		return Result{}, fmt.Errorf("agent executor received non-agent hook")
	}
	agent := ""
	if spec.Handler.Agent != "" {
		agent = " for agent `" + spec.Handler.Agent + "`"
	}
	prompt := fmt.Sprintf("You are executing an agentpack agent hook%s.\nReturn ONLY JSON with keys decision, message, additional_context, updated_input, updated_tool_output.\n\nHook instructions:\n%s\n\nHook input JSON:\n%s", agent, spec.Handler.Prompt, prettyInput(stdin))
	return runCodexExec(ctx, prompt, spec.Handler.Model, spec.WorkingDir)
}

func runCodexExec(ctx context.Context, prompt, model, workingDirectory string) (Result, error) {
	binary := os.Getenv("CODEX_PATH")
	if binary == "" {
		binary = "codex"
	}
	arguments := []string{"exec", "--ephemeral", "-C", workingDirectory}
	if model != "" {
		arguments = append(arguments, "--model", model)
	}
	arguments = append(arguments, prompt)
	command := exec.CommandContext(ctx, binary, arguments...)
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return Result{}, fmt.Errorf("codex exec failed: %s", exit.Stderr)
		}
		return Result{}, fmt.Errorf("spawn codex exec for hook prompt: %w", err)
	}
	value, err := ExtractJSONObject(string(output))
	if err != nil {
		return Result{}, err
	}
	return NormalizeResponse(value), nil
}

type DispatchArgs struct {
	Target         harness.Target
	Event          Event
	SpecsDirectory string
	Stdin          []byte
	Client         *http.Client
}
type DispatchOutcome struct {
	JSON     any
	ExitCode int
}

func Dispatch(ctx context.Context, arguments DispatchArgs) (DispatchOutcome, error) {
	var stdinValue any
	_ = json.Unmarshal(arguments.Stdin, &stdinValue)
	tool := ExtractToolName(stdinValue)
	candidates := CandidateToolNames(tool)
	specs := loadSpecs(arguments.SpecsDirectory)
	merged := Result{Decision: Allow, Metadata: make(map[string]any)}
	fired := 0
	for _, loaded := range specs {
		spec := loaded.spec
		if spec.Event != arguments.Event || !MatcherMatches(spec.Matcher, candidates) {
			continue
		}
		result, err := executeSpec(ctx, spec, arguments.Stdin, arguments.Client)
		if err != nil {
			if arguments.Event == PreToolUse || arguments.Event == PermissionRequest {
				merged.Decision = Deny
				appendMessage(&merged.Message, "hook dispatch error ("+loaded.path+"): "+err.Error(), "\n")
			}
			continue
		}
		mergeResults(&merged, result)
		fired++
	}
	if fired == 0 && merged.Decision != Deny {
		return DispatchOutcome{JSON: map[string]any{}}, nil
	}
	exitCode := 0
	if merged.Decision == Deny {
		exitCode = 2
	}
	return DispatchOutcome{JSON: HookOutput(arguments.Target, arguments.Event, merged), ExitCode: exitCode}, nil
}

func ExtractToolName(value any) string {
	object, _ := value.(map[string]any)
	if text, ok := object["tool_name"].(string); ok {
		return text
	}
	if text, ok := object["tool"].(string); ok {
		return text
	}
	if tool, ok := object["tool"].(map[string]any); ok {
		if text, ok := tool["name"].(string); ok {
			return text
		}
	}
	return ""
}

func CandidateToolNames(raw string) []string {
	if raw == "" {
		return nil
	}
	result := []string{raw}
	pairs := map[string]string{"Shell": "Bash", "Bash": "Shell", "Write": "Edit", "Edit": "Write", "Fetch": "WebFetch", "WebFetch": "Fetch"}
	if other := pairs[raw]; other != "" {
		result = append(result, other)
	}
	if rest, ok := strings.CutPrefix(raw, "MCP:"); ok {
		result = append(result, "mcp__"+rest)
	} else if rest, ok := strings.CutPrefix(raw, "mcp__"); ok {
		result = append(result, "MCP:"+rest)
	}
	return result
}

func MatcherMatches(matcher string, candidates []string) bool {
	matcher = strings.TrimSpace(matcher)
	if matcher == "" {
		return true
	}
	expression, err := regexp.Compile("^(?:" + matcher + ")$")
	if err != nil {
		return false
	}
	for _, candidate := range candidates {
		if expression.MatchString(candidate) {
			return true
		}
	}
	return false
}

type loadedSpec struct {
	path string
	spec ExecutionSpec
}

func loadSpecs(root string) []loadedSpec {
	var paths []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && filepath.Ext(path) == ".json" {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	var specs []loadedSpec
	for _, path := range paths {
		if spec, err := LoadExecutionSpec(path); err == nil {
			specs = append(specs, loadedSpec{path: path, spec: spec})
		}
	}
	return specs
}

func executeSpec(ctx context.Context, spec ExecutionSpec, stdin []byte, client *http.Client) (Result, error) {
	switch spec.Handler.Kind {
	case CommandHandler:
		output, err := RunCommand(ctx, spec, stdin)
		if err != nil {
			return Result{}, err
		}
		result := Result{Decision: Allow}
		if len(output.Stdout) != 0 {
			if value, err := ExtractJSONObject(string(output.Stdout)); err == nil {
				result = NormalizeResponse(value)
			}
		}
		if output.ExitCode == 2 {
			result.Decision = Deny
			if result.Message == "" {
				result.Message = string(output.Stderr)
			}
		}
		return result, nil
	case HTTPHandler:
		return RunHTTP(ctx, client, spec, stdin)
	case PromptHandler:
		return RunPrompt(ctx, spec, stdin)
	case AgentHandler:
		return RunAgent(ctx, spec, stdin)
	default:
		return Result{}, fmt.Errorf("unknown handler kind %q", spec.Handler.Kind)
	}
}

func mergeResults(target *Result, next Result) {
	if next.Decision == Deny {
		target.Decision = Deny
	} else if target.Decision != Deny && next.Decision == Ask {
		target.Decision = Ask
	}
	appendMessage(&target.Message, next.Message, "\n")
	appendMessage(&target.AdditionalContext, next.AdditionalContext, "\n\n")
	if next.UpdatedInput != nil {
		target.UpdatedInput = next.UpdatedInput
	}
	if next.UpdatedToolOutput != nil {
		target.UpdatedToolOutput = next.UpdatedToolOutput
	}
	if target.Metadata == nil {
		target.Metadata = make(map[string]any)
	}
	for key, value := range next.Metadata {
		target.Metadata[key] = value
	}
}

func appendMessage(target *string, value, separator string) {
	if value == "" {
		return
	}
	if *target != "" {
		*target += separator
	}
	*target += value
}
func takeString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, found := object[key]; found {
			delete(object, key)
			if text, ok := value.(string); ok {
				return text
			}
		}
	}
	return ""
}
func takeValue(object map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, found := object[key]; found {
			delete(object, key)
			return value
		}
	}
	return nil
}
func prettyInput(data []byte) string {
	var value any
	if json.Unmarshal(data, &value) != nil {
		value = nil
	}
	output, _ := json.MarshalIndent(value, "", "  ")
	return string(output)
}
