package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	sse "github.com/tmaxmax/go-sse"
)

type SSEEvent struct{ Event, Data string }

func ParseSSE(input []byte) ([]SSEEvent, error) {
	var result []SSEEvent
	for event, err := range sse.Read(bytes.NewReader(input), &sse.ReadConfig{MaxEventSize: len(input) + 64*1024}) {
		if err != nil {
			return nil, fmt.Errorf("parse server-sent events: %w", err)
		}
		result = append(result, SSEEvent{Event: event.Type, Data: event.Data})
	}
	return result, nil
}
func EncodeSSE(event string, data any) []byte {
	encoded, _ := json.Marshal(data)
	return []byte("event: " + event + "\ndata: " + string(encoded) + "\n\n")
}

type blockState struct {
	Kind                 string
	Index                uint64
	ID, Name, Text, Args string
	Emitted, Buffered    bool
}

func ReduceCodexSSE(input []byte) ([]map[string]any, error) {
	events, err := ParseSSE(input)
	if err != nil {
		return nil, err
	}
	var output []map[string]any
	blocks := map[uint64]*blockState{}
	items := map[uint64]any{}
	itemIndices := map[string]uint64{}
	var anthropicIndex uint64
	sawTool, sawTerminal, incomplete, eligible := false, false, false, false
	terminalType, responseID := "", ""
	var usage map[string]any
	var searches uint64
	for _, event := range events {
		if event.Data == "" {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(event.Data), &payload) != nil {
			continue
		}
		kind := stringValue(payload["type"])
		if kind == "" {
			kind = event.Event
		}
		switch kind {
		case "codex.rate_limits":
			if boolean(pointer(payload, "rate_limits", "limit_reached")) {
				return nil, fmt.Errorf("rate limit reached")
			}
			output = append(output, map[string]any{"kind": "progress"})
		case "keepalive", "response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed":
			output = append(output, map[string]any{"kind": "progress"})
		case "response.failed", "response.error", "error":
			message := stringValue(pointer(payload, "response", "error", "message"))
			if message == "" {
				message = stringValue(pointer(payload, "error", "message"))
			}
			if message == "" {
				message = "Upstream error"
			}
			return nil, errors.New(message)
		case "response.output_item.added":
			item := object(payload["item"])
			index := uintValue(payload["output_index"])
			switch stringValue(item["type"]) {
			case "reasoning":
			case "web_search_call":
				output = append(output, map[string]any{"kind": "progress"})
			case "message":
				current := anthropicIndex
				anthropicIndex++
				if id := stringValue(item["id"]); id != "" {
					itemIndices[id] = index
				}
				blocks[index] = &blockState{Kind: "text", Index: current}
				output = append(output, map[string]any{"kind": "text-start", "index": current})
			case "function_call":
				sawTool = true
				current := anthropicIndex
				anthropicIndex++
				state := &blockState{Kind: "tool", Index: current, ID: stringValue(item["call_id"]), Name: stringValue(item["name"])}
				state.Buffered = state.Name == "Read"
				blocks[index] = state
				output = append(output, map[string]any{"kind": "tool-start", "index": current, "id": state.ID, "name": state.Name})
			}
		case "response.output_text.delta":
			index, ok := number(payload["output_index"])
			if !ok {
				if mapped, found := itemIndices[stringValue(payload["item_id"])]; found {
					index, ok = mapped, true
				}
			}
			state := blocks[index]
			if ok && state != nil && state.Kind == "text" {
				delta := stringValue(payload["delta"])
				if delta != "" {
					state.Text += delta
					output = append(output, map[string]any{"kind": "text-delta", "index": state.Index, "text": delta})
				}
			}
		case "response.function_call_arguments.delta":
			state := blocks[uintValue(payload["output_index"])]
			if state != nil && state.Kind == "tool" {
				delta := stringValue(payload["delta"])
				if delta != "" {
					state.Args += delta
					if state.Buffered {
						output = append(output, map[string]any{"kind": "tool-progress", "index": state.Index})
					} else {
						state.Emitted = true
						output = append(output, map[string]any{"kind": "tool-delta", "index": state.Index, "partialJson": delta})
					}
				}
			}
		case "response.function_call_arguments.done":
			state := blocks[uintValue(payload["output_index"])]
			if state != nil && state.Args == "" {
				state.Args = stringValue(payload["arguments"])
			}
		case "response.output_item.done":
			item := object(payload["item"])
			if stringValue(item["type"]) == "web_search_call" {
				index := anthropicIndex
				anthropicIndex += 2
				searches++
				output = append(output, map[string]any{"kind": "web-search", "index": index, "resultIndex": index + 1, "id": serverToolID(stringValue(item["id"])), "query": webSearchQuery(item)})
				continue
			}
			index := uintValue(payload["output_index"])
			state := blocks[index]
			if state == nil {
				continue
			}
			delete(blocks, index)
			if state.Kind == "text" {
				if state.Text != "" {
					items[index] = map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": state.Text}}}
				}
				output = append(output, map[string]any{"kind": "text-stop", "index": state.Index})
			} else {
				final := stringValue(item["arguments"])
				if final == "" {
					final = state.Args
				}
				final = sanitizeToolArgs(state.Name, final)
				state.Args = final
				if final != "" && (state.Buffered || !state.Emitted) {
					output = append(output, map[string]any{"kind": "tool-delta", "index": state.Index, "partialJson": final})
				}
				items[index] = map[string]any{"type": "function_call", "call_id": state.ID, "name": state.Name, "arguments": state.Args}
				output = append(output, map[string]any{"kind": "tool-stop", "index": state.Index})
			}
		case "response.completed", "response.incomplete", "response.done":
			sawTerminal = true
			terminalType = kind
			responseID = stringValue(pointer(payload, "response", "id"))
			usage = object(pointer(payload, "response", "usage"))
			if len(usage) == 0 {
				usage = nil
			}
			if kind == "response.incomplete" || pointer(payload, "response", "incomplete_details", "reason") != nil || stringValue(pointer(payload, "response", "status")) == "incomplete" {
				incomplete = true
			}
			eligible = (kind == "response.completed" || kind == "response.done") && !incomplete
		}
	}
	if !sawTerminal || len(blocks) != 0 {
		if sawTerminal {
			return nil, fmt.Errorf("upstream stream ended with open output blocks")
		}
		return nil, fmt.Errorf("upstream stream ended without a terminal response event")
	}
	reason := "end_turn"
	if incomplete {
		reason = "max_tokens"
	} else if sawTool {
		reason = "tool_use"
	}
	sorted := make([]int, 0, len(items))
	for index := range items {
		sorted = append(sorted, int(index))
	}
	sort.Ints(sorted)
	outputItems := make([]any, 0, len(sorted))
	for _, index := range sorted {
		outputItems = append(outputItems, items[uint64(index)])
	}
	finish := map[string]any{"kind": "finish", "stopReason": reason, "terminalType": terminalType, "continuationEligible": eligible, "usage": usage, "webSearchRequests": searches, "outputItems": outputItems}
	if responseID != "" {
		finish["responseId"] = responseID
	} else {
		finish["responseId"] = nil
	}
	output = append(output, finish)
	return output, nil
}

