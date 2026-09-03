package proxy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type TranslateOptions struct {
	SessionID, ServiceTier, ServiceTierOverride, EffortOverride string
}

func TranslateAnthropic(request map[string]any, options TranslateOptions) (map[string]any, error) {
	instructions := flattenSystem(request["system"])
	input := buildInput(array(request["messages"]))
	result := map[string]any{
		"model": stringValue(request["model"]), "instructions": instructions, "input": input,
		"tool_choice":         mapToolChoice(object(request["tool_choice"]), array(request["tools"])),
		"parallel_tool_calls": true, "store": false, "stream": true,
		"text": map[string]any{"verbosity": "low"},
	}
	if tools := mapTools(array(request["tools"])); len(tools) != 0 {
		result["tools"] = tools
	}
	output := object(request["output_config"])
	if format := object(output["format"]); stringValue(format["type"]) == "json_schema" {
		name := stringValue(format["name"])
		if name == "" {
			name = "response"
		}
		result["text"].(map[string]any)["format"] = map[string]any{"type": "json_schema", "name": name, "schema": normalizeSchema(format["schema"]), "strict": true}
	}
	effort := stringValue(output["effort"])
	if options.EffortOverride != "" {
		effort = options.EffortOverride
	}
	if effort != "" && !oneOf(effort, "none", "low", "medium", "high", "xhigh", "max") {
		return nil, fmt.Errorf("invalid output_config.effort: %q; must be one of: none, low, medium, high, xhigh, max", effort)
	}
	if effort == "max" {
		effort = "xhigh"
	}
	if effort != "" {
		result["reasoning"] = map[string]any{"effort": effort}
		result["include"] = []any{"reasoning.encrypted_content"}
	}
	tier := options.ServiceTier
	if options.ServiceTierOverride != "" {
		tier = options.ServiceTierOverride
	}
	if tier != "" {
		if tier == "fast" {
			tier = "priority"
		}
		if !oneOf(tier, "priority", "flex") {
			return nil, fmt.Errorf("invalid service tier override: %q; must be one of: fast, priority, flex", tier)
		}
		result["service_tier"] = tier
	}
	if options.SessionID != "" {
		result["prompt_cache_key"] = options.SessionID
	}
	return result, nil
}

