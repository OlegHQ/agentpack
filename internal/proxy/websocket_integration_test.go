package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketTransportSendsCreateAndCollectsFrames(t *testing.T) {
	var received map[string]any
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("openai-beta") != WebSocketProtocolHeader {
			t.Errorf("beta=%q", request.Header.Get("openai-beta"))
		}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		_, message, err := connection.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		_ = json.Unmarshal(message, &received)
		_ = connection.WriteJSON(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "item_1"}})
		_ = connection.WriteJSON(map[string]any{"type": "response.output_text.delta", "output_index": 0, "delta": "ok"})
		_ = connection.WriteJSON(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message"}})
		_ = connection.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}})
	}))
	defer upstream.Close()
	server, err := NewServer(Config{Transport: TransportWebSocket, ConnectTimeout: time.Second, WebSocketIdleTimeout: time.Second, Models: DefaultModelMap()}, &staticAuth{endpoint: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	body, err := server.callWebSocket(context.Background(), 1, AuthSnapshot{AccessToken: "token", EndpointURL: upstream.URL}, map[string]any{"model": "gpt-5.5", "stream": true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if received["type"] != "response.create" || received["stream"] != nil {
		t.Fatalf("payload=%#v", received)
	}
	if !strings.Contains(string(body), `"type":"response.completed"`) {
		t.Fatalf("body=%s", body)
	}
}
