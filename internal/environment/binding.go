package environment

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OlegHQ/agentpack/internal/paths"
	"github.com/pelletier/go-toml/v2"
)

const bindingFileName = "environment.toml"

// Binding associates a workspace checkout with an external definition root.
type Binding struct {
	Environment    string `toml:"environment"`
	DefinitionRoot string `toml:"definition_root"`
	WorkspaceRoot  string `toml:"workspace_root"`
}

func bindingPath(workspaceRoot string) (string, error) {
	return paths.ProjectStateFile(workspaceRoot, bindingFileName)
}

// LoadBinding returns the binding for a workspace, if any.
func LoadBinding(workspaceRoot string) (Binding, bool, error) {
	path, err := bindingPath(workspaceRoot)
	if err != nil {
		return Binding{}, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Binding{}, false, nil
	}
	if err != nil {
		return Binding{}, false, err
	}
	var binding Binding
	if err := toml.Unmarshal(data, &binding); err != nil {
		return Binding{}, false, fmt.Errorf("parse environment binding: %w", err)
	}
	binding.DefinitionRoot = strings.TrimSpace(binding.DefinitionRoot)
	binding.Environment = strings.TrimSpace(binding.Environment)
	binding.WorkspaceRoot = strings.TrimSpace(binding.WorkspaceRoot)
	if binding.DefinitionRoot == "" {
		return Binding{}, false, nil
	}
	return binding, true, nil
}

// SaveBinding writes the workspace → definition association.
func SaveBinding(binding Binding) error {
	if binding.WorkspaceRoot == "" || binding.DefinitionRoot == "" {
		return fmt.Errorf("environment binding requires workspace_root and definition_root")
	}
	workspace, err := filepath.Abs(binding.WorkspaceRoot)
	if err != nil {
		return err
	}
	definition, err := filepath.Abs(binding.DefinitionRoot)
	if err != nil {
		return err
	}
	binding.WorkspaceRoot = workspace
	binding.DefinitionRoot = definition
	path, err := bindingPath(workspace)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := toml.Marshal(binding)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ClearBinding removes the association for a workspace.
func ClearBinding(workspaceRoot string) error {
	path, err := bindingPath(workspaceRoot)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
