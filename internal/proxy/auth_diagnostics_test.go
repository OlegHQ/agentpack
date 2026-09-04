package proxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthPrefersEnvironmentAndLoadsCodexTokens(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", " env-key ")
	state, err := loadAuthState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Token != "env-key" || state.URL != openAIEndpoint || state.Source != apiKeySource {
		t.Fatalf("state=%#v", state)
	}
	t.Setenv("OPENAI_API_KEY", "")
	root := t.TempDir()
	authPath := filepath.Join(root, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"access","refresh_token":"refresh","account_id":"acct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTPACK_PROXY_AUTH_JSON", authPath)
	state, err = loadAuthState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Token != "access" || state.RefreshToken != "refresh" || state.AccountID != "acct" || state.URL != codexEndpoint {
		t.Fatalf("state=%#v", state)
	}
}

func TestDiagnosticsWritesJSONLAndLatest(t *testing.T) {
	root := t.TempDir()
	diagnostics, err := NewDiagnostics(DiagnosticsConfig{LogDirectory: root, MaxBodyBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	diagnostics.Event("test", map[string]any{"request_id": 1})
	diagnostics.Close()
	latest, err := os.ReadFile(filepath.Join(root, "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(latest, &metadata); err != nil {
		t.Fatal(err)
	}
	path := metadata.Path
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"kind":"test"`) {
		t.Fatalf("log=%s", data)
	}
}
