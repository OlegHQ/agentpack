package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type staticAuth struct {
	endpoint  string
	refreshed atomic.Bool
}

func (auth *staticAuth) Snapshot() (AuthSnapshot, error) {
	return AuthSnapshot{AccessToken: "upstream-token", AccountID: "acct_1", EndpointURL: auth.endpoint}, nil
}
func (auth *staticAuth) RefreshAfterUnauthorized() (bool, error) {
	auth.refreshed.Store(true)
	return true, nil
}

func TestServerTranslatesAndReducesMessages(t *testing.T) {
	var upstreamBody map[string]any
	fixture, err := os.ReadFile("testdata/codex_text.sse")
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/responses" {
			t.Errorf("path=%s", request.URL.Path)
		}
		if request.Header.Get("chatgpt-account-id") != "acct_1" {
			t.Errorf("account header missing")
		}
		_ = json.NewDecoder(request.Body).Decode(&upstreamBody)
		writer.Header().Set("content-type", "text/event-stream")
		_, _ = writer.Write(fixture)
	}))
	defer upstream.Close()
	config := Config{ClientToken: "client-token", Transport: TransportHTTP, RequestTimeout: 30 * time.Second, Models: DefaultModelMap(), Diagnostics: DiagnosticsConfig{}}
	server, err := NewServer(config, &staticAuth{endpoint: upstream.URL + "/responses"})
	if err != nil {
		t.Fatal(err)
	}
	done := server.Start()
	defer func() { _ = server.Shutdown(context.Background()); <-done }()
	request, _ := http.NewRequest(http.MethodPost, server.BaseURL()+"/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"max_tokens":10}`))
	request.Header.Set("authorization", "Bearer client-token")
	request.Header.Set("content-type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 {
		t.Fatalf("status=%s body=%s", response.Status, body)
	}
	var value map[string]any
	_ = json.Unmarshal(body, &value)
	if stringValue(object(array(value["content"])[0])["text"]) != "Hello world" {
		t.Fatalf("body=%s", body)
	}
	if stringValue(upstreamBody["model"]) != "gpt-5.4" || !boolean(upstreamBody["stream"]) {
		t.Fatalf("upstream=%#v", upstreamBody)
	}
}

func TestServerRejectsInvalidClientToken(t *testing.T) {
	config := Config{ClientToken: "right", Transport: TransportHTTP, RequestTimeout: time.Second, Models: DefaultModelMap()}
	server, err := NewServer(config, &staticAuth{endpoint: "http://127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	done := server.Start()
	defer func() { _ = server.Shutdown(context.Background()); <-done }()
	request, _ := http.NewRequest(http.MethodPost, server.BaseURL()+"/v1/messages/count_tokens", strings.NewReader(`{"messages":[]}`))
	request.Header.Set("authorization", "Bearer wrong")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

func TestRunningAppliesClaudeEnvironmentWithoutAPIKey(t *testing.T) {
	server, err := NewServer(Config{Models: DefaultModelMap()}, &staticAuth{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	running := &Running{Server: server, Token: "proxy-token"}
	command := exec.Command("claude")
	command.Env = []string{"ANTHROPIC_API_KEY=old"}
	running.Apply(command)
	joined := strings.Join(command.Env, "\n")
	if strings.Contains(joined, "ANTHROPIC_API_KEY=") || !strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=proxy-token") {
		t.Fatalf("env=%v", command.Env)
	}
}
