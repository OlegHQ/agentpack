package hooks

import (
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
		if !found {
			return Bundle{}, invalidHook(sourcePath, "unknown hook event %q", eventName)
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
			if err != nil {
				return Bundle{}, err
			}
			bundle.Hooks = append(bundle.Hooks, hook)
		}
	}
	return bundle, nil
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
		return Hook{}, invalidHook(sourcePath, "unsupported Claude hook type %q", kindText)
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
