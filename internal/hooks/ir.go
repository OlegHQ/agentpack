package hooks

import (
	"encoding/json"
	"fmt"

	"github.com/olegpustovit/agentpack/internal/harness"
)

type Event string

const (
	PreToolUse        Event = "PreToolUse"
	PostToolUse       Event = "PostToolUse"
	UserPromptSubmit  Event = "UserPromptSubmit"
	Stop              Event = "Stop"
	SubagentStop      Event = "SubagentStop"
	SessionStart      Event = "SessionStart"
	SessionEnd        Event = "SessionEnd"
	PreCompact        Event = "PreCompact"
	Notification      Event = "Notification"
	PermissionRequest Event = "PermissionRequest"
)

// Events that Claude Code and Cursor both have, under their Claude Code names.
const (
	PostToolUseFailure Event = "PostToolUseFailure"
	SubagentStart      Event = "SubagentStart"
)

// claudeEvents is the hook event list of the Claude Code hooks reference
// (code.claude.com/docs/en/hooks, read 2026-10-13).
var claudeEvents = eventSet(
	"SessionStart", "Setup", "UserPromptSubmit", "UserPromptExpansion", "PreToolUse", "PermissionRequest",
	"PermissionDenied", "PostToolUse", "PostToolUseFailure", "PostToolBatch", "Notification", "MessageDisplay",
	"SubagentStart", "SubagentStop", "TaskCreated", "TaskCompleted", "Stop", "StopFailure", "TeammateIdle",
	"InstructionsLoaded", "ConfigChange", "CwdChanged", "DirectoryAdded", "FileChanged", "WorktreeCreate",
	"WorktreeRemove", "PreCompact", "PostCompact", "PreModelSwitch", "PostModelSwitch", "Elicitation",
	"ElicitationResult", "SessionEnd",
)

// cursorEvents is the hook event list of the Cursor hooks reference
// (cursor.com/docs/hooks, read 2026-10-13), each with the event it is in this
// package: the Claude Code name where both agents have the event, otherwise
// Cursor's own name, which then only Cursor can run.
var cursorEvents = map[string]Event{
	"sessionStart": SessionStart, "sessionEnd": SessionEnd, "preToolUse": PreToolUse, "postToolUse": PostToolUse,
	"postToolUseFailure": PostToolUseFailure, "subagentStart": SubagentStart, "subagentStop": SubagentStop,
	"beforeSubmitPrompt": UserPromptSubmit, "preCompact": PreCompact, "stop": Stop,
	"beforeShellExecution": "beforeShellExecution", "afterShellExecution": "afterShellExecution",
	"beforeMCPExecution": "beforeMCPExecution", "afterMCPExecution": "afterMCPExecution",
	"beforeReadFile": "beforeReadFile", "afterFileEdit": "afterFileEdit",
	"afterAgentResponse": "afterAgentResponse", "afterAgentThought": "afterAgentThought",
	"beforeTabFileRead": "beforeTabFileRead", "afterTabFileEdit": "afterTabFileEdit", "workspaceOpen": "workspaceOpen",
}

func eventSet(names ...string) map[Event]bool {
	set := make(map[Event]bool, len(names))
	for _, name := range names {
		set[Event(name)] = true
	}
	return set
}

// ParseEvent reads an event as a hook file or the hook-exec command line
// names it: a Claude Code event, its dashed alias, or a Cursor event.
func ParseEvent(value string) (Event, bool) {
	aliases := map[string]Event{
		"pre-tool-use": PreToolUse, "post-tool-use": PostToolUse, "user-prompt-submit": UserPromptSubmit,
		"stop": Stop, "subagent-stop": SubagentStop, "session-start": SessionStart, "session-end": SessionEnd,
		"pre-compact": PreCompact, "notification": Notification, "permission-request": PermissionRequest,
	}
	if event, found := aliases[value]; found {
		return event, true
	}
	if claudeEvents[Event(value)] {
		return Event(value), true
	}
	event, found := cursorEvents[value]
	return event, found
}

