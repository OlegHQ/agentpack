package grok

import (
	"os"
	"path/filepath"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func nativeHome() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".grok"), true
}
func prepareHistory(staged, native string) error {
	return base.LinkDurableDirectory(filepath.Join(native, "sessions"), filepath.Join(staged, "sessions"))
}
func verifyHistory(staged, native string) error {
	if !base.DurablePathMatches(filepath.Join(staged, "sessions"), filepath.Join(native, "sessions")) {
		return os.ErrInvalid
	}
	return nil
}
func recoverHistory(projectRoot, currentMode string) error {
	native, ok := nativeHome()
	if !ok {
		return nil
	}
	current, err := paths.StagingRootForMode(projectRoot, currentMode)
	if err != nil {
		return err
	}
	modes := filepath.Dir(current)
	entries, err := os.ReadDir(modes)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		staged := filepath.Join(modes, entry.Name(), "grok-home")
		info, err := os.Lstat(staged)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		conflicts, err := paths.SessionHistoryRecoveryDirForComponent(projectRoot, "grok", entry.Name())
		if err != nil {
			return err
		}
		if err := base.RecoverWithoutOverwrite(filepath.Join(staged, "sessions"), filepath.Join(native, "sessions"), filepath.Join(conflicts, "sessions")); err != nil {
			return err
		}
	}
	return nil
}

func recoverCredentials(projectRoot, mode string) error {
	legacy, err := paths.StagingRootForMode(projectRoot, mode)
	if err != nil {
		return err
	}
	durable, err := paths.StagingGrokHomeDirForMode(projectRoot, mode)
	if err != nil {
		return err
	}
	for _, name := range []string{"auth.json", "mcp_credentials.json"} {
		destination := filepath.Join(durable, name)
		if _, err := os.Lstat(destination); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		source := filepath.Join(legacy, "grok-home", name)
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(durable, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}
