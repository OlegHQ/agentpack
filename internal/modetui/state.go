package modetui

import (
	"fmt"
	"sort"

	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mode"
)

type SelectorState uint8

const (
	Neutral SelectorState = iota
	ExplicitEnable
	ExplicitDisable
)

type State struct {
	Selected string
	Modes    map[string]mode.Definition
	Dirty    bool
}

func LoadState(project *manifest.Manifest, selected string) (State, error) {
	if selected == "" {
		selected = mode.DefaultName
	}
	modes := make(map[string]mode.Definition, len(project.Modes)+1)
	for name, definition := range project.Modes {
		modes[name] = cloneDefinition(definition)
	}
	if _, found := modes[mode.DefaultName]; !found {
		modes[mode.DefaultName] = mode.ImplicitDefault()
	}
	if _, found := modes[selected]; !found {
		return State{}, fmt.Errorf("mode: unknown mode: %s", selected)
	}
	return State{Selected: selected, Modes: modes}, nil
}

func (state *State) Definition() mode.Definition { return state.Modes[state.Selected] }
func (state *State) ReadOnly() bool              { return mode.IsReserved(state.Selected) }
func (state *State) Names() []string {
	names := make([]string, 0, len(state.Modes))
	for name := range state.Modes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (state *State) Create(name string) error {
	name, err := mode.ValidateName(name)
	if err != nil {
		return err
	}
	if _, found := state.Modes[name]; found {
		return fmt.Errorf("mode: mode already exists: %s", name)
	}
	state.Modes[name] = mode.Definition{Base: mode.BaseAll}
	state.Selected, state.Dirty = name, true
	return nil
}
func (state *State) Rename(name string) error {
	if state.ReadOnly() {
		return fmt.Errorf("mode: default is reserved and cannot be renamed")
	}
	name, err := mode.ValidateName(name)
	if err != nil {
		return err
	}
	if _, found := state.Modes[name]; found {
		return fmt.Errorf("mode: mode already exists: %s", name)
	}
	definition := state.Modes[state.Selected]
	delete(state.Modes, state.Selected)
	state.Modes[name] = definition
	state.Selected, state.Dirty = name, true
	return nil
}
func (state *State) Delete() error {
	if state.ReadOnly() {
		return fmt.Errorf("mode: default is reserved and cannot be deleted")
	}
	delete(state.Modes, state.Selected)
	state.Selected, state.Dirty = mode.DefaultName, true
	return nil
}
func (state *State) SetBase(base mode.Base) error {
	if state.ReadOnly() {
		return fmt.Errorf("mode: default is read-only")
	}
	definition := state.Definition()
	if definition.Base != base {
		definition.Base = base
		state.Modes[state.Selected], state.Dirty = definition, true
	}
	return nil
}
func (state *State) Apply(raw string, enabled bool) error {
	if state.ReadOnly() {
		return fmt.Errorf("mode: default is read-only")
	}
	selector, err := mode.ParseSelector(raw)
	if err != nil {
		return err
	}
	canonical := selector.CanonicalString()
	definition := state.Definition()
	if enabled {
		definition.Disable = remove(definition.Disable, canonical)
		definition.Enable = add(definition.Enable, canonical)
	} else {
		definition.Enable = remove(definition.Enable, canonical)
		definition.Disable = add(definition.Disable, canonical)
	}
	definition.SortAndDeduplicate()
	state.Modes[state.Selected], state.Dirty = definition, true
	return nil
}
func (state *State) Clear(raw string) error {
	if state.ReadOnly() {
		return fmt.Errorf("mode: default is read-only")
	}
	selector, err := mode.ParseSelector(raw)
	if err != nil {
		return err
	}
	canonical := selector.CanonicalString()
	definition := state.Definition()
	before := len(definition.Enable) + len(definition.Disable)
	definition.Enable, definition.Disable = remove(definition.Enable, canonical), remove(definition.Disable, canonical)
	if before != len(definition.Enable)+len(definition.Disable) {
		state.Modes[state.Selected], state.Dirty = definition, true
	}
	return nil
}
func (state State) SelectorState(canonical string) SelectorState {
	definition := state.Definition()
	if contains(definition.Enable, canonical) {
		return ExplicitEnable
	}
	if contains(definition.Disable, canonical) {
		return ExplicitDisable
	}
	return Neutral
}

func cloneDefinition(value mode.Definition) mode.Definition {
	value.Enable = append([]string(nil), value.Enable...)
	value.Disable = append([]string(nil), value.Disable...)
	return value
}
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func remove(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}
func add(values []string, value string) []string {
	if contains(values, value) {
		return values
	}
	return append(values, value)
}
