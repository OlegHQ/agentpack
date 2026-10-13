package hooks

import (
	"errors"
	"fmt"
	"sort"
)

func ParseNested(sourcePath string, value any, base Origin) (Bundle, error) {
	root, ok := asObject(value)
	if !ok {
		return Bundle{}, invalidHook(sourcePath, "hook file must be a JSON object")
	}
	hooks, ok := asObject(root["hooks"])
	if !ok {
		return Bundle{}, invalidHook(sourcePath, "hook file must contain a top-level hooks object")
	}
	return parseEventGroups(sourcePath, hooks, base, false)
}

func ParseCodex(sourcePath string, value any, base Origin) (Bundle, error) {
	root, ok := asObject(value)
	if !ok {
		return Bundle{}, invalidHook(sourcePath, "Codex hooks file must be an object")
	}
	if nested, nestedOK := asObject(root["hooks"]); nestedOK {
		root = nested
	}
	return parseEventGroups(sourcePath, root, base, true)
}

func parseEventGroups(sourcePath string, root map[string]any, base Origin, codex bool) (Bundle, error) {
	var bundle Bundle
	eventNames := sortedKeys(root)
	for eventIndex, eventName := range eventNames {
		event, found := ParseEvent(eventName)
		if !found || !event.Claude() {
			bundle.skip(base, sourcePath, "skipping its %s hooks: %q is not a hook event of Claude Code or Codex", eventName, eventName)
			continue
		}
		entries, ok := root[eventName].([]any)
		if !ok {
			return Bundle{}, invalidHook(sourcePath, "event groups must be arrays")
		}
		for groupIndex, rawEntry := range entries {
			entry, ok := asObject(rawEntry)
			if !ok {
				return Bundle{}, invalidHook(sourcePath, "matcher groups must be objects")
			}
			matcher, _ := entry["matcher"].(string)
			if handlers, grouped := entry["hooks"].([]any); grouped {
				extra := objectExtra(entry, "matcher", "hooks")
				for hookIndex, rawHandler := range handlers {
					hook, err := buildHook(sourcePath, event, matcher, rawHandler, indexedOrigin(base, eventIndex, groupIndex, hookIndex), extra)
					if bundle.skipUnsupported(base, sourcePath, eventName, err) {
						continue
					}
					if err != nil {
						return Bundle{}, err
					}
					bundle.Hooks = append(bundle.Hooks, hook)
				}
				continue
			}
			if !codex {
				return Bundle{}, invalidHook(sourcePath, "matcher groups must contain hooks arrays")
			}
			hook, err := buildHook(sourcePath, event, matcher, rawEntry, indexedOrigin(base, eventIndex, groupIndex, 0), objectExtra(entry, "matcher"))
			if bundle.skipUnsupported(base, sourcePath, eventName, err) {
				continue
			}
			if err != nil {
				return Bundle{}, err
			}
			bundle.Hooks = append(bundle.Hooks, hook)
		}
	}
	return bundle, nil
}

// ParseCursor reads a Cursor hooks file: {"version": 1, "hooks": {"<event>":
// [{"command": "...", "matcher": "...", ...}]}}, with camelCase event names
// and one flat entry per hook.
func ParseCursor(sourcePath string, value any, base Origin) (Bundle, error) {
	root, ok := asObject(value)
	if !ok {
		return Bundle{}, invalidHook(sourcePath, "hook file must be a JSON object")
	}
	events, ok := asObject(root["hooks"])
	if !ok {
		return Bundle{}, invalidHook(sourcePath, "hook file must contain a top-level hooks object")
	}
	var bundle Bundle
	for eventIndex, eventName := range sortedKeys(events) {
		event, found := ParseCursorEvent(eventName)
		if !found {
			bundle.skip(base, sourcePath, "skipping its %s hooks: %q is not a Cursor hook event", eventName, eventName)
			continue
		}
		entries, ok := events[eventName].([]any)
		if !ok {
			return Bundle{}, invalidHook(sourcePath, "event groups must be arrays")
		}
		for entryIndex, rawEntry := range entries {
			entry, ok := asObject(rawEntry)
			if !ok {
				return Bundle{}, invalidHook(sourcePath, "hook entries must be JSON objects")
			}
			if _, typed := entry["type"]; !typed {
				copied := objectExtra(entry)
				copied["type"] = string(CommandHandler)
				entry = copied
			}
			matcher, _ := entry["matcher"].(string)
			hook, err := buildHook(sourcePath, event, matcher, entry, indexedOrigin(base, eventIndex, entryIndex, 0), nil)
			if bundle.skipUnsupported(base, sourcePath, eventName, err) {
				continue
			}
			if err != nil {
				return Bundle{}, err
			}
			delete(hook.RawExtra, "matcher")
			hook.Dialect = CursorDialect
			bundle.Hooks = append(bundle.Hooks, hook)
		}
	}
	return bundle, nil
}

