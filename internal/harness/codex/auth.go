package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/pelletier/go-toml/v2"
	keyring "github.com/zalando/go-keyring"
)

const authKeyringService = "Codex Auth"

func keyringAccount(codexHome string) string {
	canonical, err := filepath.EvalSymlinks(codexHome)
	if err != nil {
		canonical = codexHome
	}
	sum := sha256.Sum256([]byte(canonical))
	return "cli|" + hex.EncodeToString(sum[:8])
}

func materializeAuthFromKeyring(userHome, destination string) (bool, error) {
	value, err := keyring.Get(authKeyringService, keyringAccount(userHome))
	if err == keyring.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, nil
	}
	var parsed any
	if json.Unmarshal([]byte(value), &parsed) != nil {
		return false, nil
	}
	return true, atomicWriteAuth(destination, []byte(value))
}
func atomicWriteAuth(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
func sharedAuthSource(userHome string) (string, error) {
	user := filepath.Join(userHome, "auth.json")
	if info, err := os.Stat(user); err == nil && info.Mode().IsRegular() {
		return user, nil
	}
	shared, err := paths.SharedCodexAuthPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		return "", err
	}
	if info, err := os.Stat(shared); err == nil && info.Mode().IsRegular() {
		return shared, nil
	}
	_, err = materializeAuthFromKeyring(userHome, shared)
	return shared, err
}
func prepareAuth(userHome, staged string) error {
	source, err := sharedAuthSource(userHome)
	if err != nil {
		return err
	}
	destination := filepath.Join(staged, "auth.json")
	_ = os.Remove(destination)
	target := source
	if parent, err := filepath.EvalSymlinks(filepath.Dir(source)); err == nil {
		target = filepath.Join(parent, filepath.Base(source))
	}
	if err := os.Symlink(target, destination); err != nil {
		if _, statErr := os.Stat(target); statErr == nil {
			return os.Link(target, destination)
		}
		return nil
	}
	return nil
}
func preserveAuth(staged string) error {
	shared, err := paths.SharedCodexAuthPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(shared); err == nil {
		return nil
	}
	path := filepath.Join(staged, "auth.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var value any
	if json.Unmarshal(data, &value) != nil {
		return nil
	}
	return atomicWriteAuth(shared, data)
}
func forceAuthFileStore(staged string) error {
	return updateConfig(filepath.Join(staged, "config.toml"), func(root map[string]any) { root["cli_auth_credentials_store"] = "file" })
}
func updateConfig(path string, mutate func(map[string]any)) error {
	root := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	mutate(root)
	data, err := toml.Marshal(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
