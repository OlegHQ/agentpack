package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/gofrs/flock"
)

var pendingLoginHomes sync.Map
var pendingGenerationLeases sync.Map // *exec.Cmd -> *flock.Flock

func New() base.Harness {
	return base.Definition{
		Target:         base.Codex,
		Root:           stagedRoot,
		Reset:          resetPaths,
		BeforeReset:    preReset,
		Begin:          beginGeneration,
		Setup:          prepare,
		MCP:            writeMCP,
		Guidance:       injectGuidance,
		Check:          verify,
		Launch:         launch,
		LaunchFinished: afterLaunch,
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
	home, lease, err := acquireGenerationLease(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	keepLease := false
	defer func() {
		if !keepLease {
			_ = lease.Unlock()
		}
	}()
	if needsShortHome(home) {
		if shortDir, err := shortStagingCodexHome(ctx.ProjectRoot, ctx.Mode.Name()); err == nil {
			home = shortDir
		}
	}
	if _, err := os.Lstat(filepath.Join(home, credentialsBaselineFile)); err == nil {
		if err := reconcileMCPAuthMode(ctx.ProjectRoot, ctx.Mode.Name()); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if isCodexLogin(arguments) {
		loginHome, err := prepareIsolatedLoginHome(home)
		if err != nil {
			return nil, err
		}
		pendingLoginHomes.Store(loginHome, home)
		home = loginHome
	}
	command := exec.Command(binary, arguments...)
	command.Env = append(os.Environ(), "CODEX_HOME="+home)
	pendingGenerationLeases.Store(command, lease)
	keepLease = true
	return command, nil
}

func isCodexLogin(arguments []string) bool {
	command, tail := codexCommand(arguments)
	if command != "login" {
		return false
	}
	subcommand, _ := codexCommand(tail)
	return subcommand != "status"
}

func isCodexLogout(arguments []string) bool {
	command, _ := codexCommand(arguments)
	return command == "logout"
}

func codexCommand(arguments []string) (string, []string) {
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			return "", nil
		}
		if codexOptionTakesValue(argument) {
			index++
			continue
		}
		if strings.HasPrefix(argument, "-") {
			continue
		}
		return argument, arguments[index+1:]
	}
	return "", nil
}

func codexOptionTakesValue(argument string) bool {
	switch argument {
	case "-c", "--config", "--enable", "--disable", "--remote", "--remote-auth-token-env", "-i", "--image", "-m", "--model", "--local-provider", "-p", "--profile", "-s", "--sandbox", "-C", "--cd", "--add-dir", "-a", "--ask-for-approval":
		return true
	}
	return false
}

func usesInteractiveCodex(arguments []string) bool {
	command, _ := codexCommand(arguments)
	switch command {
	case "agents", "exec", "e", "review", "login", "logout", "mcp", "plugin", "app-server", "remote-control", "completion", "update", "doctor", "sandbox", "debug", "apply", "a", "queue", "archive", "delete", "migrate-rollouts", "unarchive", "cloud", "exec-server", "features", "help", "tcp-tunnel":
		return false
	}
	return true
}

func afterLaunch(ctx base.LaunchContext) error {
	var lease *flock.Flock
	if ctx.Command != nil {
		if value, ok := pendingGenerationLeases.LoadAndDelete(ctx.Command); ok {
			lease = value.(*flock.Flock)
			defer func() {
				if lease != nil {
					_ = lease.Unlock()
				}
			}()
		}
	}
	var home string
	if ctx.Command != nil {
		for _, entry := range ctx.Command.Env {
			if value, ok := strings.CutPrefix(entry, "CODEX_HOME="); ok {
				home = value
			}
		}
	}
	if home == "" {
		var err error
		home, err = CurrentHome(ctx.ProjectRoot, ctx.Mode.Name())
		if err != nil {
			return err
		}
	}
	var loginErr error
	if value, ok := pendingLoginHomes.LoadAndDelete(home); ok {
		loginHome := home
		home = value.(string)
		loginErr = finishIsolatedLoginHome(loginHome, home)
	}
	result := errors.Join(loginErr, finishAuthLaunch(home, ctx.Arguments), reconcileMCPAuthHome(ctx.ProjectRoot, home))
	if ctx.Command != nil {
		if lease != nil {
			result = errors.Join(result, lease.Unlock())
			lease = nil
		}
		// Run after the child exits. Busy generations retain their leases and
		// loaded threads, so retirement can make progress on later launches.
		_ = retireGenerations(ctx.ProjectRoot, ctx.Mode.Name())
	}
	return result
}

func stagedRoot(ctx base.StageContext) (string, error) {
	if home := ctx.StagedRoots[base.Codex]; home != "" {
		return home, nil
	}
	return CurrentHome(ctx.ProjectRoot, ctx.Mode.Name())
}

const controlSocketSuffix = "/app-server-control/app-server-control.sock"

func maxSunLen() int {
	switch runtime.GOOS {
	case "linux":
		return 108
	case "windows":
		return 0 // legacy staged homes are not shortened with symlinks on Windows
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
	if runtime.GOOS == "windows" {
		modeHash := sha256.Sum256([]byte(hash + "\x00" + modeName))
		return filepath.Join(os.TempDir(), "a", hex.EncodeToString(modeHash[:4])), nil
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
	if ctx.StagedRoots[base.Codex] != "" {
		return nil, nil // the new generation is private and already empty
	}
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
	if ctx.StagedRoots[base.Codex] != "" {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
	} else if needsShortHome(root) {
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
	if err := verifyAuth(root); err != nil {
		return err
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
