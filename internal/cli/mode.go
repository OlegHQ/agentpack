package cli

import (
	"fmt"

	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/modecatalog"
)

func (runner Runner) runMode(root string, arguments []string, quiet bool) error {
	if len(arguments) == 0 {
		return fmt.Errorf("mode requires an action")
	}
	project, err := manifest.Load(root)
	if err != nil {
		return err
	}
	if project == nil {
		return fmt.Errorf("agentpack.toml required for mode management")
	}
	action, args := arguments[0], arguments[1:]
	var locked *lockfile.PackLock
	if value, loadErr := lockfile.Load(root); loadErr == nil {
		locked = &value
	}
	catalog, err := modecatalog.BuildCapabilityCatalog(root, locked, project)
	if err != nil {
		return err
	}
	switch action {
	case "list":
		if err := noArgs(args); err != nil {
			return err
		}
		for _, name := range project.ListModeNames() {
			definition, _ := project.ModeDefinition(name)
			fmt.Fprintf(runner.Stdout, "%s: base=%s, enable=%d, disable=%d\n", name, definition.Base, len(definition.Enable), len(definition.Disable))
		}
	case "show":
		if len(args) != 1 {
			return fmt.Errorf("mode show requires one name")
		}
		definition, found := project.ModeDefinition(args[0])
		if !found {
			return fmt.Errorf("unknown mode: %s", args[0])
		}
		fmt.Fprintf(runner.Stdout, "mode: %s\nbase: %s\nenable: %q\ndisable: %q\n", args[0], definition.Base, definition.Enable, definition.Disable)
	case "create", "delete":
		if len(args) != 1 {
			return fmt.Errorf("mode %s requires one name", action)
		}
		if action == "create" {
			err = manifest.CreateMode(root, args[0])
		} else {
			err = manifest.DeleteMode(root, args[0])
		}
	case "enable", "disable":
		if len(args) < 1 {
			return fmt.Errorf("mode %s requires a name", action)
		}
		if _, found := project.ModeDefinition(args[0]); !found {
			return fmt.Errorf("unknown mode: %s", args[0])
		}
		for _, raw := range args[1:] {
			selector, parseErr := mode.ParseSelector(raw)
			if parseErr != nil {
				return parseErr
			}
			if validateErr := catalog.Validate(selector); validateErr != nil {
				return validateErr
			}
		}
		err = manifest.AddModeSelectors(root, args[0], action == "enable", args[1:])
	case "base":
		if len(args) != 2 {
			return fmt.Errorf("mode base requires a name and all|none")
		}
		if _, found := project.ModeDefinition(args[0]); !found {
			return fmt.Errorf("unknown mode: %s", args[0])
		}
		base := mode.Base(args[1])
		if base != mode.BaseAll && base != mode.BaseNone {
			return fmt.Errorf("invalid mode base %q", args[1])
		}
		err = manifest.SetModeBase(root, args[0], base)
	case "tui":
		return fmt.Errorf("mode TUI is not ported yet")
	default:
		return fmt.Errorf("unknown mode action %q", action)
	}
	if err == nil && !quiet && action != "list" && action != "show" {
		fmt.Fprintf(runner.Stdout, "Updated mode %q.\n", first(args))
	}
	return err
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