func AccumulateCodex(input []byte, messageID, model string) (map[string]any, error) {
	events, err := ReduceCodexSSE(input)
	if err != nil {
		return nil, err
	}
	blocks := map[uint64]map[string]any{}
	reason := ""
	var usage map[string]any
	var searches uint64
	for _, event := range events {
		index := uintValue(event["index"])
		switch stringValue(event["kind"]) {
		case "text-start":
			blocks[index] = map[string]any{"type": "text", "text": ""}
		case "text-delta":
			blocks[index]["text"] = stringValue(blocks[index]["text"]) + stringValue(event["text"])
		case "tool-start":
			blocks[index] = map[string]any{"type": "tool_use", "id": event["id"], "name": event["name"], "args": ""}
		case "tool-delta":
			blocks[index]["args"] = stringValue(blocks[index]["args"]) + stringValue(event["partialJson"])
		case "finish":
			reason = stringValue(event["stopReason"])
			usage = object(event["usage"])
			searches = uintValue(event["webSearchRequests"])
		}
	}
	indices := make([]int, 0, len(blocks))
	for index := range blocks {
		indices = append(indices, int(index))
	}
	sort.Ints(indices)
	content := []any{}
	for _, raw := range indices {
		block := blocks[uint64(raw)]
		if block["type"] == "text" {
			if stringValue(block["text"]) != "" {
				content = append(content, block)
			}
		} else {
			args := stringValue(block["args"])
			var parsed any = map[string]any{}
			if args != "" && json.Unmarshal([]byte(args), &parsed) != nil {
				parsed = map[string]any{"_raw": args}
			}
			delete(block, "args")
			block["input"] = parsed
			content = append(content, block)
		}
	}
	return map[string]any{"id": messageID, "type": "message", "role": "assistant", "model": model, "content": content, "stop_reason": reason, "stop_sequence": nil, "usage": anthropicUsage(usage, searches)}, nil
}

