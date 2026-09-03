package proxy

import (
	"net/http"
	"testing"
)

func TestModelMapAndWebSocketWireHelpers(t *testing.T) {
	models := ModelMap{Big: "big", Middle: "mid", Small: "small"}
	tests := map[string]ProxyModel{"claude-opus-4-7": {Requested: "claude-opus-4-7", Upstream: "big"}, "claude-sonnet-4-6": {Requested: "claude-sonnet-4-6", Upstream: "mid"}, "claude-haiku-4-5": {Requested: "claude-haiku-4-5", Upstream: "small"}, "gpt-5.5-fast": {Requested: "gpt-5.5-fast", Upstream: "gpt-5.5", ServiceTier: "priority"}}
	for input, expected := range tests {
		if got := models.Resolve(input); got != expected {
			t.Fatalf("%s: %#v != %#v", input, got, expected)
		}
	}
	if got, err := WebSocketURL("https://chatgpt.com/backend-api/codex/responses"); err != nil || got != "wss://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("url=%s err=%v", got, err)
	}
	headers := http.Header{"Openai-Beta": []string{"responses=experimental"}, "Content-Length": []string{"123"}}
	headers = WebSocketHeaders(headers)
	if headers.Get("openai-beta") != WebSocketProtocolHeader || headers.Get("content-length") != "" {
		t.Fatalf("headers=%v", headers)
	}
	payload := WebSocketPayload(map[string]any{"model": "gpt-5.5", "stream": true, "input": []any{}})
	if string(payload) != "{\"input\":[],\"model\":\"gpt-5.5\",\"type\":\"response.create\"}" {
		t.Fatalf("payload=%s", payload)
	}
}

func TestDiagnosticsSnippetIsUTF8Safe(t *testing.T) {
	if got := Snippet("abčdef", 3); got != "ab..." {
		t.Fatalf("snippet=%q", got)
	}
}
