package cache

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/OlegHQ/agentpack/internal/paths"
)

func CopyPackageDirToCache(source, identityPrefix string) (cacheKey, commit, destination string, err error) {
	if _, err := paths.EnsureUserAgentpackLayout(); err != nil {
		return "", "", "", err
	}
	cacheRoot, err := paths.CacheDir()
	if err != nil {
		return "", "", "", err
	}
	temporary, err := os.MkdirTemp(cacheRoot, ".tmp-copy-")
	if err != nil {
		return "", "", "", fmt.Errorf("create cache temporary directory: %w", err)
	}
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			_ = os.RemoveAll(temporary)
		}
	}()
	commit, err = hashAndCopySourceTree(source, temporary)
	if err != nil {
		return "", "", "", err
	}
	cacheKey = ComputeKey(identityPrefix + "\x00" + commit)
	destination, err = EntryDir(cacheKey)
	if err != nil {
		return "", "", "", err
	}
	if err := os.RemoveAll(destination); err != nil {
		return "", "", "", fmt.Errorf("remove existing cache entry %s: %w", destination, err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		if copyErr := copyMergeTree(temporary, destination); copyErr != nil {
			return "", "", "", errors.Join(fmt.Errorf("rename cache entry: %w", err), copyErr)
		}
		if removeErr := os.RemoveAll(temporary); removeErr != nil {
			return "", "", "", fmt.Errorf("remove copied cache temporary directory: %w", removeErr)
		}
	}
	keepTemporary = true
	if err := NormalizePluginLayout(destination); err != nil {
		return "", "", "", err
	}
	return cacheKey, commit, destination, nil
}

func copyMergeTree(source, destination string) error {
	resolved, err := filepath.EvalSymlinks(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve copy source %s: %w", source, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("inspect copy source %s: %w", resolved, err)
	}
	if !info.IsDir() {
		return copyFile(resolved, destination)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return fmt.Errorf("create copy destination %s: %w", destination, err)
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return fmt.Errorf("read copy source %s: %w", resolved, err)
	}
	for _, entry := range entries {
		if err := copyMergeTree(filepath.Join(resolved, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create copy parent %s: %w", filepath.Dir(destination), err)
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open copy source %s: %w", source, err)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		input.Close()
		return fmt.Errorf("create copy destination %s: %w", destination, err)
	}
	_, copyErr := io.Copy(output, input)
	inputCloseErr := input.Close()
	outputCloseErr := output.Close()
	if copyErr != nil {
		return fmt.Errorf("copy %s to %s: %w", source, destination, copyErr)
	}
	if inputCloseErr != nil || outputCloseErr != nil {
		return errors.Join(inputCloseErr, outputCloseErr)
	}
	return nil
}
