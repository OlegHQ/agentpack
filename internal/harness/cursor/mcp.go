package cursor

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/OlegHQ/agentpack/internal/mcp"
)

func WriteMCP(path string, entries mcp.Entries) error {
	data, err := json.MarshalIndent(mcp.Config{Servers: entries.Bare()}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
