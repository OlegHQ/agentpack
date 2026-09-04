package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/tailscale/hujson"
)

var packSubdirectories = []string{"commands", "agents", "skills", "rules", "hooks", "assets", "scripts"}
var mutableFiles = []string{"machineid", "agent-cli-state.json", "argv.json", "ide_state.json"}

func materializeFakeHome(ctx base.StageContext) error {
	fake, err := paths.StagingCursorHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return err
	}
	if err := os.RemoveAll(fake); err != nil {
		return err
	}
	fakeCursor := filepath.Join(fake, ".cursor")
	if err := os.MkdirAll(fakeCursor, 0o755); err != nil {
		return err
	}
	pack, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	for _, name := range packSubdirectories {
		if err := linkOrCopyPresent(filepath.Join(pack, name), filepath.Join(fakeCursor, name)); err != nil {
			return err
		}
	}
	home, homeErr := os.UserHomeDir()
	var realCursor string
	if homeErr == nil {
		realCursor = filepath.Join(home, ".cursor")
	}
	if err := mergeMCPFiles(filepath.Join(pack, "mcp.json"), filepath.Join(realCursor, "mcp.json"), filepath.Join(fakeCursor, "mcp.json")); err != nil {
		return err
	}
	if err := mergeHookFiles(filepath.Join(pack, "hooks", "hooks.json"), filepath.Join(realCursor, "hooks.json"), filepath.Join(fakeCursor, "hooks.json")); err != nil {
		return err
	}
	if realCursor != "" {
		for _, name := range mutableFiles {
			if err := linkMutableFile(filepath.Join(realCursor, name), filepath.Join(fakeCursor, name)); err != nil {
				return err
			}
		}
	}
	var cliSource string
	if realCursor != "" {
		cliSource = filepath.Join(realCursor, "cli-config.json")
	}
	if err := forceFakeAttribution(fakeCursor, cliSource); err != nil {
		return err
	}
	if homeErr == nil {
		if err := materializeUserStorage(fake, fakeCursor, home, realCursor); err != nil {
			return err
		}
		if err := materializePlatformData(fake, home); err != nil {
			return err
		}
	}
	return nil
}

func linkOrCopyPresent(source, destination string) error {
	info, err := os.Stat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(source, destination); err == nil {
		return nil
	}
	if info.IsDir() {
		return base.CopySelectedEntries(source, destination, entriesIn(source))
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, info.Mode().Perm())
}
func entriesIn(root string) []string {
	entries, _ := os.ReadDir(root)
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result
}
func linkMutableFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		return err
	}
	_ = os.Remove(destination)
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(source); err != nil {
			return nil
		}
	}
	return os.Symlink(source, destination)
}
func readMCP(path string) (mcp.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return mcp.Config{}, err
	}
	data, err = hujson.Standardize(append(data, '\n'))
	if err != nil {
		return mcp.Config{}, err
	}
	var config mcp.Config
	err = json.Unmarshal(data, &config)
	return config, err
}
func mergeMCPFiles(pack, user, destination string) error {
	_, packErr := os.Stat(pack)
	_, userErr := os.Stat(user)
	if packErr != nil && userErr != nil {
		return nil
	}
	config := mcp.Config{Servers: map[string]mcp.Server{}}
	if packErr == nil {
		value, err := readMCP(pack)
		if err != nil {
			return err
		}
		for name, server := range value.Servers {
			config.Servers[name] = server
		}
	}
	if userErr == nil {
		value, err := readMCP(user)
		if err != nil {
			return err
		}
		for name, server := range value.Servers {
			config.Servers[name] = server
		}
	}
	return writeJSON(destination, config)
}
func mergeHookFiles(pack, user, destination string) error {
	merged := make(map[string][]any)
	for _, path := range []string{user, pack} {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		var root struct {
			Hooks map[string][]any `json:"hooks"`
		}
		data, err = hujson.Standardize(append(data, '\n'))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &root); err != nil {
			return err
		}
		for event, entries := range root.Hooks {
			merged[event] = append(merged[event], entries...)
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return writeJSON(destination, map[string]any{"version": 1, "hooks": merged})
}
func patchAttribution(value map[string]any) map[string]any {
	attribution, ok := value["attribution"].(map[string]any)
	if !ok {
		attribution = make(map[string]any)
		value["attribution"] = attribution
	}
	attribution["attributeCommitsToAgent"] = false
	attribution["attributePRsToAgent"] = false
	return value
}
func forceFakeAttribution(fakeCursor, source string) error {
	if keepAttribution() {
		return nil
	}
	value := make(map[string]any)
	if data, err := os.ReadFile(source); err == nil {
		_ = json.Unmarshal(data, &value)
	}
	return writeJSON(filepath.Join(fakeCursor, "cli-config.json"), patchAttribution(value))
}
func materializeUserStorage(fake, fakeCursor, home, realCursor string) error {
	fakeUser := filepath.Join(fakeCursor, "User")
	if err := os.MkdirAll(fakeUser, 0o755); err != nil {
		return err
	}
	electron := electronUserDirectory(home)
	for _, name := range []string{"globalStorage", "workspaceStorage"} {
		source := filepath.Join(electron, name)
		if _, err := os.Stat(source); os.IsNotExist(err) {
			legacy := filepath.Join(realCursor, "User", name)
			if _, legacyErr := os.Stat(legacy); legacyErr == nil {
				source = legacy
			} else if err := os.MkdirAll(source, 0o755); err != nil {
				return err
			}
		}
		if err := linkOrCopyPresent(source, filepath.Join(fakeUser, name)); err != nil {
			return err
		}
	}
	return nil
}
func electronUserDirectory(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Cursor", "User")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Cursor", "User")
	default:
		basePath := os.Getenv("XDG_CONFIG_HOME")
		if basePath == "" {
			basePath = filepath.Join(home, ".config")
		}
		return filepath.Join(basePath, "Cursor", "User")
	}
}
func materializePlatformData(fake, home string) error {
	var pairs [][2]string
	switch runtime.GOOS {
	case "darwin":
		pairs = [][2]string{{filepath.Join(home, "Library", "Keychains"), filepath.Join(fake, "Library", "Keychains")}, {filepath.Join(home, "Library", "Application Support", "Cursor"), filepath.Join(fake, "Library", "Application Support", "Cursor")}}
	case "windows":
		pairs = [][2]string{{filepath.Join(home, "AppData", "Roaming", "Cursor"), filepath.Join(fake, "AppData", "Roaming", "Cursor")}}
	default:
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		pairs = [][2]string{{filepath.Join(config, "Cursor"), filepath.Join(fake, ".config", "Cursor")}, {filepath.Join(config, "cursor"), filepath.Join(fake, ".config", "cursor")}, {filepath.Join(data, "Cursor"), filepath.Join(fake, ".local", "share", "Cursor")}}
	}
	for _, pair := range pairs {
		if err := os.MkdirAll(pair[0], 0o755); err != nil {
			return err
		}
		if err := linkOrCopyPresent(pair[0], pair[1]); err != nil {
			return err
		}
	}
	return nil
}
