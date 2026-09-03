package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const WebSocketProtocolHeader = "responses_websockets=2026-02-06"

type WebSocketSetupError struct {
	Message          string
	Status           int
	Code, RetryAfter string
	RequestSent      bool
}

func (err *WebSocketSetupError) Error() string { return err.Message }

func WebSocketURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported Codex WebSocket URL scheme: %s", parsed.Scheme)
	}
	return parsed.String(), nil
}
func WebSocketHeaders(input http.Header) http.Header {
	output := input.Clone()
	output.Set("openai-beta", WebSocketProtocolHeader)
	output.Del("content-length")
	return output
}
func WebSocketPayload(body map[string]any) []byte {
	value := cloneMap(body)
	delete(value, "stream")
	value["type"] = "response.create"
	data, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"type":"response.create"}`)
	}
	return data
}

func (server *Server) callWebSocket(ctx context.Context, requestID uint64, snapshot AuthSnapshot, payload map[string]any, sessionID string) ([]byte, error) {
	rawURL, err := WebSocketURL(snapshot.EndpointURL)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	setCodexHeaders(headers, snapshot, sessionID)
	headers = WebSocketHeaders(headers)
	dialer := websocket.Dialer{HandshakeTimeout: server.config.ConnectTimeout}
	connection, response, err := dialer.DialContext(ctx, rawURL, headers)
	if err != nil {
		setup := &WebSocketSetupError{Message: "connect Codex WebSocket: " + err.Error()}
		if response != nil {
			setup.Status = response.StatusCode
			setup.RetryAfter = response.Header.Get("retry-after")
			setup.Message = fmt.Sprintf("Codex WebSocket handshake failed: HTTP %d", response.StatusCode)
		}
		if (setup.Status == 401 || setup.Status == 403) && server.auth != nil {
			if refreshed, refreshErr := server.auth.RefreshAfterUnauthorized(); refreshErr != nil {
				return nil, refreshErr
			} else if refreshed {
				renewed, _ := server.auth.Snapshot()
				return server.callWebSocketOnce(ctx, requestID, renewed, payload, sessionID)
			}
		}
		return nil, setup
	}
	return server.readWebSocket(connection, requestID, payload)
}
func (server *Server) callWebSocketOnce(ctx context.Context, requestID uint64, snapshot AuthSnapshot, payload map[string]any, sessionID string) ([]byte, error) {
	rawURL, err := WebSocketURL(snapshot.EndpointURL)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	setCodexHeaders(headers, snapshot, sessionID)
	connection, response, err := (&websocket.Dialer{HandshakeTimeout: server.config.ConnectTimeout}).DialContext(ctx, rawURL, WebSocketHeaders(headers))
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		return nil, &WebSocketSetupError{Message: err.Error(), Status: status}
	}
	defer connection.Close()
	return server.readWebSocket(connection, requestID, payload)
}
func (server *Server) readWebSocket(connection *websocket.Conn, requestID uint64, payload map[string]any) ([]byte, error) {
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(server.config.WebSocketIdleTimeout))
	if err := connection.WriteMessage(websocket.TextMessage, WebSocketPayload(payload)); err != nil {
		return nil, fmt.Errorf("send Codex WebSocket response.create: %w", err)
	}
	var output []byte
	for {
		kind, message, err := connection.ReadMessage()
		if err != nil {
			if len(output) == 0 {
				return nil, &WebSocketSetupError{Message: "Codex WebSocket closed before terminal event: " + err.Error(), RequestSent: true}
			}
			break
		}
		if kind != websocket.TextMessage {
			return nil, fmt.Errorf("unexpected binary Codex WebSocket frame")
		}
		text := string(message)
		if len(output) == 0 {
			if setup := setupError(text, true); setup != nil {
				return nil, setup
			}
		}
		for _, line := range strings.Split(text, "\n") {
			output = append(output, []byte("data: "+line+"\n")...)
		}
		output = append(output, '\n')
		var value map[string]any
		_ = json.Unmarshal(message, &value)
		if oneOf(stringValue(value["type"]), "response.completed", "response.failed", "response.incomplete", "response.done", "error") {
			break
		}
	}
	server.diagnostics.Event("websocket_complete", map[string]any{"request_id": requestID, "bytes": len(output)})
	return output, nil
}
func setupError(text string, sent bool) *WebSocketSetupError {
	var value map[string]any
	if json.Unmarshal([]byte(text), &value) != nil {
		return nil
	}
	status := int(uintValue(value["status"]))
	if status == 0 {
		status = int(uintValue(value["status_code"]))
	}
	code := stringValue(pointer(value, "error", "code"))
	if status != 401 && status != 403 && status != 429 && code != "previous_response_not_found" {
		return nil
	}
	message := stringValue(pointer(value, "error", "message"))
	if message == "" {
		message = code
	}
	if message == "" {
		message = "Codex WebSocket setup error"
	}
	return &WebSocketSetupError{Message: message, Status: status, Code: code, RetryAfter: stringValue(pointer(value, "headers", "retry-after")), RequestSent: sent}
}
