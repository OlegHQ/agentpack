package proxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/zalando/go-keyring"
)

const codexEndpoint = "https://chatgpt.com/backend-api/codex/responses"
const openAIEndpoint = "https://api.openai.com/v1/responses"
const refreshEndpoint = "https://auth.openai.com/oauth/token"
const codexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

type AuthSnapshot struct{ AccessToken, AccountID, EndpointURL string }
type AuthManager interface {
	Snapshot() (AuthSnapshot, error)
	RefreshAfterUnauthorized() (bool, error)
}
type authSource uint8

const (
	apiKeySource authSource = iota
	chatGPTSource
)

type authState struct {
	Token, URL, AccountID, AuthFile, RefreshToken string
	Source                                        authSource
}
type UpstreamAuth struct {
	mu     sync.Mutex
	state  authState
	client *http.Client
}

func LoadUpstreamAuth() (*UpstreamAuth, error) {
	state, err := loadAuthState()
	if err != nil {
		return nil, err
	}
	if override := strings.TrimSpace(os.Getenv("AGENTPACK_PROXY_UPSTREAM_URL")); override != "" {
		state.URL = override
	}
	return &UpstreamAuth{state: state, client: http.DefaultClient}, nil
}
func (auth *UpstreamAuth) Snapshot() (AuthSnapshot, error) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	return AuthSnapshot{AccessToken: auth.state.Token, AccountID: auth.state.AccountID, EndpointURL: auth.state.URL}, nil
}
func (auth *UpstreamAuth) DefaultTransport() Transport {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if auth.state.Source == apiKeySource {
		return TransportHTTP
	}
	return TransportWebSocket
}
func (auth *UpstreamAuth) RefreshAfterUnauthorized() (bool, error) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if auth.state.Source != chatGPTSource || auth.state.RefreshToken == "" {
		return false, nil
	}
	body, _ := json.Marshal(map[string]any{"client_id": codexClientID, "grant_type": "refresh_token", "refresh_token": auth.state.RefreshToken})
	request, err := http.NewRequest(http.MethodPost, refreshEndpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	request.Header.Set("content-type", "application/json")
	response, err := auth.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("refresh Codex OAuth token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, nil
	}
	var value map[string]any
	if json.NewDecoder(response.Body).Decode(&value) != nil {
		return false, nil
	}
	access := stringValue(value["access_token"])
	if access == "" {
		return false, nil
	}
	auth.state.Token = access
	if refresh := stringValue(value["refresh_token"]); refresh != "" {
		auth.state.RefreshToken = refresh
	}
	return true, persistRefreshed(auth.state, stringValue(value["id_token"]), access)
}

func loadAuthState() (authState, error) {
	if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
		return authState{Token: key, URL: openAIEndpoint, Source: apiKeySource}, nil
	}
	path, err := resolveAuthJSON()
	if err != nil {
		return authState{}, fmt.Errorf("OPENAI_API_KEY is unset and no Codex auth.json is available for proxy auth: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return authState{}, err
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return authState{}, err
	}
	if token := stringValue(value["personal_access_token"]); strings.TrimSpace(token) != "" {
		return authState{Token: token, URL: codexEndpoint, AuthFile: path, Source: chatGPTSource}, nil
	}
	tokens := object(value["tokens"])
	if token := stringValue(tokens["access_token"]); strings.TrimSpace(token) != "" {
		return authState{Token: token, URL: codexEndpoint, AccountID: stringValue(tokens["account_id"]), AuthFile: path, RefreshToken: stringValue(tokens["refresh_token"]), Source: chatGPTSource}, nil
	}
	if key := stringValue(value["OPENAI_API_KEY"]); strings.TrimSpace(key) != "" {
		return authState{Token: key, URL: openAIEndpoint, AuthFile: path, Source: apiKeySource}, nil
	}
	return authState{}, fmt.Errorf("Codex auth file did not contain an API key, ChatGPT access token, or personal access token")
}
func resolveAuthJSON() (string, error) {
	if path := os.Getenv("AGENTPACK_PROXY_AUTH_JSON"); regular(path) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".codex", "auth.json")
		if regular(path) {
			return path, nil
		}
		account := codexKeyringAccount(filepath.Join(home, ".codex"))
		if raw, keyErr := keyring.Get("Codex Auth", account); keyErr == nil {
			var value any
			if json.Unmarshal([]byte(raw), &value) == nil {
				shared, _ := paths.SharedCodexAuthPath()
				if writeAtomicJSON(shared, value) == nil {
					return shared, nil
				}
			}
		}
	}
	shared, err := paths.SharedCodexAuthPath()
	if err == nil && regular(shared) {
		return shared, nil
	}
	return "", os.ErrNotExist
}
func codexKeyringAccount(home string) string {
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		canonical = home
	}
	sum := sha256.Sum256([]byte(canonical))
	return "cli|" + hex.EncodeToString(sum[:])[:16]
}
func persistRefreshed(state authState, idToken, access string) error {
	if state.AuthFile == "" {
		return nil
	}
	data, _ := os.ReadFile(state.AuthFile)
	value := map[string]any{}
	_ = json.Unmarshal(data, &value)
	tokens := object(value["tokens"])
	tokens["access_token"] = access
	if state.RefreshToken != "" {
		tokens["refresh_token"] = state.RefreshToken
	}
	if state.AccountID != "" {
		tokens["account_id"] = state.AccountID
	}
	if idToken != "" {
		tokens["id_token"] = map[string]any{"raw_jwt": idToken}
	}
	value["tokens"] = tokens
	value["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	return writeAtomicJSON(state.AuthFile, value)
}
func writeAtomicJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(path), fmt.Sprintf(".auth.json.tmp.%d", os.Getpid()))
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
func regular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