func CodexToAnthropicSSE(input []byte, messageID, model string) ([][]byte, error) {
	events, err := ReduceCodexSSE(input)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	started := false
	ensure := func() {
		if started {
			return
		}
		started = true
		out = append(out, EncodeSSE("message_start", messageStart{Type: "message_start", Message: messageValue{ID: messageID, Type: "message", Role: "assistant", Model: model, Content: []any{}, Usage: usageValue{}}}), EncodeSSE("ping", typeOnly{Type: "ping"}))
	}
	for _, event := range events {
		kind := stringValue(event["kind"])
		index := uintValue(event["index"])
		switch kind {
		case "text-start":
			ensure()
			out = append(out, EncodeSSE("content_block_start", blockStart{Type: "content_block_start", Index: index, ContentBlock: orderedBlock{Type: "text", Text: stringPointer("")}}))
		case "text-delta":
			out = append(out, EncodeSSE("content_block_delta", blockDelta{Type: "content_block_delta", Index: index, Delta: orderedDelta{Type: "text_delta", Text: stringValue(event["text"])}}))
		case "text-stop":
			out = append(out, EncodeSSE("content_block_stop", blockStop{Type: "content_block_stop", Index: index}))
		case "tool-start":
			ensure()
			out = append(out, EncodeSSE("content_block_start", blockStart{Type: "content_block_start", Index: index, ContentBlock: orderedBlock{Type: "tool_use", ID: stringValue(event["id"]), Name: stringValue(event["name"]), Input: map[string]any{}}}))
		case "tool-delta":
			out = append(out, EncodeSSE("content_block_delta", blockDelta{Type: "content_block_delta", Index: index, Delta: orderedDelta{Type: "input_json_delta", PartialJSON: stringValue(event["partialJson"])}}))
		case "tool-stop":
			out = append(out, EncodeSSE("content_block_stop", blockStop{Type: "content_block_stop", Index: index}))
		case "finish":
			ensure()
			out = append(out, EncodeSSE("message_delta", messageDelta{Type: "message_delta", Delta: stopDelta{StopReason: stringValue(event["stopReason"]), StopSequence: nil}, Usage: orderedUsage(object(event["usage"]), uintValue(event["webSearchRequests"]))}), EncodeSSE("message_stop", typeOnly{Type: "message_stop"}))
		}
	}
	return out, nil
}

