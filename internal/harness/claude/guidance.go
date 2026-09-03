package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func InjectGuidance(bundle, blob string) error {
	guidancePath := filepath.Join(bundle, "_agentpack", "guidance.md")
	if err := os.MkdirAll(filepath.Dir(guidancePath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(guidancePath, []byte(blob), 0o644); err != nil {
		return err
	}
	return addGuidanceHook(bundle, guidancePath)
}

func addGuidanceHook(bundle, guidancePath string) error {
	hooksPath := filepath.Join(bundle, "hooks", "hooks.json")
	root := map[string]any{"hooks": map[string]any{}}
	if data, err := os.ReadFile(hooksPath); err == nil {
		if json.Unmarshal(data, &root) != nil {
			root = map[string]any{"hooks": map[string]any{}}
		}
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s: hooks not a JSON object", hooksPath)
	}
	var session []any
	if value, exists := hooks["SessionStart"]; exists {
		var arrayOK bool
		session, arrayOK = value.([]any)
		if !arrayOK {
			return fmt.Errorf("%s: SessionStart not an array", hooksPath)
		}
	}
	kept := session[:0]
	for _, group := range session {
		encoded, _ := json.Marshal(group)
		if !strings.Contains(string(encoded), "agentpack hook-exec inject-guidance") {
			kept = append(kept, group)
		}
	}
	command := "agentpack hook-exec inject-guidance --target claude --event SessionStart --file " + shellQuote(guidancePath)
	hooks["SessionStart"] = append(kept, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}})
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(hooksPath, data, 0o644)
}

func shellQuote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n'\"\\$`!&|;()<>*?[]{}") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
