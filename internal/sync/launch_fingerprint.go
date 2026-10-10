package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
)

const stagingLayoutVersion = "2"

type launchState struct {
	Digest string `json:"digest"`
}

func ComputeLaunchDigest(projectRoot, workspace string, effective mode.Effective, target *base.Target) (string, error) {
	hash := sha256.New()
	writeDigestFile(hash, "manifest", paths.ManifestPath(projectRoot))
	writeDigestFile(hash, "lock", paths.LockPath(projectRoot))
	hash.Write([]byte("dot_agents\x00"))
	dot, err := hashDotAgents(paths.ProjectDotAgentsDir(workspace))
	if err != nil {
		return "", err
	}
	hash.Write(dot)
	hash.Write([]byte("staging_root\x00" + os.Getenv("AGENTPACK_STAGING_ROOT")))
	hash.Write([]byte("mode\x00" + effective.FingerprintMaterial()))
	name := "__none__"
	if target != nil {
		name = string(*target)
	}
	hash.Write([]byte("target\x00" + name))
	hash.Write([]byte("workspace\x00"))
	hash.Write([]byte(workspace))
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func writeDigestFile(hash interface{ Write([]byte) (int, error) }, label, path string) {
	hash.Write([]byte(label + "\x00"))
	data, err := os.ReadFile(path)
	if err != nil {
		hash.Write([]byte("__missing__"))
		return
	}
	hash.Write(data)
}
func hashDotAgents(root string) ([]byte, error) {
	info, err := os.Stat(root)
	if os.IsNotExist(err) || err == nil && !info.IsDir() {
		return []byte("__dot_agents_absent__"), nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err == nil && info.Mode().IsRegular() {
				files = append(files, path)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(files)
	hash := sha256.New()
	hash.Write([]byte("staging_layout_version\x00" + stagingLayoutVersion))
	for _, path := range files {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		hash.Write([]byte(relative))
		hash.Write([]byte{0})
		info, _ := os.Stat(path)
		size := uint64(info.Size())
		var encoded [8]byte
		for index := 0; index < 8; index++ {
			encoded[index] = byte(size >> (8 * index))
		}
		hash.Write(encoded[:])
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		hash.Write(data)
	}
	return hash.Sum(nil), nil
}
func ReadLaunchDigest(projectRoot, modeName string) (string, bool, error) {
	path, err := paths.LaunchSyncStatePath(projectRoot, modeName)
	if err != nil {
		return "", false, err
	}
	return readDigestState(path)
}
func WriteLaunchDigest(projectRoot, modeName, digest string) error {
	path, err := paths.LaunchSyncStatePath(projectRoot, modeName)
	if err != nil {
		return err
	}
	return writeDigestState(path, digest)
}
func readMaterializationDigest(projectRoot, modeName string) (string, bool, error) {
	path, err := paths.ProjectStateFile(projectRoot, "materialized-"+paths.ModePathComponent(modeName)+".json")
	if err != nil {
		return "", false, err
	}
	return readDigestState(path)
}
func writeMaterializationDigest(projectRoot, modeName, digest string) error {
	path, err := paths.ProjectStateFile(projectRoot, "materialized-"+paths.ModePathComponent(modeName)+".json")
	if err != nil {
		return err
	}
	return writeDigestState(path, digest)
}
func computeMaterializationDigest(definition, workspace string, effective mode.Effective) (string, error) {
	inputs, err := ComputeLaunchDigest(definition, workspace, effective, nil)
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	roots := []string{filepath.Join(workspace, ".agents"), filepath.Join(workspace, ".claude"), filepath.Join(workspace, ".cursor"), filepath.Join(workspace, "AGENTS.md"), filepath.Join(workspace, "CLAUDE.md"), filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".grok", "skills")}
	for _, target := range base.AllTargets() {
		roots = append(roots, inheritedConfigPaths(&target)...)
	}
	ambient, err := fingerprintInputs(roots)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(inputs + "\x00ambient\x00" + ambient + "\x00attribution\x00" + os.Getenv("AGENTPACK_KEEP_ATTRIBUTION")))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
func readDigestState(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var state launchState
	if err := json.Unmarshal(data, &state); err != nil {
		return "", false, fmt.Errorf("read sync input state %s: %w", path, err)
	}
	return state.Digest, true, nil
}
func writeDigestState(path, digest string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(launchState{Digest: digest})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".sync-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
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
	return os.Rename(file.Name(), path)
}
