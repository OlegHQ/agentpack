package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/OlegHQ/agentpack/internal/slug"
)

const (
	LockfileName              = "pack.lock"
	ManifestName              = "agentpack.toml"
	StagedAgentpackBundleName = "agentpack-bundle"
	DotAgentsDir              = ".agents"
)

var ErrProjectNotFound = errors.New("no agentpack.toml or pack.lock found")

func ProjectDotAgentsDir(projectRoot string) string { return filepath.Join(projectRoot, DotAgentsDir) }
func ManifestPath(projectRoot string) string        { return filepath.Join(projectRoot, ManifestName) }
func LockPath(projectRoot string) string            { return filepath.Join(projectRoot, LockfileName) }

// UserAgentpackHome implements the documented AGENTPACK_HOME/XDG/LOCALAPPDATA
// precedence without depending on a platform directory package.
func UserAgentpackHome() (string, error) {
	if value := strings.TrimSpace(os.Getenv("AGENTPACK_HOME")); value != "" {
		return value, nil
	}
	if runtime.GOOS == "windows" {
		if value := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); value != "" {
			return filepath.Join(value, "agentpack"), nil
		}
		return "", errors.New("resolve agentpack home: LOCALAPPDATA is empty")
	}
	if value := os.Getenv("XDG_DATA_HOME"); value != "" {
		return filepath.Join(value, "agentpack"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve agentpack home: %w", err)
	}
	return filepath.Join(home, ".local", "share", "agentpack"), nil
}

func EnsureUserAgentpackLayout() (string, error) {
	root, err := UserAgentpackHome()
	if err != nil {
		return "", err
	}
	for _, name := range []string{"cache", "local", "projects"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", path, err)
		}
	}
	return root, nil
}

func underHome(parts ...string) (string, error) {
	root, err := UserAgentpackHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{root}, parts...)...), nil
}

func CacheDir() (string, error)          { return underHome("cache") }
func CacheDBPath() (string, error)       { return underHome("cache", "db.reddb") }
func LocalRegistryRoot() (string, error) { return underHome("local") }

func LocalMirrorPathFromShorthand(spec string) (string, error) {
	return underHome("local", filepath.FromSlash(spec))
}

func ProjectStateDir(projectRoot string) (string, error) {
	hash, err := ProjectPathHash(projectRoot)
	if err != nil {
		return "", err
	}
	return underHome("projects", hash)
}

func projectStateFile(projectRoot, name string) (string, error) {
	root, err := ProjectStateDir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

func CursorOverlayManifestPath(projectRoot string) (string, error) {
	return projectStateFile(projectRoot, "cursor-overlay.manifest")
}

func AgyOverlayManifestPath(projectRoot string) (string, error) {
	return projectStateFile(projectRoot, "agy-overlay.manifest")
}

func SessionHistoryRecoveryDirForComponent(projectRoot, harness, modeComponent string) (string, error) {
	hash, err := ProjectPathHash(projectRoot)
	if err != nil {
		return "", err
	}
	return underHome("recovery", "session-history", harness, hash, modeComponent, "conflicts")
}

func ProxyLogDir(projectRoot string) (string, error) {
	if value := strings.TrimSpace(os.Getenv("AGENTPACK_PROXY_LOG_DIR")); value != "" {
		return value, nil
	}
	return projectStateFile(projectRoot, "proxy-logs")
}

func FindProjectRoot(start string) (string, error) {
	directory, err := canonical(start)
	if err != nil {
		return "", err
	}
	for {
		if regularFile(filepath.Join(directory, ManifestName)) || regularFile(filepath.Join(directory, LockfileName)) {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("%w from %s", ErrProjectNotFound, start)
		}
		directory = parent
	}
}

func ResolveProjectRoot(explicit string) (string, error) {
	if explicit == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get current directory: %w", err)
		}
		return FindProjectRoot(cwd)
	}
	root, err := canonical(explicit)
	if err != nil {
		return "", err
	}
	if !regularFile(filepath.Join(root, ManifestName)) && !regularFile(filepath.Join(root, LockfileName)) {
		return "", fmt.Errorf("%w from %s", ErrProjectNotFound, root)
	}
	return root, nil
}

func ResolveProjectRootOrCWD(explicit string) (string, error) {
	if explicit != "" {
		return canonical(explicit)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}
	root, err := FindProjectRoot(cwd)
	if err == nil {
		return root, nil
	}
	if !errors.Is(err, ErrProjectNotFound) {
		return "", err
	}
	return canonical(cwd)
}

