package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/OlegHQ/agentpack/internal/paths"
)

type Server struct {
	config      Config
	auth        AuthManager
	diagnostics *Diagnostics
	listener    net.Listener
	http        *http.Server
	client      *http.Client
	counter     atomic.Uint64
}

func NewServer(config Config, auth AuthManager) (*Server, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", config.Port))
	if err != nil {
		return nil, err
	}
	diagnostics, err := NewDiagnostics(config.Diagnostics)
	if err != nil {
		listener.Close()
		return nil, err
	}
	server := &Server{config: config, auth: auth, diagnostics: diagnostics, listener: listener, client: &http.Client{Timeout: config.RequestTimeout}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", server.handle)
	server.http = &http.Server{Handler: mux}
	diagnostics.Event("proxy_start", map[string]any{"bind_addr": listener.Addr().String(), "transport": config.Transport, "request_timeout_ms": config.RequestTimeout.Milliseconds(), "connect_timeout_ms": config.ConnectTimeout.Milliseconds(), "websocket_idle_timeout_ms": config.WebSocketIdleTimeout.Milliseconds()})
	return server, nil
}
func (server *Server) BaseURL() string { return "http://" + server.listener.Addr().String() }
func (server *Server) Run() error {
	err := server.http.Serve(server.listener)
	if err == http.ErrServerClosed {
		err = nil
	}
	server.diagnostics.Event("proxy_stop", map[string]any{"reason": "accept_loop_exit"})
	server.diagnostics.Close()
	return err
}
func (server *Server) Start() <-chan error {
	done := make(chan error, 1)
	go func() { done <- server.Run(); close(done) }()
	return done
}
func (server *Server) Shutdown(ctx context.Context) error { return server.http.Shutdown(ctx) }
func (server *Server) handle(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	if path == "/__agentpack/shutdown" {
		writeJSONResponse(writer, 200, map[string]any{"ok": true})
		go server.Shutdown(context.Background())
		return
	}
	if path == "/health" || path == "/healthz" {
		snapshot, _ := server.auth.Snapshot()
		writeJSONResponse(writer, 200, map[string]any{"ok": true, "status": "healthy", "upstream": snapshot.EndpointURL})
		return
	}
	requestID := server.counter.Add(1)
	if !server.authorized(request) {
		server.diagnostics.Event("request_rejected", map[string]any{"request_id": requestID, "path": path, "reason": "invalid_proxy_token"})
		writeError(writer, 401, "authentication_error", "invalid proxy token")
		return
	}
	switch {
	case request.Method == http.MethodGet && path == "/v1/models":
		writeJSONResponse(writer, 200, server.config.Models.List())
	case request.Method == http.MethodPost && path == "/v1/messages/count_tokens":
		body, err := readRequest(request)
		if err != nil {
			writeError(writer, 400, "invalid_request_error", err.Error())
			return
		}
		writeJSONResponse(writer, 200, map[string]any{"input_tokens": countTokens(body)})
	case request.Method == http.MethodPost && path == "/v1/messages":
		server.messages(writer, request, requestID)
	default:
		writeError(writer, 404, "not_found_error", "unknown proxy endpoint")
	}
}
func (server *Server) authorized(request *http.Request) bool {
	if server.config.ClientToken == "" {
		return true
	}
	bearer, hasBearer := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	return (hasBearer && bearer == server.config.ClientToken) || request.Header.Get("x-api-key") == server.config.ClientToken
}
func (server *Server) messages(writer http.ResponseWriter, request *http.Request, requestID uint64) {
	body, err := readRequest(request)
	if err != nil {
		writeError(writer, 400, "invalid_request_error", err.Error())
		return
	}
	requestedModel := stringValue(body["model"])
	stream, _ := body["stream"].(bool)
	bytes, err := server.callUpstream(request.Context(), requestID, body, request.Header.Get("x-claude-code-session-id"))
	if err != nil {
		writeError(writer, 502, "api_error", err.Error())
		return
	}
	messageID := fmt.Sprintf("msg_agentpack_%d_%d", time.Now().UnixMilli(), server.counter.Add(1))
	if stream {
		chunks, err := CodexToAnthropicSSE(bytes, messageID, requestedModel)
		if err != nil {
			writeError(writer, 502, "api_error", err.Error())
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Cache-Control", "no-cache")
		writer.WriteHeader(200)
		flusher, _ := writer.(http.Flusher)
		for _, chunk := range chunks {
			_, _ = writer.Write(chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
		return
	}
	response, err := AccumulateCodex(bytes, messageID, requestedModel)
	if err != nil {
		writeError(writer, 502, "api_error", err.Error())
		return
	}
	writeJSONResponse(writer, 200, response)
}
func (server *Server) callUpstream(ctx context.Context, requestID uint64, anthropic map[string]any, sessionID string) ([]byte, error) {
	snapshot, err := server.auth.Snapshot()
	if err != nil {
		return nil, err
	}
	requested := server.config.Models.Resolve(stringValue(anthropic["model"]))
	copy := cloneMap(anthropic)
	copy["model"], copy["stream"] = requested.Upstream, true
	payload, err := TranslateAnthropic(copy, TranslateOptions{SessionID: sessionID, ServiceTier: requested.ServiceTier})
	if err != nil {
		return nil, err
	}
	server.diagnostics.Event("upstream_request", map[string]any{"request_id": requestID, "requested_model": requested.Requested, "upstream_model": requested.Upstream, "service_tier": requested.ServiceTier, "transport": server.config.Transport})
	if server.config.Transport == TransportWebSocket {
		return server.callWebSocket(ctx, requestID, snapshot, payload, sessionID)
	}
	if server.config.Transport == TransportAuto {
		bytes, wsErr := server.callWebSocket(ctx, requestID, snapshot, payload, sessionID)
		if wsErr == nil {
			return bytes, nil
		}
		server.diagnostics.Event("transport_fallback", map[string]any{"request_id": requestID, "from": "websocket", "to": "http", "error": wsErr.Error()})
	}
	return server.callHTTP(ctx, requestID, snapshot, payload, sessionID)
}
func (server *Server) callHTTP(ctx context.Context, requestID uint64, snapshot AuthSnapshot, payload map[string]any, sessionID string) ([]byte, error) {
	response, err := server.sendHTTP(ctx, snapshot, payload, sessionID)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == 401 {
		response.Body.Close()
		if refreshed, refreshErr := server.auth.RefreshAfterUnauthorized(); refreshErr != nil {
			return nil, refreshErr
		} else if refreshed {
			snapshot, _ = server.auth.Snapshot()
			response, err = server.sendHTTP(ctx, snapshot, payload, sessionID)
			if err != nil {
				return nil, err
			}
		}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream Codex request failed: %s; body=%s", response.Status, Snippet(string(body), 1000))
	}
	server.diagnostics.Event("http_complete", map[string]any{"request_id": requestID, "bytes": len(body)})
	return body, nil
}
func (server *Server) sendHTTP(ctx context.Context, snapshot AuthSnapshot, payload map[string]any, sessionID string) (*http.Response, error) {
	data, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, snapshot.EndpointURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	setCodexHeaders(request.Header, snapshot, sessionID)
	return server.client.Do(request)
}
func setCodexHeaders(headers http.Header, snapshot AuthSnapshot, sessionID string) {
	headers.Set("content-type", "application/json")
	headers.Set("accept", "text/event-stream")
	headers.Set("authorization", "Bearer "+snapshot.AccessToken)
	headers.Set("originator", "claude-code-proxy")
	headers.Set("openai-beta", "responses=experimental")
	headers.Set("user-agent", "agentpack-claude-proxy")
	if snapshot.AccountID != "" {
		headers.Set("chatgpt-account-id", snapshot.AccountID)
	}
	if sessionID != "" {
		headers.Set("session_id", sessionID)
		headers.Set("x-client-request-id", sessionID)
		headers.Set("x-codex-window-id", sessionID+":0")
	}
}
func readRequest(request *http.Request) (map[string]any, error) {
	defer request.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("parse Anthropic request JSON: %w", err)
	}
	return body, nil
}
func countTokens(body map[string]any) int {
	chars := 0
	if data, err := json.Marshal(body["system"]); err == nil {
		chars += len(data)
	}
	for _, message := range array(body["messages"]) {
		if data, err := json.Marshal(object(message)["content"]); err == nil {
			chars += len(data)
		}
	}
	count := chars / 4
	if count < 1 {
		count = 1
	}
	return count
}
func cloneMap(value map[string]any) map[string]any {
	data, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result
}
func writeJSONResponse(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
func writeError(writer http.ResponseWriter, status int, kind, message string) {
	writeJSONResponse(writer, status, map[string]any{"type": "error", "error": map[string]any{"type": kind, "message": message}})
}

type Running struct {
	Server *Server
	Done   <-chan error
	Token  string
}

func Start(projectRoot string) (*Running, error) {
	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	token := "agentpack-proxy-" + hex.EncodeToString(tokenBytes)
	auth, err := LoadUpstreamAuth()
	if err != nil {
		return nil, err
	}
	config := ConfigFromEnvironment(token)
	if _, set := os.LookupEnv("AGENTPACK_PROXY_TRANSPORT"); !set {
		config.Transport = auth.DefaultTransport()
	}
	logDir, err := paths.ProxyLogDir(projectRoot)
	if err != nil {
		return nil, err
	}
	config.Diagnostics.LogDirectory = logDir
	server, err := NewServer(config, auth)
	if err != nil {
		return nil, err
	}
	return &Running{Server: server, Done: server.Start(), Token: token}, nil
}
func (running *Running) Apply(command *exec.Cmd) {
	environment := command.Env
	if environment == nil {
		environment = os.Environ()
	}
	environment = withoutEnvironment(environment, "ANTHROPIC_API_KEY")
	environment = append(environment, "ANTHROPIC_BASE_URL="+running.Server.BaseURL(), "ANTHROPIC_AUTH_TOKEN="+running.Token, "ANTHROPIC_DEFAULT_OPUS_MODEL=claude-opus-4-7", "ANTHROPIC_DEFAULT_SONNET_MODEL=claude-sonnet-4-6", "ANTHROPIC_DEFAULT_HAIKU_MODEL=claude-haiku-4-5", "ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-4-5", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1")
	command.Env = environment
}
func (running *Running) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = running.Server.Shutdown(ctx)
	<-running.Done
}
func withoutEnvironment(values []string, key string) []string {
	prefix := key + "="
	result := values[:0]
	for _, value := range values {
		if !strings.HasPrefix(value, prefix) {
			result = append(result, value)
		}
	}
	return result
}
