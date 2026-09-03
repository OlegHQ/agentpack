package opencode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

func MergeMCP(path string, entries mcp.Entries) error {
	root := map[string]any{"$schema": "https://opencode.ai/config.json"}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(stripComments(data), &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	servers, ok := root["mcp"].(map[string]any)
	if root["mcp"] != nil && !ok {
		return fmt.Errorf("%s: mcp must be a JSON object", path)
	}
	if servers == nil {
		servers = make(map[string]any)
		root["mcp"] = servers
	}
	for _, name := range entries.Names() {
		if _, exists := servers[name]; exists {
			continue
		}
		server := entries[name].Server
		value := map[string]any{"enabled": server.Disabled == nil || !*server.Disabled}
		if server.IsRemote() {
			value["type"], value["url"] = "remote", *server.URL
		} else {
			value["type"] = "local"
			command := append([]string(nil), server.Args...)
			if server.Command != nil {
				command = append([]string{*server.Command}, command...)
			}
			value["command"] = command
			if len(server.Env) != 0 {
				value["environment"] = server.Env
			}
		}
		servers[name] = value
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func stripComments(input []byte) []byte {
	var output bytes.Buffer
	inString, escaped := false, false
	for i := 0; i < len(input); {
		if inString {
			output.WriteByte(input[i])
			if escaped {
				escaped = false
			} else if input[i] == '\\' {
				escaped = true
			} else if input[i] == '"' {
				inString = false
			}
			i++
			continue
		}
		if input[i] == '"' {
			inString = true
			output.WriteByte(input[i])
			i++
			continue
		}
		if i+1 < len(input) && input[i] == '/' && input[i+1] == '/' {
			i += 2
			for i < len(input) && input[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(input) && input[i] == '/' && input[i+1] == '*' {
			i += 2
			for i+1 < len(input) && !(input[i] == '*' && input[i+1] == '/') {
				i++
			}
			if i+1 < len(input) {
				i += 2
			}
			continue
		}
		output.WriteByte(input[i])
		i++
	}
	return output.Bytes()
}