// LooksLikeCursor reports whether a hooks file found at a default location is
// in Cursor's format: it names Cursor events and no Claude Code event.
func LooksLikeCursor(value any) bool {
	root, _ := asObject(value)
	events, _ := asObject(root["hooks"])
	cursor := false
	for name := range events {
		if claudeEvents[Event(name)] {
			return false
		}
		if _, found := cursorEvents[name]; found {
			cursor = true
		}
	}
	return cursor
}

type unsupportedHandler struct{ kind string }

func (err unsupportedHandler) Error() string { return "unsupported hook type " + err.kind }

func (bundle *Bundle) skip(origin Origin, sourcePath, format string, arguments ...any) {
	bundle.Skipped = append(bundle.Skipped, Diagnostic{Level: "skipped", Source: fmt.Sprintf("%s (%s)", origin.Module, sourcePath), Message: fmt.Sprintf(format, arguments...)})
}

// skipUnsupported records a handler of a type agentpack cannot run and
// reports whether err was that.
func (bundle *Bundle) skipUnsupported(origin Origin, sourcePath, eventName string, err error) bool {
	var unsupported unsupportedHandler
	if !errors.As(err, &unsupported) {
		return false
	}
	bundle.skip(origin, sourcePath, "skipping a %s hook: hooks of type %q are not run by agentpack", eventName, unsupported.kind)
	return true
}

func buildHook(sourcePath string, event Event, matcher string, raw any, origin Origin, groupExtra map[string]any) (Hook, error) {
	object, ok := asObject(raw)
	if !ok {
		return Hook{}, invalidHook(sourcePath, "hook entries must be JSON objects")
	}
	kindText, ok := object["type"].(string)
	if !ok {
		return Hook{}, invalidHook(sourcePath, "hook entries must include type")
	}
	handler := Handler{Kind: HandlerKind(kindText)}
	var excluded []string
	switch handler.Kind {
	case CommandHandler:
		handler.Command, ok = object["command"].(string)
		if !ok {
			return Hook{}, invalidHook(sourcePath, "command hook missing command")
		}
		if timeout, ok := unsigned(object["timeout"]); ok {
			handler.Timeout = &timeout
		}
		excluded = []string{"type", "command", "timeout"}
	case HTTPHandler:
		handler.URL, ok = object["url"].(string)
		if !ok {
			return Hook{}, invalidHook(sourcePath, "http hook missing url")
		}
		handler.Method, _ = object["method"].(string)
		handler.Headers = stringMap(object["headers"])
		handler.Body = object["body"]
		excluded = []string{"type", "url", "method", "headers", "body"}
	case PromptHandler:
		handler.Prompt, ok = object["prompt"].(string)
		if !ok {
			return Hook{}, invalidHook(sourcePath, "prompt hook missing prompt")
		}
		handler.Model, _ = object["model"].(string)
		excluded = []string{"type", "prompt", "model"}
	case AgentHandler:
		handler.Prompt, ok = object["prompt"].(string)
		if !ok {
			handler.Prompt, ok = object["instruction"].(string)
		}
		if !ok {
			return Hook{}, invalidHook(sourcePath, "agent hook missing prompt")
		}
		handler.Agent, _ = object["agent"].(string)
		handler.Model, _ = object["model"].(string)
		excluded = []string{"type", "prompt", "instruction", "agent", "model"}
	default:
		return Hook{}, unsupportedHandler{kind: kindText}
	}
	return Hook{Event: event, Matcher: matcher, Handler: handler, Origin: origin, MatcherGroupExtra: groupExtra, RawExtra: objectExtra(object, excluded...)}, nil
}

func indexedOrigin(origin Origin, event, group, hook int) Origin {
	origin.EventIndex, origin.MatcherGroupIndex, origin.HookIndex = event, group, hook
	return origin
}

func asObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func objectExtra(object map[string]any, excluded ...string) map[string]any {
	skip := make(map[string]bool, len(excluded))
	for _, key := range excluded {
		skip[key] = true
	}
	extra := make(map[string]any)
	for key, value := range object {
		if !skip[key] {
			extra[key] = value
		}
	}
	return extra
}

func unsigned(value any) (uint64, bool) {
	switch number := value.(type) {
	case float64:
		if number >= 0 && number == float64(uint64(number)) {
			return uint64(number), true
		}
	case uint64:
		return number, true
	case int:
		if number >= 0 {
			return uint64(number), true
		}
	}
	return 0, false
}

func stringMap(value any) map[string]string {
	object, _ := asObject(value)
	result := make(map[string]string)
	for key, value := range object {
		if text, ok := value.(string); ok {
			result[key] = text
		}
	}
	return result
}

func invalidHook(path, format string, arguments ...any) error {
	return fmt.Errorf("invalid hook file %s: %s", path, fmt.Sprintf(format, arguments...))
}