// ParseCursorEvent reads an event name of a Cursor hooks file.
func ParseCursorEvent(value string) (Event, bool) {
	event, found := cursorEvents[value]
	return event, found
}

// Claude reports whether Claude Code has the event.
func (event Event) Claude() bool { return claudeEvents[event] }

// CursorName is the event's name in a Cursor hooks file, when Cursor has it.
func (event Event) CursorName() (string, bool) {
	for name, mapped := range cursorEvents {
		if mapped == event {
			return name, true
		}
	}
	return "", false
}

// Dialect says which agent's hook file a hook was published in. The handler
// reads that agent's input and writes that agent's output.
type Dialect string

const (
	ClaudeDialect Dialect = "claude"
	CursorDialect Dialect = "cursor"
	CodexDialect  Dialect = "codex"
)

type Layer string

const (
	SeededNative Layer = "SeededNative"
	PackPlugin   Layer = "PackPlugin"
	BareSkill    Layer = "BareSkill"
	DotAgents    Layer = "DotAgents"
)

func (layer Layer) Rank() int {
	switch layer {
	case SeededNative:
		return 0
	case PackPlugin:
		return 1
	case BareSkill:
		return 2
	default:
		return 3
	}
}

type HandlerKind string

const (
	CommandHandler HandlerKind = "command"
	HTTPHandler    HandlerKind = "http"
	PromptHandler  HandlerKind = "prompt"
	AgentHandler   HandlerKind = "agent"
)

type Handler struct {
	Kind    HandlerKind
	Command string
	// Args makes a command hook exec-form, as Claude Code writes it: Command
	// is the program and Args its arguments, run without a shell.
	Args    []string
	Timeout *uint64
	URL     string
	Method  string
	Headers map[string]string
	Body    any
	Prompt  string
	Agent   string
	Model   string
}

// MarshalJSON preserves the externally tagged representation used by hook
// execution specs.
func (handler Handler) MarshalJSON() ([]byte, error) {
	inner := make(map[string]any)
	tag := ""
	switch handler.Kind {
	case CommandHandler:
		tag, inner["command"] = "Command", handler.Command
		if len(handler.Args) != 0 {
			inner["args"] = handler.Args
		}
		if handler.Timeout != nil {
			inner["timeout_secs"] = *handler.Timeout
		}
	case HTTPHandler:
		tag, inner["url"] = "Http", handler.URL
		if handler.Method != "" {
			inner["method"] = handler.Method
		}
		if len(handler.Headers) != 0 {
			inner["headers"] = handler.Headers
		}
		if handler.Body != nil {
			inner["body"] = handler.Body
		}
	case PromptHandler:
		tag, inner["prompt"] = "Prompt", handler.Prompt
		if handler.Model != "" {
			inner["model"] = handler.Model
		}
	case AgentHandler:
		tag, inner["prompt"] = "Agent", handler.Prompt
		if handler.Agent != "" {
			inner["agent"] = handler.Agent
		}
		if handler.Model != "" {
			inner["model"] = handler.Model
		}
	default:
		return nil, fmt.Errorf("unknown handler kind %q", handler.Kind)
	}
	return json.Marshal(map[string]any{tag: inner})
}

func (handler *Handler) UnmarshalJSON(data []byte) error {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(data, &outer); err != nil {
		return err
	}
	if len(outer) != 1 {
		return fmt.Errorf("hook handler must contain exactly one variant")
	}
	for tag, raw := range outer {
		var inner struct {
			Command     string            `json:"command"`
			Args        []string          `json:"args"`
			TimeoutSecs *uint64           `json:"timeout_secs"`
			URL         string            `json:"url"`
			Method      string            `json:"method"`
			Headers     map[string]string `json:"headers"`
			Body        any               `json:"body"`
			Prompt      string            `json:"prompt"`
			Agent       string            `json:"agent"`
			Model       string            `json:"model"`
		}
		if err := json.Unmarshal(raw, &inner); err != nil {
			return err
		}
		switch tag {
		case "Command":
			handler.Kind = CommandHandler
		case "Http":
			handler.Kind = HTTPHandler
		case "Prompt":
			handler.Kind = PromptHandler
		case "Agent":
			handler.Kind = AgentHandler
		default:
			return fmt.Errorf("unknown hook handler variant %q", tag)
		}
		handler.Command, handler.Args, handler.Timeout = inner.Command, inner.Args, inner.TimeoutSecs
		handler.URL, handler.Method, handler.Headers, handler.Body = inner.URL, inner.Method, inner.Headers, inner.Body
		handler.Prompt, handler.Agent, handler.Model = inner.Prompt, inner.Agent, inner.Model
	}
	return nil
}

