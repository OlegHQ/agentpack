package grok

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/pelletier/go-toml/v2"
)

func New() base.Harness {
	return base.Definition{Target: base.Grok, Root: stagedRoot, Reset: resetPaths, BeforeReset: preReset, Setup: prepare, MCP: writeMCP, Guidance: injectGuidance, Check: verify, Launch: launch, LaunchFinished: persistCredentials}
}

func launch(ctx base.LaunchContext) (*exec.Cmd, error) {
	arguments := append([]string(nil), ctx.Arguments...)
	if !base.HasFlagValue(arguments, "--cwd") {
		arguments = append([]string{"--cwd", ctx.ProjectRoot}, arguments...)
	}
	if ctx.Yolo {
		arguments = base.PrependOnce(arguments, "--always-approve")
	}
	binary, err := base.ResolveBinary("GROK_PATH", "grok")
	if err != nil {
		return nil, err
	}
	home, err := paths.StagingGrokHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	if err := loadSharedCredentials(home); err != nil {
		return nil, err
	}
	command := exec.Command(binary, arguments...)
	command.Env = append(os.Environ(), "GROK_HOME="+home)
	return command, nil
}
func stagedRoot(ctx base.StageContext) (string, error) {
	return paths.StagingGrokBundleDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
}
func resetPaths(ctx base.StageContext) ([]string, error) {
	root, err := paths.StagingGrokDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	return []string{root}, nil
}
func preReset(ctx base.StageContext) error {
	if err := recoverCredentials(ctx.ProjectRoot, ctx.Mode.Name()); err != nil {
		return err
	}
	return recoverHistory(ctx.ProjectRoot, ctx.Mode.Name())
}
func prepare(ctx base.StageContext) error {
	bundle, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bundle, "plugin.json"), []byte(`{"name":"agentpack-bundle"}`), 0o644); err != nil {
		return err
	}
	home, err := paths.StagingGrokHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	if err := seedHome(home, bundle); err != nil {
		return err
	}
	if native, ok := nativeHome(); ok {
		if err := prepareHistory(home, native); err != nil {
			return err
		}
	}
	if keepAttribution() {
		return nil
	}
	return writeAttribution(home)
}
func seedHome(staged, bundle string) error {
	for _, name := range []string{"config.toml", "skills", "agents", "commands", "plugins", "AGENTS.md"} {
		if err := os.RemoveAll(filepath.Join(staged, name)); err != nil {
			return err
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		native := filepath.Join(home, ".grok")
		if err := base.CopySelectedEntries(native, staged, []string{"config.toml", "skills", "agents", "commands", "plugins"}); err != nil {
			return err
		}
		for _, name := range []string{"auth.json", "mcp_credentials.json"} {
			source, dest := filepath.Join(native, name), filepath.Join(staged, name)
			if _, err := os.Lstat(dest); os.IsNotExist(err) {
				if info, err := os.Stat(source); err == nil && info.Mode().IsRegular() {
					data, err := os.ReadFile(source)
					if err != nil {
						return err
					}
					if err := os.WriteFile(dest, data, 0o600); err != nil {
						return err
					}
				}
			}
		}
	}
	if err := enableBundle(filepath.Join(staged, "config.toml")); err != nil {
		return err
	}
	// Grok trusts plugins under GROK_HOME/plugins. A [plugins].paths entry stays
	// disabled, and a trusted plugin still omits its skills until it is named in
	// [plugins].enabled.
	return linkTrustedPlugin(staged, bundle)
}
func enableBundle(path string) error {
	root := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &root); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	plugins, ok := root["plugins"].(map[string]any)
	if root["plugins"] != nil && !ok {
		return fmt.Errorf("%s: plugins must be a TOML table", path)
	}
	if plugins == nil {
		plugins = make(map[string]any)
		root["plugins"] = plugins
	}
	var enabled []string
	switch raw := plugins["enabled"].(type) {
	case nil:
	case []any:
		for _, value := range raw {
			text, ok := value.(string)
			if !ok {
				return fmt.Errorf("%s: plugins.enabled must be an array of strings", path)
			}
			enabled = append(enabled, text)
		}
	case []string:
		enabled = append(enabled, raw...)
	default:
		return fmt.Errorf("%s: plugins.enabled must be an array of strings", path)
	}
	if !slices.Contains(enabled, "agentpack-bundle") {
		enabled = append(enabled, "agentpack-bundle")
	}
	plugins["enabled"] = enabled
	data, err := toml.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
func linkTrustedPlugin(home, bundle string) error {
	dir := filepath.Join(home, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(dir, "agentpack-bundle")
	if info, err := os.Lstat(dest); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s exists and is not an agentpack symlink", dest)
		}
		if err := os.Remove(dest); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(bundle, dest)
}
func writeAttribution(home string) error {
	path := filepath.Join(home, "AGENTS.md")
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "<!-- agentpack:no-attribution:begin -->") {
		return nil
	}
	output := strings.TrimRight(string(data), "\n")
	if output == "" {
		output = "# AGENTS.md"
	}
	output += "\n\n<!-- agentpack:no-attribution:begin -->\n" + strings.TrimSpace(base.NoAttributionBody) + "\n<!-- agentpack:no-attribution:end -->\n"
	return os.WriteFile(path, []byte(output), 0o644)
}
func writeMCP(entries mcp.Entries, ctx base.StageContext) error {
	home, err := paths.StagingGrokHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return err
	}
	return MergeMCP(filepath.Join(home, "config.toml"), entries)
}
func injectGuidance(blob string, ctx base.StageContext) error {
	home, err := paths.StagingGrokHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return err
	}
	return base.WriteGuidance(filepath.Join(home, "AGENTS.md"), blob)
}
func verify(ctx base.StageContext) error {
	home, err := paths.StagingGrokHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return err
	}
	bundle, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(home, "config.toml"), filepath.Join(bundle, "plugin.json")} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("grok staging missing %s: %w", path, err)
		}
	}
	target, err := os.Readlink(filepath.Join(home, "plugins", "agentpack-bundle"))
	if err != nil || target != bundle {
		return fmt.Errorf("grok staging plugin is not the current mode bundle")
	}
	config, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		return err
	}
	var settings struct {
		Plugins struct {
			Enabled []string `toml:"enabled"`
		} `toml:"plugins"`
	}
	if err := toml.Unmarshal(config, &settings); err != nil {
		return err
	}
	if !slices.Contains(settings.Plugins.Enabled, "agentpack-bundle") {
		return fmt.Errorf("grok staging config does not enable agentpack-bundle")
	}
	if native, ok := nativeHome(); ok {
		if err := verifyHistory(home, native); err != nil {
			return fmt.Errorf("grok durable session link does not resolve to native sessions")
		}
	}
	return nil
}
func keepAttribution() bool {
	switch os.Getenv("AGENTPACK_KEEP_ATTRIBUTION") {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
