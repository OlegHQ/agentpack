package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"filippo.io/age"
	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/pelletier/go-toml/v2"
	keyring "github.com/zalando/go-keyring"
)

const authKeyringService = "Codex Auth"
const authLogoutMarker = "logged-out"
const encryptedAuthKeyringService = "codex"

func codexKeyringTarget(service, account string) string {
	return account + "." + service
}

func decodeCodexWindowsPassword(blob []byte) (string, error) {
	if len(blob)%2 != 0 {
		return "", fmt.Errorf("invalid Codex Windows keyring password length")
	}
	units := make([]uint16, len(blob)/2)
	for index := range units {
		units[index] = binary.LittleEndian.Uint16(blob[index*2:])
	}
	return string(utf16.Decode(units)), nil
}

func keyringAccount(codexHome string) string {
	canonical, err := filepath.EvalSymlinks(codexHome)
	if err != nil {
		canonical = codexHome
	}
	sum := sha256.Sum256([]byte(canonical))
	return "cli|" + hex.EncodeToString(sum[:8])
}

func materializeAuthFromKeyring(userHome, destination string) (bool, error) {
	value, err := readCodexKeyring(authKeyringService, keyringAccount(userHome))
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

func materializeEncryptedAuth(userHome, destination string) (bool, error) {
	path := filepath.Join(userHome, "secrets", "codex_auth.age")
	ciphertext, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	account := "secrets|" + strings.TrimPrefix(keyringAccount(userHome), "cli|")
	passphrase, err := readCodexKeyring(encryptedAuthKeyringService, account)
	if err == keyring.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Codex encrypted auth key: %w", err)
	}
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return false, fmt.Errorf("create Codex auth decryptor: %w", err)
	}
	plaintext, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		return false, fmt.Errorf("decrypt Codex auth store: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(plaintext, 8<<20))
	if err != nil {
		return false, fmt.Errorf("read Codex auth store: %w", err)
	}
	var store struct {
		Version int               `json:"version"`
		Secrets map[string]string `json:"secrets"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return false, fmt.Errorf("decode Codex auth store: %w", err)
	}
	if store.Version > 1 {
		return false, fmt.Errorf("unsupported Codex auth store version %d", store.Version)
	}
	auth := []byte(store.Secrets["global/CODEX_AUTH"])
	if len(auth) == 0 {
		return false, nil
	}
	if !validAuthData(auth) {
		return false, fmt.Errorf("invalid Codex auth in encrypted store")
	}
	return true, atomicWriteAuth(destination, auth)
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
		shared, err := paths.SharedCodexAuthPath()
		if err != nil {
			return "", err
		}
		if err := os.Remove(filepath.Join(filepath.Dir(shared), authLogoutMarker)); err != nil && !os.IsNotExist(err) {
			return "", err
		}
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
		if err := os.Remove(filepath.Join(filepath.Dir(shared), authLogoutMarker)); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return shared, nil
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(shared), authLogoutMarker)); err == nil {
		return shared, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if imported, err := materializeEncryptedAuth(userHome, shared); imported || err != nil {
		return shared, err
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
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	target := canonicalAuthTarget(source)
	if err := os.Symlink(target, destination); err != nil {
		if _, statErr := os.Stat(target); statErr == nil {
			if linkErr := os.Link(target, destination); linkErr == nil {
				return nil
			}
		}
		return fmt.Errorf("link staged Codex auth to %s: %w", target, err)
	}
	return nil
}

func canonicalAuthTarget(path string) string {
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(parent, filepath.Base(path))
	}
	return path
}

func prepareIsolatedLoginHome(staged string) (string, error) {
	loginHome, err := os.MkdirTemp(filepath.Dir(staged), ".codex-login-")
	if err != nil {
		return "", err
	}
	config, err := os.ReadFile(filepath.Join(staged, "config.toml"))
	if err == nil {
		err = os.WriteFile(filepath.Join(loginHome, "config.toml"), config, 0o600)
	}
	if err != nil {
		_ = os.RemoveAll(loginHome)
		return "", err
	}
	return loginHome, nil
}

func finishIsolatedLoginHome(loginHome, staged string) error {
	loginAuth := filepath.Join(loginHome, "auth.json")
	data, err := os.ReadFile(loginAuth)
	fromKeyring := false
	if os.IsNotExist(err) {
		if fromKeyring, err = materializeAuthFromKeyring(loginHome, loginAuth); err != nil {
			return err
		}
		data, err = os.ReadFile(loginAuth)
	}
	if os.IsNotExist(err) {
		return os.RemoveAll(loginHome) // failed login leaves the old credential untouched
	}
	if err != nil {
		return err
	}
	if !validAuthData(data) {
		return fmt.Errorf("Codex login wrote invalid auth in %s; left it for recovery", loginHome)
	}
	native, ok := nativeHome()
	if !ok {
		return fmt.Errorf("cannot find native Codex home to persist login from %s", loginHome)
	}
	destination, err := sharedAuthSource(native)
	if err != nil {
		return err
	}
	shared, err := paths.SharedCodexAuthPath()
	if err != nil {
		return err
	}
	if err := installAuthData(destination, shared, data); err != nil {
		return fmt.Errorf("persist Codex login from %s: %w", loginHome, err)
	}
	if err := prepareAuth(native, staged); err != nil {
		return err
	}
	if fromKeyring {
		if err := deleteCodexKeyring(authKeyringService, keyringAccount(loginHome)); err != nil && err != keyring.ErrNotFound {
			return fmt.Errorf("remove isolated Codex keyring login: %w", err)
		}
	}
	return os.RemoveAll(loginHome)
}

func installAuthData(destination, shared string, data []byte) error {
	if existing, err := os.ReadFile(destination); err == nil {
		if string(existing) != string(data) {
			backup := filepath.Join(filepath.Dir(shared), "auth.json.previous")
			if err := atomicWriteAuth(backup, existing); err != nil {
				return fmt.Errorf("back up previous Codex auth: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return atomicWriteAuth(destination, data)
}

func preserveAuth(staged string) error {
	shared, err := paths.SharedCodexAuthPath()
	if err != nil {
		return err
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
	if !validAuthData(data) {
		return nil
	}
	destination := shared
	if native, ok := nativeHome(); ok {
		userAuth := filepath.Join(native, "auth.json")
		if userInfo, err := os.Stat(userAuth); err == nil && userInfo.Mode().IsRegular() {
			destination = userAuth
		}
	}
	if existing, err := os.Stat(destination); err == nil {
		if !info.ModTime().After(existing.ModTime()) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return installAuthData(destination, shared, data)
}

func validAuthData(data []byte) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(data, &value) != nil || len(value) == 0 {
		return false
	}
	for _, field := range []string{"OPENAI_API_KEY", "tokens", "agent_identity", "personal_access_token", "bedrock_api_key", "bedrock_access_keys"} {
		if raw, ok := value[field]; ok && string(raw) != "null" {
			return true
		}
	}
	return false
}

func verifyAuth(staged string) error {
	path := filepath.Join(staged, "auth.json")
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("codex staged auth link is missing: %w", err)
	}
	shared, err := paths.SharedCodexAuthPath()
	if err != nil {
		return err
	}
	if base.DurablePathMatches(path, shared) {
		return nil
	}
	if native, ok := nativeHome(); ok && base.DurablePathMatches(path, filepath.Join(native, "auth.json")) {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(staged, target)
		}
		for _, accepted := range []string{shared, canonicalAuthTarget(shared)} {
			if filepath.Clean(target) == filepath.Clean(accepted) {
				return nil // shared store has not been created by a first login yet
			}
		}
		if native, ok := nativeHome(); ok {
			path := filepath.Join(native, "auth.json")
			for _, accepted := range []string{path, canonicalAuthTarget(path)} {
				if filepath.Clean(target) == filepath.Clean(accepted) {
					return nil // native store has not been created by a first login yet
				}
			}
		}
		return fmt.Errorf("codex staged auth link target %q does not match durable credentials", target)
	}
	return fmt.Errorf("codex staged auth is no longer linked to durable credentials")
}

func finishAuthLaunch(ctxHome string, arguments []string) error {
	if _, err := os.Stat(filepath.Join(ctxHome, "config.toml")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	authPath := filepath.Join(ctxHome, "auth.json")
	if info, err := os.Lstat(authPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("codex staged auth is not a regular file or link")
		}
		if err := preserveAuth(ctxHome); err != nil {
			return err
		}
		if native, ok := nativeHome(); ok {
			return prepareAuth(native, ctxHome)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if !usesInteractiveCodex(arguments) && !isCodexLogout(arguments) {
		// CLI login runs in an isolated home, so a missing link in the main
		// staging home is not a completed logout for these commands.
		if native, ok := nativeHome(); ok {
			return prepareAuth(native, ctxHome)
		}
		return nil
	}
	if native, ok := nativeHome(); ok {
		nativeAuth := filepath.Join(native, "auth.json")
		if _, err := os.Stat(nativeAuth); os.IsNotExist(err) {
			// The logout marker below prevents importing a stale keyring value
			// when the platform keyring is unavailable.
			_ = deleteCodexKeyring(authKeyringService, keyringAccount(native))
		} else if err != nil {
			return err
		}
		if err := os.Remove(nativeAuth); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	shared, err := paths.SharedCodexAuthPath()
	if err != nil {
		return err
	}
	if err := os.Remove(shared); err != nil && !os.IsNotExist(err) {
		return err
	}
	return atomicWriteAuth(filepath.Join(filepath.Dir(shared), authLogoutMarker), []byte("logout\n"))
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
