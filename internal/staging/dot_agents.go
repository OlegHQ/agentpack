package staging

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/OlegHQ/agentpack/internal/artifacts"
	"github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/paths"
)

func StageDotAgents(projectRoot, modeName string, effective mode.Effective) error {
	dotRoot := paths.ProjectDotAgentsDir(projectRoot)
	if info, err := os.Stat(dotRoot); err != nil || !info.IsDir() {
		return nil
	}
	plugins, err := paths.StagingPluginsDirForMode(projectRoot, modeName)
	if err != nil {
		return err
	}
	bundle := filepath.Join(plugins, paths.StagedAgentpackBundleName)
	codex, err := paths.StagingCodexHomeDirForMode(projectRoot, modeName)
	if err != nil {
		return err
	}
	for _, overlay := range []struct{ source, destination string }{{"claude", bundle}, {"codex", codex}} {
		if err := copyDotTree(dotRoot, overlay.source, overlay.destination, true, effective); err != nil {
			return err
		}
	}
	if err := mergeDotRules(dotRoot, filepath.Join(bundle, "rules"), effective); err != nil {
		return err
	}
	for _, destination := range []string{bundle, codex} {
		if err := copyDotTree(dotRoot, "skills", destination, false, effective); err != nil {
			return err
		}
	}
	for _, source := range []string{"agents", "commands"} {
		if err := copyDotTree(dotRoot, source, bundle, false, effective); err != nil {
			return err
		}
		if err := renderDotAsCodexSkills(dotRoot, source, codex, effective); err != nil {
			return err
		}
	}
	if err := copyDotFile(dotRoot, "AGENTS.md", filepath.Join(codex, "AGENTS.md"), effective); err != nil {
		return err
	}
	return copyDotFile(dotRoot, "CLAUDE.md", filepath.Join(bundle, "CLAUDE.md"), effective)
}

func copyDotTree(dotRoot, sourceRelative, destinationRoot string, stripPrefix bool, effective mode.Effective) error {
	sourceRoot := filepath.Join(dotRoot, sourceRelative)
	if info, err := os.Stat(sourceRoot); err != nil || !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		selectorRelative := filepath.ToSlash(filepath.Join(sourceRelative, relative))
		allowed, err := effective.AllowsDotAgentsPath(selectorRelative)
		if err != nil || !allowed {
			return err
		}
		destinationRelative := relative
		if !stripPrefix {
			destinationRelative = filepath.Join(sourceRelative, relative)
		}
		return linkOrCopy(path, filepath.Join(destinationRoot, destinationRelative))
	})
}

func mergeDotRules(dotRoot, destinationRoot string, effective mode.Effective) error {
	rulesRoot := filepath.Join(dotRoot, "rules")
	if info, err := os.Stat(rulesRoot); err != nil || !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(rulesRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".mdc") {
			return nil
		}
		relative, err := filepath.Rel(rulesRoot, path)
		if err != nil {
			return err
		}
		allowed, err := effective.AllowsDotAgentsPath(filepath.ToSlash(filepath.Join("rules", relative)))
		if err != nil || !allowed {
			return err
		}
		flattened := strings.ReplaceAll(filepath.ToSlash(relative), "/", "--")
		return linkOrCopy(path, filepath.Join(destinationRoot, "dot-agents--"+flattened))
	})
}

func renderDotAsCodexSkills(dotRoot, sourceRelative, destinationRoot string, effective mode.Effective) error {
	sourceRoot := filepath.Join(dotRoot, sourceRelative)
	if info, err := os.Stat(sourceRoot); err != nil || !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".md" && extension != ".mdc" {
			return nil
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		selectorRelative := filepath.ToSlash(filepath.Join(sourceRelative, relative))
		allowed, err := effective.AllowsDotAgentsPath(selectorRelative)
		if err != nil || !allowed {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		artifact, err := artifacts.Parse(selectorRelative, string(contents), "")
		if err != nil {
			log.Printf("warning: skipping dot-agents artifact %s: %v", selectorRelative, err)
			return nil
		}
		if artifact == nil {
			return nil
		}
		rendered := artifact.Render(harness.Codex)
		return writeFile(filepath.Join(destinationRoot, filepath.FromSlash(rendered.RelativePath)), []byte(rendered.Contents))
	})
}

func copyDotFile(dotRoot, sourceRelative, destination string, effective mode.Effective) error {
	source := filepath.Join(dotRoot, sourceRelative)
	if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	allowed, err := effective.AllowsDotAgentsPath(sourceRelative)
	if err != nil || !allowed {
		return err
	}
	return linkOrCopy(source, destination)
}

func linkOrCopy(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(source, destination); err == nil {
		return nil
	}
	return copyFile(source, destination)
}
