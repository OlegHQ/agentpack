package grok

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/pelletier/go-toml/v2"
)

func New() base.Harness {
	return base.Definition{Target: base.Grok, Root: stagedRoot, Reset: resetPaths, BeforeReset: preReset, Setup: prepare, MCP: writeMCP, Guidance: injectGuidance, Check: verify}
}
func stagedRoot(ctx base.StageContext) (string, error) {
	return paths.StagingGrokBundleDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
}
func resetPaths(ctx base.StageContext) ([]string, error) {
	home, err := paths.StagingGrokHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	root, err := paths.StagingGrokDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return nil, err
	}
	return []string{home, root}, nil
}
func preReset(ctx base.StageContext) error { return recoverHistory(ctx.ProjectRoot, ctx.Mode.Name()) }
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
	if home, err := os.UserHomeDir(); err == nil {
		native := filepath.Join(home, ".grok")
		if err := base.CopySelectedEntries(native, staged, []string{"config.toml", "skills", "agents", "commands", "plugins"}); err != nil {
			return err
		}
		for _, name := range []string{"auth.json", "mcp_credentials.json"} {
			source, dest := filepath.Join(native, name), filepath.Join(staged, name)
			if info, err := os.Stat(source); err == nil && info.Mode().IsRegular() {
				_ = os.Remove(dest)
				if err := os.Symlink(source, dest); err != nil {
					data, readErr := os.ReadFile(source)
					if readErr != nil {
						return readErr
					}
					if err := os.WriteFile(dest, data, 0o600); err != nil {
						return err
					}
				}
			}
		}
	}
	return ensurePluginPath(filepath.Join(staged, "config.toml"), bundle)
}
func ensurePluginPath(path, bundle string) error {
	root := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &root); err != nil {
			return err
		}
	}
	plugins, ok := root["plugins"].(map[string]any)
	if root["plugins"] != nil && !ok {
		return fmt.Errorf("%s: plugins must be a TOML table", path)
	}
	if plugins == nil {
		plugins = make(map[string]any)
		root["plugins"] = plugins
	}
	var values []string
	if raw, ok := plugins["paths"].([]any); ok {
		for _, v := range raw {
			if text, ok := v.(string); ok {
				values = append(values, text)
			}
		}
	} else if raw, ok := plugins["paths"].([]string); ok {
		values = append(values, raw...)
	} else if plugins["paths"] != nil {
		return fmt.Errorf("%s: plugins.paths must be an array", path)
	}
	found := false
	for _, v := range values {
		if v == bundle {
			found = true
		}
	}
	if !found {
		values = append(values, bundle)
	}
	plugins["paths"] = values
	data, err := toml.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
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
	if native, ok := nativeHome(); ok {
		if err := verifyHistory(home, native); err != nil {
			return fmt.Errorf("Grok durable session link does not resolve to native sessions")
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
