package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

// daemonHasNoLoadedThreads asks the running app server before retiring its
// home. A failed probe is never interpreted as an idle daemon.
func daemonHasNoLoadedThreads(socket string) (bool, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 3 * time.Second,
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
		},
	}
	connection, _, err := dialer.Dial("ws://localhost/", nil)
	if err != nil {
		return false, err
	}
	defer connection.Close()
	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return false, err
	}
	if err := connection.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return false, err
	}
	if err := connection.WriteJSON(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "agentpack", "version": "1"}}}); err != nil {
		return false, err
	}
	if _, err := readDaemonResponse(connection, 1); err != nil {
		return false, err
	}
	if err := connection.WriteJSON(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return false, err
	}
	if err := connection.WriteJSON(map[string]any{"id": 2, "method": "thread/loaded/list", "params": map[string]any{"limit": 1}}); err != nil {
		return false, err
	}
	result, err := readDaemonResponse(connection, 2)
	if err != nil {
		return false, err
	}
	var listed struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(result, &listed); err != nil {
		return false, err
	}
	return len(listed.Data) == 0, nil
}

func readDaemonResponse(connection *websocket.Conn, expected int) (json.RawMessage, error) {
	for {
		var response struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := connection.ReadJSON(&response); err != nil {
			return nil, err
		}
		if response.ID == nil || *response.ID != expected {
			continue
		}
		if len(response.Error) != 0 {
			return nil, fmt.Errorf("daemon request failed: %s", response.Error)
		}
		if len(response.Result) == 0 {
			return nil, fmt.Errorf("daemon returned no result")
		}
		return response.Result, nil
	}
}
