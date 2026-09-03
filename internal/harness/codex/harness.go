package codex

import (
	"fmt"
	"os"
	"path/filepath"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func New() base.Harness {
	return base.Definition{Target: base.Codex, Root: stagedRoot, BeforeReset: preReset, Setup: prepare, MCP: writeMCP, Guidance: injectGuidance, Check: verify}
}
func stagedRoot(ctx base.StageContext) (string, error) {
	return paths.StagingCodexHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
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
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if home, err := os.UserHomeDir(); err == nil {
		native := filepath.Join(home, ".codex")
		if err := base.CopySelectedEntries(native, root, []string{"config.toml", "hooks.json", "skills", "themes"}); err != nil {
			return err
		}
		if err := prepareAuth(native, root); err != nil {
			return err
		}
	}
	if err := forceAuthFileStore(root); err != nil {
		return err
	}
	if err := prepareMCPAuth(ctx.ProjectRoot, root); err != nil {
		return err
	}
	if native, ok := nativeHome(); ok {
		if err := prepareHistory(root, native); err != nil {
			return err
		}
	}
	if !keepAttribution() {
		if err := updateConfig(filepath.Join(root, "config.toml"), func(config map[string]any) { config["commit_attribution"] = "" }); err != nil {
			return err
		}
	}
	return nil
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
