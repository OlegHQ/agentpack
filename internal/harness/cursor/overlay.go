package cursor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	base "github.com/olegpustovit/agentpack/internal/harness"
	"github.com/olegpustovit/agentpack/internal/paths"
)

func readOverlayManifest(project string) ([]string, error) {
	path, err := paths.CursorOverlayManifestPath(project)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var values []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			values = append(values, line)
		}
	}
	return values, nil
}
func writeOverlayManifest(project string, values []string) error {
	path, err := paths.CursorOverlayManifestPath(project)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	sort.Strings(values)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(values, "\n")+"\n"), 0o644)
}
func cleanupOverlay(project string) error {
	values, err := readOverlayManifest(project)
	if err != nil {
		return err
	}
	for _, path := range values {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.Mode().IsRegular() {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return writeOverlayManifest(project, nil)
}

// materializeAgentsOverlay links the staged subagents and rules into the
// workspace's .cursor folder. Cursor reads both only from the project: rules
// in a plugin or under ~/.cursor/rules are not applied by cursor-agent. A
// project's own .cursor/agents or .cursor/rules folder is never replaced.
func materializeAgentsOverlay(ctx base.StageContext) error {
	if ctx.StrictExternal {
		return fmt.Errorf("cursor workspace overlay refused under --strict-external")
	}
	pack, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	workspace := ctx.WorkspaceRoot
	if workspace == "" {
		workspace, err = os.Getwd()
		if err != nil {
			workspace = ctx.ProjectRoot
		}
	}
	var linked []string
	for _, name := range []string{"agents", "rules"} {
		destination, err := overlayFolder(filepath.Join(pack, name), filepath.Join(workspace, ".cursor", name))
		if err != nil {
			return err
		}
		if destination != "" {
			linked = append(linked, destination)
		}
	}
	return writeOverlayManifest(ctx.ProjectRoot, linked)
}

// overlayFolder links source to destination when source holds a Markdown
// file and destination is not a folder or file the project owns. It returns
// the path it linked, or "" when it linked nothing.
func overlayFolder(source, destination string) (string, error) {
	entries, err := os.ReadDir(source)
	if err != nil {
		return "", nil
	}
	found := false
	for _, entry := range entries {
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if !entry.IsDir() && (extension == ".md" || extension == ".mdc") {
			found = true
		}
	}
	if !found {
		return "", nil
	}
	if info, err := os.Lstat(destination); err == nil {
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 || info.Mode().IsRegular() {
			return "", nil
		}
		if err := os.Remove(destination); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	if err := os.Symlink(absolute, destination); err != nil {
		if err := base.CopySelectedEntries(source, destination, entriesIn(source)); err != nil {
			return "", err
		}
	}
	return destination, nil
}
