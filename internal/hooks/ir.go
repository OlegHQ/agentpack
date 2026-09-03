package hooks

import (
	"encoding/json"
	"fmt"
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

func ParseEvent(value string) (Event, bool) {
	aliases := map[string]Event{
		"PreToolUse": PreToolUse, "pre-tool-use": PreToolUse,
		"PostToolUse": PostToolUse, "post-tool-use": PostToolUse,
		"UserPromptSubmit": UserPromptSubmit, "user-prompt-submit": UserPromptSubmit,
		"Stop": Stop, "stop": Stop, "SubagentStop": SubagentStop, "subagent-stop": SubagentStop,
		"SessionStart": SessionStart, "session-start": SessionStart,
		"SessionEnd": SessionEnd, "session-end": SessionEnd,
		"PreCompact": PreCompact, "pre-compact": PreCompact,
		"Notification": Notification, "notification": Notification,
		"PermissionRequest": PermissionRequest, "permission-request": PermissionRequest,
	}
	event, found := aliases[value]
	return event, found
}

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
	Timeout *uint64
	URL     string
	Method  string
	Headers map[string]string
	Body    any
	Prompt  string
	Agent   string
	Model   string
}

// MarshalJSON preserves the Rust serde externally-tagged representation used
// by hook execution specs.
func (handler Handler) MarshalJSON() ([]byte, error) {
	inner := make(map[string]any)
	tag := ""
	switch handler.Kind {
	case CommandHandler:
		tag, inner["command"] = "Command", handler.Command
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
		handler.Command, handler.Timeout = inner.Command, inner.TimeoutSecs
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

func (origin Origin) SourceID() string {
	return fmt.Sprintf("%d:%s:%s:%s", origin.Layer.Rank(), origin.Module, origin.PackageKey, origin.SourceRelative)
}

type Hook struct {
	Event             Event          `json:"event"`
	Matcher           string         `json:"matcher,omitempty"`
	Handler           Handler        `json:"handler"`
	Origin            Origin         `json:"origin"`
	MatcherGroupExtra map[string]any `json:"matcher_group_extra,omitempty"`
	RawExtra          map[string]any `json:"raw_extra,omitempty"`
}

func (hook Hook) Strict() bool { return hook.Event == PreToolUse || hook.Event == PermissionRequest }

type Bundle struct {
	Hooks []Hook `json:"hooks,omitempty"`
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