type Origin struct {
	Layer             Layer  `json:"layer"`
	Module            string `json:"module"`
	CacheKey          string `json:"cache_key,omitempty"`
	SourceRelative    string `json:"source_rel"`
	SourceRoot        string `json:"source_root"`
	SourceFile        string `json:"source_file"`
	PackageKey        string `json:"package_key"`
	EventIndex        int    `json:"event_index"`
	MatcherGroupIndex int    `json:"matcher_group_index"`
	HookIndex         int    `json:"hook_index"`
}

// SourcePackage identifies the package (or layer) a hook came from.
func (origin Origin) SourcePackage() string {
	return fmt.Sprintf("%d:%s:%s", origin.Layer.Rank(), origin.Module, origin.PackageKey)
}

func (origin Origin) SourceID() string {
	return fmt.Sprintf("%d:%s:%s:%s", origin.Layer.Rank(), origin.Module, origin.PackageKey, origin.SourceRelative)
}

type Hook struct {
	Event             Event          `json:"event"`
	Dialect           Dialect        `json:"dialect,omitempty"`
	Matcher           string         `json:"matcher,omitempty"`
	Handler           Handler        `json:"handler"`
	Origin            Origin         `json:"origin"`
	MatcherGroupExtra map[string]any `json:"matcher_group_extra,omitempty"`
	RawExtra          map[string]any `json:"raw_extra,omitempty"`
}

func (hook Hook) Strict() bool { return hook.Event == PreToolUse || hook.Event == PermissionRequest }

type Bundle struct {
	Hooks []Hook `json:"hooks,omitempty"`
	// Skipped names what a hook file holds that was not read: an event no
	// agent documents, a handler type agentpack cannot run, a file that does
	// not parse. The rest of the package is unaffected.
	Skipped []Diagnostic `json:"-"`
}

// ForTarget keeps, for each package, the hooks of one dialect: the target's
// own when the package publishes hooks for that agent, otherwise Claude
// Code's, then Cursor's, then Codex's. A package that ships a hook file per
// agent would otherwise run every one of them on each agent.
func (bundle Bundle) ForTarget(target harness.Target) Bundle {
	own := map[harness.Target]Dialect{harness.Claude: ClaudeDialect, harness.Cursor: CursorDialect, harness.Codex: CodexDialect}[target]
	available := make(map[string]map[Dialect]bool)
	for _, hook := range bundle.Hooks {
		key := hook.Origin.SourcePackage()
		if available[key] == nil {
			available[key] = make(map[Dialect]bool)
		}
		available[key][hook.Dialect] = true
	}
	chosen := make(map[string]Dialect, len(available))
	for key, dialects := range available {
		for _, candidate := range []Dialect{own, ClaudeDialect, CursorDialect, CodexDialect, ""} {
			if dialects[candidate] {
				chosen[key] = candidate
				break
			}
		}
	}
	selected := Bundle{Skipped: bundle.Skipped}
	for _, hook := range bundle.Hooks {
		if chosen[hook.Origin.SourcePackage()] == hook.Dialect {
			selected.Hooks = append(selected.Hooks, hook)
		}
	}
	return selected
}

type Decision string

const (
	Allow Decision = "allow"
	Ask   Decision = "ask"
	Deny  Decision = "deny"
)

type Result struct {
	Decision          Decision       `json:"decision"`
	Message           string         `json:"message,omitempty"`
	AdditionalContext string         `json:"additional_context,omitempty"`
	UpdatedInput      any            `json:"updated_input,omitempty"`
	UpdatedToolOutput any            `json:"updated_tool_output,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}