func ProjectPathHash(projectRoot string) (string, error) {
	root, err := canonical(projectRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:8]), nil
}

func ModePathComponent(modeName string) string {
	if modeName == "default" {
		return "default"
	}
	name := slug.DashedLower(modeName)
	if name == "" {
		name = "mode"
	}
	sum := sha256.Sum256([]byte(modeName))
	return name + "-" + hex.EncodeToString(sum[:4])
}

func LaunchSyncStatePath(projectRoot, modeName string) (string, error) {
	return projectStateFile(projectRoot, "launch-sync-"+ModePathComponent(modeName)+".state")
}

func stagingRootBase(projectRoot string) (string, error) {
	if value, ok := os.LookupEnv("AGENTPACK_STAGING_ROOT"); ok {
		return value, nil
	}
	hash, err := ProjectPathHash(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(os.TempDir(), "agentpack-"+hash), nil
}

func StagingRootForMode(projectRoot, modeName string) (string, error) {
	root, err := stagingRootBase(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "modes", ModePathComponent(modeName)), nil
}

func StagingRoot(projectRoot string) (string, error) {
	return StagingRootForMode(projectRoot, "default")
}

func stagingSubdir(projectRoot, modeName, segment string) (string, error) {
	root, err := StagingRootForMode(projectRoot, modeName)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, segment), nil
}

func StagingPluginsDirForMode(root, mode string) (string, error) {
	return stagingSubdir(root, mode, "plugins")
}
func StagingPluginsDir(root string) (string, error) { return StagingPluginsDirForMode(root, "default") }
func StagingOpenCodeDirForMode(root, mode string) (string, error) {
	return stagingSubdir(root, mode, "opencode")
}
func StagingOpenCodeDir(root string) (string, error) {
	return StagingOpenCodeDirForMode(root, "default")
}
func StagingCodexHomeDirForMode(root, mode string) (string, error) {
	return stagingSubdir(root, mode, "codex-home")
}
func StagingCodexHomeDir(root string) (string, error) {
	return StagingCodexHomeDirForMode(root, "default")
}
func StagingGrokHomeDirForMode(root, mode string) (string, error) {
	return stagingSubdir(root, mode, "grok-home")
}
func StagingGrokHomeDir(root string) (string, error) {
	return StagingGrokHomeDirForMode(root, "default")
}
func StagingGrokDirForMode(root, mode string) (string, error) {
	return stagingSubdir(root, mode, "grok")
}
func StagingGrokDir(root string) (string, error)             { return StagingGrokDirForMode(root, "default") }
func StagingAgyDirForMode(root, mode string) (string, error) { return stagingSubdir(root, mode, "agy") }
func StagingAgyDir(root string) (string, error)              { return StagingAgyDirForMode(root, "default") }
func StagingCursorBundleDirForMode(root, mode string) (string, error) {
	return stagingSubdir(root, mode, "cursor")
}
func StagingCursorBundleDir(root string) (string, error) {
	return StagingCursorBundleDirForMode(root, "default")
}
func StagingCursorHomeDirForMode(root, mode string) (string, error) {
	return stagingSubdir(root, mode, "cursor-home")
}
func StagingCursorHomeDir(root string) (string, error) {
	return StagingCursorHomeDirForMode(root, "default")
}

func bundleDirForMode(projectRoot, modeName, segment string) (string, error) {
	root, err := stagingSubdir(projectRoot, modeName, segment)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, StagedAgentpackBundleName), nil
}

func StagingGrokBundleDirForMode(root, mode string) (string, error) {
	return bundleDirForMode(root, mode, "grok")
}
func StagingGrokBundleDir(root string) (string, error) {
	return StagingGrokBundleDirForMode(root, "default")
}
func StagingAgyBundleDirForMode(root, mode string) (string, error) {
	return bundleDirForMode(root, mode, "agy")
}
func StagingAgyBundleDir(root string) (string, error) {
	return StagingAgyBundleDirForMode(root, "default")
}
func StagingCursorPackPluginDirForMode(root, mode string) (string, error) {
	return bundleDirForMode(root, mode, "cursor")
}
func StagingCursorPackPluginDir(root string) (string, error) {
	return StagingCursorPackPluginDirForMode(root, "default")
}

func SharedCodexAuthPath() (string, error)         { return underHome("shared", "codex", "auth.json") }
func AgentpackClaudeSettingsPath() (string, error) { return underHome("claude-settings.json") }
func CursorWorkspaceDir(projectRoot string) string { return filepath.Join(projectRoot, ".cursor") }

func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	return filepath.Clean(resolved), nil
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