type usageValue struct {
	InputTokens   uint64 `json:"input_tokens"`
	OutputTokens  uint64 `json:"output_tokens"`
	CacheCreation uint64 `json:"cache_creation_input_tokens"`
	CacheRead     uint64 `json:"cache_read_input_tokens"`
	ServerToolUse any    `json:"server_tool_use,omitempty"`
}
type messageValue struct {
	ID           string     `json:"id"`
	Type         string     `json:"type"`
	Role         string     `json:"role"`
	Model        string     `json:"model"`
	Content      []any      `json:"content"`
	StopReason   any        `json:"stop_reason"`
	StopSequence any        `json:"stop_sequence"`
	Usage        usageValue `json:"usage"`
}
type messageStart struct {
	Type    string       `json:"type"`
	Message messageValue `json:"message"`
}
type typeOnly struct {
	Type string `json:"type"`
}
type orderedBlock struct {
	Type  string  `json:"type"`
	ID    string  `json:"id,omitempty"`
	Name  string  `json:"name,omitempty"`
	Text  *string `json:"text,omitempty"`
	Input any     `json:"input,omitempty"`
}
type blockStart struct {
	Type         string       `json:"type"`
	Index        uint64       `json:"index"`
	ContentBlock orderedBlock `json:"content_block"`
}
type orderedDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}
type blockDelta struct {
	Type  string       `json:"type"`
	Index uint64       `json:"index"`
	Delta orderedDelta `json:"delta"`
}
type blockStop struct {
	Type  string `json:"type"`
	Index uint64 `json:"index"`
}
type stopDelta struct {
	StopReason   string `json:"stop_reason"`
	StopSequence any    `json:"stop_sequence"`
}
type messageDelta struct {
	Type  string     `json:"type"`
	Delta stopDelta  `json:"delta"`
	Usage usageValue `json:"usage"`
}

func orderedUsage(usage map[string]any, searches uint64) usageValue {
	input := uintValue(usage["input_tokens"])
	cached := uintValue(pointer(usage, "input_tokens_details", "cached_tokens"))
	if cached > input {
		cached = input
	}
	value := usageValue{InputTokens: input - cached, OutputTokens: uintValue(usage["output_tokens"]), CacheRead: cached}
	if searches > 0 {
		value.ServerToolUse = struct {
			WebSearchRequests uint64 `json:"web_search_requests"`
		}{searches}
	}
	return value
}
func stringPointer(value string) *string { return &value }

func anthropicUsage(usage map[string]any, searches uint64) map[string]any {
	input := uintValue(usage["input_tokens"])
	output := uintValue(usage["output_tokens"])
	cached := uintValue(pointer(usage, "input_tokens_details", "cached_tokens"))
	if cached > input {
		cached = input
	}
	result := map[string]any{"input_tokens": input - cached, "output_tokens": output, "cache_creation_input_tokens": uint64(0), "cache_read_input_tokens": cached}
	if searches > 0 {
		result["server_tool_use"] = map[string]any{"web_search_requests": searches}
	}
	return result
}
func pointer(value any, path ...string) any {
	current := value
	for _, key := range path {
		current = object(current)[key]
	}
	return current
}
func number(value any) (uint64, bool) {
	switch n := value.(type) {
	case float64:
		return uint64(n), true
	case uint64:
		return n, true
	case int:
		return uint64(n), true
	}
	return 0, false
}
func uintValue(value any) uint64 { result, _ := number(value); return result }
func serverToolID(id string) string {
	if id == "" {
		id = "unknown"
	}
	var out strings.Builder
	for _, r := range id {
		if r > 127 || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_') {
			out.WriteByte('_')
		} else {
			out.WriteRune(r)
		}
	}
	return "srvtoolu_" + out.String()
}
func webSearchQuery(item map[string]any) string {
	action := object(item["action"])
	if query := stringValue(action["query"]); query != "" {
		return query
	}
	for _, value := range array(action["queries"]) {
		if query := stringValue(value); query != "" {
			return query
		}
	}
	return ""
}
func sanitizeToolArgs(name, args string) string {
	if name != "Read" || args == "" {
		return args
	}
	var value map[string]any
	if json.Unmarshal([]byte(args), &value) != nil || stringValue(value["pages"]) != "" {
		return args
	}
	delete(value, "pages")
	encoded, err := json.Marshal(value)
	if err != nil {
		return args
	}
	return string(encoded)
}
