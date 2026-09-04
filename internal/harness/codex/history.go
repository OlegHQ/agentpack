package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
)

var durableDirectories = []string{"sessions", "archived_sessions"}

const promptHistory = "history.jsonl"

func nativeHome() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".codex"), true
}
func prepareHistory(staged, native string) error {
	if err := os.MkdirAll(native, 0o755); err != nil {
		return err
	}
	for _, name := range durableDirectories {
		if err := base.LinkDurableDirectory(filepath.Join(native, name), filepath.Join(staged, name)); err != nil {
			return err
		}
	}
	if err := base.LinkDurableFile(filepath.Join(native, promptHistory), filepath.Join(staged, promptHistory)); err != nil {
		return err
	}
	return updateConfig(filepath.Join(staged, "config.toml"), func(root map[string]any) {
		if _, exists := root["sqlite_home"]; !exists {
			root["sqlite_home"] = native
		}
	})
}
func verifyHistory(staged, native string) error {
	for _, name := range append(append([]string{}, durableDirectories...), promptHistory) {
		if !base.DurablePathMatches(filepath.Join(staged, name), filepath.Join(native, name)) {
			return fmt.Errorf("codex durable history link %s does not resolve to native state", name)
		}
	}
	root := make(map[string]any)
	data, err := os.ReadFile(filepath.Join(staged, "config.toml"))
	if err != nil {
		return err
	}
	if err := jsonOrToml(data, &root); err != nil {
		return err
	}
	if _, exists := root["sqlite_home"]; !exists {
		return fmt.Errorf("codex staged config is missing durable sqlite_home")
	}
	return nil
}
func recoverHistory(projectRoot, currentMode string) error {
	native, ok := nativeHome()
	if !ok {
		return nil
	}
	current, err := paths.StagingCodexHomeDirForMode(projectRoot, currentMode)
	if err != nil {
		return err
	}
	modes := filepath.Dir(filepath.Dir(current))
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
		staged := filepath.Join(modes, entry.Name(), "codex-home")
		info, err := os.Lstat(staged)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		needed, err := needsHistoryRecovery(staged, native)
		if err != nil {
			return err
		}
		if !needed {
			continue
		}
		if err := rejectActiveWriters(staged); err != nil {
			return err
		}
		conflicts, err := paths.SessionHistoryRecoveryDirForComponent(projectRoot, "codex", entry.Name())
		if err != nil {
			return err
		}
		for _, name := range durableDirectories {
			if err := base.RecoverWithoutOverwrite(filepath.Join(staged, name), filepath.Join(native, name), filepath.Join(conflicts, name)); err != nil {
				return err
			}
		}
		if err := base.RecoverWithoutOverwrite(filepath.Join(staged, promptHistory), filepath.Join(native, promptHistory), filepath.Join(conflicts, promptHistory)); err != nil {
			return err
		}
	}
	return nil
}
func needsHistoryRecovery(staged, native string) (bool, error) {
	for _, name := range append(append([]string{}, durableDirectories...), promptHistory) {
		path := filepath.Join(staged, name)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return false, err
		}
		if !base.DurablePathMatches(path, filepath.Join(native, name)) {
			return true, nil
		}
	}
	return false, nil
}
func rejectActiveWriters(staged string) error {
	directory := filepath.Join(staged, "thread-writer-locks")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		lock := flock.New(filepath.Join(directory, entry.Name()))
		acquired, err := lock.TryLock()
		if err != nil {
			return err
		}
		if !acquired {
			return fmt.Errorf("active Codex session is writing under %s; close it and retry sync", staged)
		}
		if err := lock.Unlock(); err != nil {
			return err
		}
	}
	return nil
}
