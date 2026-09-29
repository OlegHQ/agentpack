package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
)

// CurrentHome resolves the fully published home for new launches. The pointer
// is updated only after a replacement generation passes staging verification.
// Running Codex processes retain the old canonical path across that update.
func CurrentHome(projectRoot, modeName string) (string, error) {
	pointer, err := generationPointer(projectRoot, modeName)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(pointer)
	if os.IsNotExist(err) {
		return paths.StagingCodexHomeDirForMode(projectRoot, modeName)
	}
	if err != nil {
		return "", err
	}
	home := strings.TrimSpace(string(data))
	if !filepath.IsAbs(home) || strings.ContainsRune(home, 0) {
		return "", fmt.Errorf("invalid Codex home generation pointer %s", pointer)
	}
	baseDir, err := generationBase(projectRoot, modeName)
	if err != nil {
		return "", err
	}
	if filepath.Dir(filepath.Clean(home)) != filepath.Clean(baseDir) || !strings.HasPrefix(filepath.Base(home), "g-") {
		return "", fmt.Errorf("Codex home generation pointer %s points outside its mode", pointer)
	}
	return home, nil
}

func generationPointer(projectRoot, modeName string) (string, error) {
	state, err := paths.ProjectStateDir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "codex-generations", paths.ModePathComponent(modeName)+".current"), nil
}

func generationBase(projectRoot, modeName string) (string, error) {
	return shortStagingCodexHome(projectRoot, modeName)
}

type generationTransaction struct {
	home      string
	pointer   string
	lock      *flock.Flock
	committed bool
}

func (transaction *generationTransaction) Root() string { return transaction.home }

func (transaction *generationTransaction) Commit() error {
	if err := writeGenerationPointer(transaction.pointer, transaction.home); err != nil {
		return err
	}
	transaction.committed = true
	// Retirement is best effort: a busy or unreachable old daemon must keep
	// its home, and cleanup must never roll back a published generation.
	_ = retireGenerationsLocked(transaction.home)
	return nil
}

func (transaction *generationTransaction) Abort() error {
	var cleanupErr error
	if !transaction.committed {
		cleanupErr = os.RemoveAll(transaction.home)
	}
	return errors.Join(cleanupErr, transaction.lock.Unlock())
}

func beginGeneration(ctx base.StageContext) (base.RebuildTransaction, error) {
	pointer, err := generationPointer(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(pointer), 0o700); err != nil {
		return nil, err
	}
	lock := flock.New(pointer + ".lock")
	deadline, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	acquired, err := lock.TryLockContext(deadline, 50*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("lock Codex generation: %w", err)
	}
	if !acquired {
		return nil, fmt.Errorf("timed out locking Codex generation for mode %s", ctx.Mode.Name())
	}
	baseDir, err := generationBase(ctx.ProjectRoot, ctx.Mode.Name())
	if err == nil {
		err = os.MkdirAll(baseDir, 0o700)
	}
	var home string
	if err == nil {
		home, err = os.MkdirTemp(baseDir, "g-")
	}
	if err == nil && (needsShortHome(home) || windowsSocketPathTooLong(home)) {
		err = fmt.Errorf("Codex generation path exceeds the local socket limit: %s", home)
	}
	if err != nil {
		if home != "" {
			_ = os.RemoveAll(home)
		}
		_ = lock.Unlock()
		return nil, err
	}
	return &generationTransaction{home: home, pointer: pointer, lock: lock}, nil
}

func windowsSocketPathTooLong(home string) bool {
	return runtime.GOOS == "windows" && len(home)+len(controlSocketSuffix) >= 108
}

func writeGenerationPointer(pointer, home string) error {
	file, err := os.CreateTemp(filepath.Dir(pointer), ".codex-current-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(home + "\n"); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), pointer)
}

// publishedGenerationHomes includes previous generations so copied MCP OAuth
// stores can be reconciled even after a process exits during a mode switch.
func publishedGenerationHomes(projectRoot string) ([]string, error) {
	state, err := paths.ProjectStateDir(projectRoot)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(state, "codex-generations"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var homes []string
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".current") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(state, "codex-generations", entry.Name()))
		if err != nil {
			return nil, err
		}
		current := strings.TrimSpace(string(data))
		if !filepath.IsAbs(current) || !strings.HasPrefix(filepath.Base(current), "g-") {
			return nil, fmt.Errorf("invalid Codex generation pointer for %s", entry.Name())
		}
		siblings, err := os.ReadDir(filepath.Dir(current))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, sibling := range siblings {
			if sibling.IsDir() && strings.HasPrefix(sibling.Name(), "g-") {
				homes = append(homes, filepath.Join(filepath.Dir(current), sibling.Name()))
			}
		}
	}
	slices.Sort(homes)
	return slices.Compact(homes), nil
}

func acquireGenerationLease(projectRoot, modeName string) (string, *flock.Flock, error) {
	pointer, err := generationPointer(projectRoot, modeName)
	if err != nil {
		return "", nil, err
	}
	guard := flock.New(pointer + ".lock")
	if err := os.MkdirAll(filepath.Dir(pointer), 0o700); err != nil {
		return "", nil, err
	}
	if err := guard.RLock(); err != nil {
		return "", nil, err
	}
	defer guard.Unlock()
	home, err := CurrentHome(projectRoot, modeName)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(filepath.Dir(home), 0o700); err != nil {
		return "", nil, err
	}
	lease := flock.New(home + ".lease")
	if err := lease.RLock(); err != nil {
		return "", nil, err
	}
	return home, lease, nil
}

func retireGenerations(projectRoot, modeName string) error {
	pointer, err := generationPointer(projectRoot, modeName)
	if err != nil {
		return err
	}
	guard := flock.New(pointer + ".lock")
	if err := guard.Lock(); err != nil {
		return err
	}
	defer guard.Unlock()
	home, err := CurrentHome(projectRoot, modeName)
	if err != nil {
		return err
	}
	return retireGenerationsLocked(home)
}

func retireGenerationsLocked(current string) error {
	if !strings.HasPrefix(filepath.Base(current), "g-") {
		return nil
	}
	entries, err := os.ReadDir(filepath.Dir(current))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "g-") {
			continue
		}
		home := filepath.Join(filepath.Dir(current), entry.Name())
		if home == current {
			continue
		}
		lease := flock.New(home + ".lease")
		free, err := lease.TryLock()
		if err != nil || !free {
			continue
		}
		retired := false
		if canRetireGeneration(home) {
			retired = os.RemoveAll(home) == nil
		}
		_ = lease.Unlock()
		if retired {
			_ = os.Remove(home + ".lease")
		}
	}
	return nil
}

func canRetireGeneration(home string) bool {
	socket := filepath.Join(home, "app-server-control", "app-server-control.sock")
	if _, err := os.Stat(socket); os.IsNotExist(err) {
		return true
	}
	empty, err := daemonHasNoLoadedThreads(socket)
	if err != nil || !empty {
		return false
	}
	binary, err := base.ResolveBinary("CODEX_PATH", "codex")
	if err != nil {
		return false
	}
	command := exec.Command(binary, "app-server", "daemon", "stop")
	command.Env = append(os.Environ(), "CODEX_HOME="+home)
	return command.Run() == nil
}
