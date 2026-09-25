package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func New() base.Harness {
	return base.Definition{
		Target:      base.Codex,
		Root:        stagedRoot,
		Reset:       resetPaths,
		BeforeReset: preReset,
		Setup:       prepare,
		MCP:         writeMCP,
		Guidance:    injectGuidance,
		Check:       verify,
		Launch:      launch,
	}
}

func launch(ctx base.LaunchContext) (*exec.Cmd, error) {
	arguments := append([]string(nil), ctx.Arguments...)
	if ctx.Yolo && !base.HasAny(arguments, "--dangerously-bypass-approvals-and-sandbox", "--yolo") {
		flag := "--dangerously-bypass-approvals-and-sandbox"
		if len(arguments) == 0 || strings.HasPrefix(arguments[0], "-") {
			arguments = append([]string{flag}, arguments...)
		} else {
			arguments = append(arguments[:1], append([]string{flag}, arguments[1:]...)...)
		}
	}
	binary, err := base.ResolveBinary("CODEX_PATH", "codex")
	if err != nil {
		return nil, err
	}
	home, err := paths.StagingCodexHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	if needsShortHome(home) {
		if shortDir, err := shortStagingCodexHome(ctx.ProjectRoot, ctx.Mode.Name()); err == nil {
			home = shortDir
		}
	}
	command := exec.Command(binary, arguments...)
	command.Env = append(os.Environ(), "CODEX_HOME="+home)
	return command, nil
}

func stagedRoot(ctx base.StageContext) (string, error) {
	return paths.StagingCodexHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
}

const controlSocketSuffix = "/app-server-control/app-server-control.sock"

func maxSunLen() int {
	switch runtime.GOOS {
	case "linux":
		return 108
	case "windows":
		return 0
	default:
		return 104
	}
}

func isShortHomeSymlink(dir string) (string, bool) {
	info, err := os.Lstat(dir)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	target, err := os.Readlink(dir)
	if err != nil {
		return "", false
	}
	return target, true
}

func needsShortHome(dir string) bool {
	limit := maxSunLen()
	if limit == 0 {
		return false
	}
	if _, ok := isShortHomeSymlink(dir); ok {
		return true
	}
	resolved := canonicalOrProjected(dir)
	return len(resolved)+len(controlSocketSuffix) >= limit
}

func canonicalOrProjected(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	curr := abs
	var suffix []string
	for {
		if fi, err := os.Stat(curr); err == nil && fi.IsDir() {
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		suffix = append([]string{filepath.Base(curr)}, suffix...)
		curr = parent
	}
	if resolved, err := filepath.EvalSymlinks(curr); err == nil && resolved != "" {
		curr = resolved
	}
	return filepath.Join(append([]string{curr}, suffix...)...)
}

func shortStagingCodexHome(projectRoot, modeName string) (string, error) {
	hash, err := paths.ProjectPathHash(projectRoot)
	if err != nil {
		return "", err
	}
	baseDir := "/var/tmp"
	if fi, err := os.Stat(baseDir); err != nil || !fi.IsDir() {
		baseDir = os.TempDir()
	}
	if resolved, err := filepath.EvalSymlinks(baseDir); err == nil && resolved != "" {
		baseDir = resolved
	}
	modeComp := paths.ModePathComponent(modeName)
	if len(modeComp) > 10 {
		sum := sha256.Sum256([]byte(modeName))
		modeComp = hex.EncodeToString(sum[:4])
	}
	return filepath.Join(baseDir, "ap-c", hash, modeComp), nil
}

func resetPaths(ctx base.StageContext) ([]string, error) {
	root, err := stagedRoot(ctx)
	if err != nil {
		return nil, err
	}
	result := []string{root}
	if target, ok := isShortHomeSymlink(root); ok {
		result = append(result, target)
	}
	if needsShortHome(root) {
		if shortDir, err := shortStagingCodexHome(ctx.ProjectRoot, ctx.Mode.Name()); err == nil {
			result = append(result, shortDir)
		}
	}
	sort.Strings(result)
	return slices.Compact(result), nil
}

func preReset(ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	if err := recoverHistory(ctx.ProjectRoot, ctx.Mode.Name()); err != nil {
		return err
	}
	if err := preserveAuth(root); err != nil {
		return err
	}
	return recoverMCPAuth(ctx.ProjectRoot, ctx.Mode.Name())
}

func prepare(ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	targetDir := root
	if needsShortHome(root) {
		shortDir, err := shortStagingCodexHome(ctx.ProjectRoot, ctx.Mode.Name())
		if err != nil {
			return err
		}
		targetDir = shortDir
		if err := os.MkdirAll(shortDir, 0o755); err != nil {
			return err
		}
		if err := ensureSymlink(shortDir, root); err != nil {
			return err
		}
	} else {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		native := filepath.Join(home, ".codex")
		if err := base.CopySelectedEntries(native, targetDir, []string{"config.toml", "hooks.json", "skills", "themes"}); err != nil {
			return err
		}
		if err := prepareAuth(native, targetDir); err != nil {
			return err
		}
	}
	if err := forceAuthFileStore(targetDir); err != nil {
		return err
	}
	if err := prepareMCPAuth(ctx.ProjectRoot, targetDir); err != nil {
		return err
	}
	if native, ok := nativeHome(); ok {
		if err := prepareHistory(targetDir, native); err != nil {
			return err
		}
	}
	if !keepAttribution() {
		if err := updateConfig(filepath.Join(targetDir, "config.toml"), func(config map[string]any) { delete(config, "commit_attribution") }); err != nil {
			return err
		}
	}
	return nil
}

func ensureSymlink(target, link string) error {
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			curr, _ := os.Readlink(link)
			if curr == target {
				return nil
			}
			_ = os.Remove(link)
		} else {
			_ = os.RemoveAll(link)
		}
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	return os.Symlink(target, link)
}

func writeMCP(entries mcp.Entries, ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	return MergeMCP(filepath.Join(root, "config.toml"), entries)
}

func injectGuidance(blob string, ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	return base.WriteGuidance(filepath.Join(root, "AGENTS.md"), blob)
}

func verify(ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("codex home staging missing %s", root)
	}
	if native, ok := nativeHome(); ok {
		if err := verifyHistory(root, native); err != nil {
			return err
		}
	}
	return verifyMCPAuth(ctx.ProjectRoot, root)
}

func keepAttribution() bool {
	switch os.Getenv("AGENTPACK_KEEP_ATTRIBUTION") {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
