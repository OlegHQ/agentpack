package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/pelletier/go-toml/v2"
)

const credentialsFile = ".credentials.json"
const oauthLocksDirectory = "mcp-oauth-locks"

func oauthRoot(projectRoot string) (string, error) {
	state, err := paths.ProjectStateDir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "codex-mcp-oauth"), nil
}
func oauthCredentials(projectRoot string) (string, error) {
	root, err := oauthRoot(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, credentialsFile), nil
}
func oauthLocks(projectRoot string) (string, error) {
	root, err := oauthRoot(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, oauthLocksDirectory), nil
}

func prepareMCPAuth(projectRoot, staged string) error {
	credentials, err := oauthCredentials(projectRoot)
	if err != nil {
		return err
	}
	if _, err := os.Stat(credentials); os.IsNotExist(err) {
		if err := writeCredentialStore(credentials, map[string]any{}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if _, err := readCredentialStore(credentials); err != nil {
		return err
	}
	if err := base.LinkDurableFile(credentials, filepath.Join(staged, credentialsFile)); err != nil {
		return err
	}
	locks, err := oauthLocks(projectRoot)
	if err != nil {
		return err
	}
	if err := base.LinkDurableDirectory(locks, filepath.Join(staged, oauthLocksDirectory)); err != nil {
		return err
	}
	return updateConfig(filepath.Join(staged, "config.toml"), func(root map[string]any) { root["mcp_oauth_credentials_store"] = "file" })
}
func verifyMCPAuth(projectRoot, staged string) error {
	credentials, err := oauthCredentials(projectRoot)
	if err != nil {
		return err
	}
	if !base.DurablePathMatches(filepath.Join(staged, credentialsFile), credentials) {
		return fmt.Errorf("Codex MCP credential link does not resolve to %s", credentials)
	}
	locks, err := oauthLocks(projectRoot)
	if err != nil {
		return err
	}
	if !base.DurablePathMatches(filepath.Join(staged, oauthLocksDirectory), locks) {
		return fmt.Errorf("Codex MCP OAuth lock directory does not resolve to %s", locks)
	}
	root := make(map[string]any)
	data, err := os.ReadFile(filepath.Join(staged, "config.toml"))
	if err != nil {
		return err
	}
	if err := jsonOrToml(data, &root); err != nil {
		return err
	}
	if root["mcp_oauth_credentials_store"] != "file" {
		return fmt.Errorf("Codex staged config is not using the durable MCP OAuth file store")
	}
	return nil
}
func jsonOrToml(data []byte, target *map[string]any) error { return toml.Unmarshal(data, target) }

type credentialCandidate struct {
	modified time.Time
	path     string
}

func recoverMCPAuth(projectRoot, currentMode string) error {
	current, err := paths.StagingCodexHomeDirForMode(projectRoot, currentMode)
	if err != nil {
		return err
	}
	modes := filepath.Dir(filepath.Dir(current))
	durable, err := oauthCredentials(projectRoot)
	if err != nil {
		return err
	}
	candidates, err := credentialCandidates(modes, durable)
	if err != nil {
		return err
	}
	hasLegacy := false
	for _, candidate := range candidates {
		if candidate.path != durable {
			hasLegacy = true
		}
	}
	if !hasLegacy {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modified.Equal(candidates[j].modified) {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].modified.Before(candidates[j].modified)
	})
	merged := make(map[string]any)
	for _, candidate := range candidates {
		store, err := readCredentialStore(candidate.path)
		if err != nil {
			return err
		}
		for key, value := range store {
			merged[key] = value
		}
	}
	return writeCredentialStore(durable, merged)
}
func credentialCandidates(modes, durable string) ([]credentialCandidate, error) {
	var candidates []credentialCandidate
	if err := appendCredentialCandidate(&candidates, durable, durable); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(modes)
	if os.IsNotExist(err) {
		return candidates, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := appendCredentialCandidate(&candidates, filepath.Join(modes, entry.Name(), "codex-home", credentialsFile), durable); err != nil {
			return nil, err
		}
	}
	return candidates, nil
}
func appendCredentialCandidate(candidates *[]credentialCandidate, path, durable string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if path != durable && base.DurablePathMatches(path, durable) {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to recover unexpected Codex MCP credential symlink %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	*candidates = append(*candidates, credentialCandidate{info.ModTime(), path})
	return nil
}
func readCredentialStore(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	store := make(map[string]any)
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("invalid Codex MCP OAuth credential store %s: %w", path, err)
	}
	return store, nil
}
func writeCredentialStore(path string, store map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
