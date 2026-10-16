package opencode

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/tailscale/hujson"

	base "github.com/olegpustovit/agentpack/internal/harness"
)

// writeLSP adds the language servers to the lsp map of opencode.json.
// OpenCode spells a server {command: [program, args...], extensions, env,
// initialization}; it has no setting for Claude Code's settings, transport,
// timeouts or restart policy, so those are left out with a warning. A server
// already named in the user's opencode.json is kept as the user wrote it.
func writeLSP(servers map[string]json.RawMessage, ctx base.StageContext) error {
	root, err := stagedRoot(ctx)
	if err != nil {
		return err
	}
	return MergeLSP(filepath.Join(root, "opencode.json"), servers)
}

func MergeLSP(path string, servers map[string]json.RawMessage) error {
	config := map[string]any{"$schema": "https://opencode.ai/config.json"}
	if data, err := os.ReadFile(path); err == nil {
		standard, err := hujson.Standardize(append(data, '\n'))
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if err := json.Unmarshal(standard, &config); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if enabled, isBool := config["lsp"].(bool); isBool && !enabled {
		log.Printf("warning: OpenCode: opencode.json turns language servers off (\"lsp\": false); skipping %d language server(s)", len(servers))
		return nil
	}
	existing, _ := config["lsp"].(map[string]any)
	if existing == nil {
		existing = make(map[string]any)
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, found := existing[name]; found {
			continue
		}
		var server struct {
			Command               string            `json:"command"`
			Args                  []string          `json:"args"`
			ExtensionToLanguage   map[string]string `json:"extensionToLanguage"`
			Env                   map[string]string `json:"env"`
			InitializationOptions json.RawMessage   `json:"initializationOptions"`
		}
		if err := json.Unmarshal(servers[name], &server); err != nil {
			return fmt.Errorf("language server %q: %w", name, err)
		}
		var all map[string]json.RawMessage
		_ = json.Unmarshal(servers[name], &all)
		var dropped []string
		for key := range all {
			switch key {
			case "command", "args", "extensionToLanguage", "env", "initializationOptions":
			default:
				dropped = append(dropped, key)
			}
		}
		if len(dropped) != 0 {
			sort.Strings(dropped)
			log.Printf("warning: OpenCode: language server %q: leaving out %v, which OpenCode has no setting for", name, dropped)
		}
		extensions := make([]string, 0, len(server.ExtensionToLanguage))
		for extension := range server.ExtensionToLanguage {
			extensions = append(extensions, extension)
		}
		sort.Strings(extensions)
		value := map[string]any{"command": append([]string{server.Command}, server.Args...), "extensions": extensions}
		if len(server.Env) != 0 {
			value["env"] = server.Env
		}
		if len(server.InitializationOptions) != 0 {
			value["initialization"] = server.InitializationOptions
		}
		existing[name] = value
	}
	config["lsp"] = existing
	return writeConfig(path, config)
}
