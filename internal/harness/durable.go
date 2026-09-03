package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func LinkDurableDirectory(native, staged string) error {
	if err := os.MkdirAll(native, 0o755); err != nil {
		return err
	}
	if err := removeAny(staged); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(native, staged); err != nil {
		return fmt.Errorf("create durable directory link %s: %w", staged, err)
	}
	return nil
}

func DurablePathMatches(path, target string) bool {
	left, leftErr := os.Stat(path)
	right, rightErr := os.Stat(target)
	return leftErr == nil && rightErr == nil && os.SameFile(left, right)
}

func RecoverWithoutOverwrite(source, destination, conflictsRoot string) error {
	info, err := os.Lstat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if DurablePathMatches(source, destination) {
			return nil
		}
		return fmt.Errorf("refusing to recover unexpected symlink %s", source)
	}
	if info.Mode().IsRegular() {
		return recoverFile(source, destination, conflictsRoot, "history.jsonl")
	}
	if !info.IsDir() || DurablePathMatches(source, destination) {
		return nil
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to recover symlink inside session history: %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(destination, relative), 0o755)
		}
		if info.Mode().IsRegular() {
			return recoverFile(path, filepath.Join(destination, relative), conflictsRoot, relative)
		}
		return nil
	})
}

func recoverFile(source, destination, conflictsRoot, relative string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	before, err := os.Stat(source)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		input, openErr := os.Open(source)
		if openErr != nil {
			output.Close()
			return openErr
		}
		_, copyErr := io.Copy(output, input)
		closeIn, closeOut := input.Close(), output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeIn != nil {
			return closeIn
		}
		if closeOut != nil {
			return closeOut
		}
		after, statErr := os.Stat(source)
		if statErr != nil {
			return statErr
		}
		if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			_ = os.Remove(destination)
			return fmt.Errorf("session history changed during recovery: %s; close the active harness and retry", source)
		}
		return nil
	}
	if !os.IsExist(err) {
		return err
	}
	sourceHash, err := hashFile(source)
	if err != nil {
		return err
	}
	destinationHash, err := hashFile(destination)
	if err != nil {
		return err
	}
	if sourceHash == destinationHash {
		return nil
	}
	extension := "." + hex.EncodeToString(sourceHash[:6]) + ".conflict"
	conflict := filepath.Join(conflictsRoot, filepath.Dir(relative), filepath.Base(relative)+extension)
	if existing, err := hashFile(conflict); err == nil && existing == sourceHash {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(conflict), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(conflict, data, 0o644)
}

func hashFile(path string) ([32]byte, error) {
	var zero [32]byte
	file, err := os.Open(path)
	if err != nil {
		return zero, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return zero, err
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}
func removeAny(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}