func flattenSystem(value any) string {
	if text, ok := value.(string); ok {
		if !strings.HasPrefix(text, "x-anthropic-billing-header:") {
			return text
		}
		return ""
	}
	var texts []string
	for _, block := range array(value) {
		item := object(block)
		text := stringValue(item["text"])
		if stringValue(item["type"]) == "text" && !strings.HasPrefix(text, "x-anthropic-billing-header:") {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n\n")
}

func buildInput(messages []any) []any {
	var result []any
	for _, raw := range messages {
		message := object(raw)
		role := stringValue(message["role"])
		blocks := normalizeContent(message["content"])
		switch role {
		case "user":
			var parts []any
			flush := func() {
				if len(parts) != 0 {
					result = append(result, map[string]any{"type": "message", "role": "user", "content": parts})
					parts = nil
				}
			}
			for _, rawBlock := range blocks {
				block := object(rawBlock)
				switch stringValue(block["type"]) {
				case "text":
					parts = append(parts, map[string]any{"type": "input_text", "text": stringValue(block["text"])})
				case "image":
					source := object(block["source"])
					url := stringValue(source["url"])
					if stringValue(source["type"]) == "base64" {
						url = "data:" + stringValue(source["media_type"]) + ";base64," + stringValue(source["data"])
					}
					parts = append(parts, map[string]any{"type": "input_image", "image_url": url})
				case "tool_result":
					flush()
					body := toolResultString(block["content"])
					if boolean(block["is_error"]) {
						body = "[tool execution error]\n" + body
					}
					result = append(result, map[string]any{"type": "function_call_output", "call_id": stringValue(block["tool_use_id"]), "output": body})
				}
			}
			flush()
		case "system":
			var parts []any
			for _, rawBlock := range blocks {
				block := object(rawBlock)
				if stringValue(block["type"]) == "text" {
					parts = append(parts, map[string]any{"type": "input_text", "text": stringValue(block["text"])})
				}
			}
			if len(parts) != 0 {
				result = append(result, map[string]any{"type": "message", "role": "developer", "content": parts})
			}
		default:
			var parts []any
			flush := func() {
				if len(parts) != 0 {
					result = append(result, map[string]any{"type": "message", "role": "assistant", "content": parts})
					parts = nil
				}
			}
			for _, rawBlock := range blocks {
				block := object(rawBlock)
				switch stringValue(block["type"]) {
				case "text":
					parts = append(parts, map[string]any{"type": "output_text", "text": stringValue(block["text"])})
				case "tool_use":
					flush()
					encoded, _ := json.Marshal(block["input"])
					result = append(result, map[string]any{"type": "function_call", "call_id": stringValue(block["id"]), "name": stringValue(block["name"]), "arguments": string(encoded)})
				}
			}
			flush()
		}
	}
	return result
}

func normalizeContent(value any) []any {
	if text, ok := value.(string); ok {
		return []any{map[string]any{"type": "text", "text": text}}
	}
	return array(value)
}
func toolResultString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	var lines []string
	for _, raw := range array(value) {
		block := object(raw)
		switch stringValue(block["type"]) {
		case "text":
			lines = append(lines, stringValue(block["text"]))
		case "image":
			source := object(block["source"])
			if stringValue(source["type"]) == "url" {
				lines = append(lines, "[image omitted: url]")
			} else {
				lines = append(lines, "[image omitted: "+stringValue(source["media_type"])+"]")
			}
		default:
			kind := stringValue(block["type"])
			if kind == "" {
				kind = "unknown"
			}
			lines = append(lines, "[unsupported content block omitted: "+kind+"]")
		}
	}
	return strings.Join(lines, "\n")
}

func mapTools(tools []any) []any {
	var result []any
	for _, raw := range tools {
		tool := object(raw)
		if stringValue(tool["type"]) == "web_search_20250305" {
			mapped := map[string]any{"type": "web_search", "external_web_access": false, "search_content_types": []any{"text", "image"}}
			filters := map[string]any{}
			if len(array(tool["allowed_domains"])) != 0 {
				filters["allowed_domains"] = tool["allowed_domains"]
			}
			if len(array(tool["blocked_domains"])) != 0 {
				filters["blocked_domains"] = tool["blocked_domains"]
			}
			if len(filters) != 0 {
				mapped["filters"] = filters
			}
			result = append(result, mapped)
		} else {
			mapped := map[string]any{"type": "function", "name": stringValue(tool["name"]), "parameters": tool["input_schema"]}
			if mapped["parameters"] == nil {
				mapped["parameters"] = map[string]any{}
			}
			if description, ok := tool["description"]; ok {
				mapped["description"] = description
			}
			result = append(result, mapped)
		}
	}
	return result
}
func mapToolChoice(choice map[string]any, tools []any) any {
	kind := stringValue(choice["type"])
	switch kind {
	case "none":
		return "none"
	case "any":
		return "required"
	case "tool":
		name := stringValue(choice["name"])
		if name == "" {
			return "required"
		}
		for _, raw := range tools {
			tool := object(raw)
			if stringValue(tool["type"]) == "web_search_20250305" && stringValue(tool["name"]) == name {
				return map[string]any{"type": "web_search"}
			}
		}
		return map[string]any{"type": "function", "name": name}
	default:
		return "auto"
	}
}
func normalizeSchema(value any) any {
	switch current := value.(type) {
	case []any:
		result := make([]any, len(current))
		for i := range current {
			result[i] = normalizeSchema(current[i])
		}
		return result
	case map[string]any:
		result := map[string]any{}
		for key, item := range current {
			result[key] = normalizeSchema(item)
		}
		if properties := object(result["properties"]); len(properties) != 0 {
			keys := make([]string, 0, len(properties))
			for key := range properties {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			required := make([]any, len(keys))
			for i, key := range keys {
				required[i] = key
			}
			result["required"] = required
		}
		return result
	default:
		return value
	}
}

func object(value any) map[string]any {
	result, _ := value.(map[string]any)
	if result == nil {
		return map[string]any{}
	}
	return result
}
func array(value any) []any        { result, _ := value.([]any); return result }
func stringValue(value any) string { result, _ := value.(string); return result }
func boolean(value any) bool       { result, _ := value.(bool); return result }
func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
