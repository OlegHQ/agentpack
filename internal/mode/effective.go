package mode

import "fmt"

type SelectorValidator interface{ ValidateSelector(Selector) error }

type Effective struct {
	name       string
	definition Definition
	enabled    []Selector
	disabled   []Selector
}

func NewEffective(name string, definition Definition, validator SelectorValidator) (Effective, error) {
	definition.ApplyDefaults()
	effective := Effective{name: name, definition: definition}
	for _, raw := range definition.Enable {
		selector, err := ParseSelector(raw)
		if err != nil {
			return Effective{}, err
		}
		if validator != nil {
			if err := validator.ValidateSelector(selector); err != nil {
				return Effective{}, err
			}
		}
		effective.enabled = append(effective.enabled, selector)
	}
	for _, raw := range definition.Disable {
		selector, err := ParseSelector(raw)
		if err != nil {
			return Effective{}, err
		}
		if validator != nil {
			if err := validator.ValidateSelector(selector); err != nil {
				return Effective{}, err
			}
		}
		effective.disabled = append(effective.disabled, selector)
	}
	return effective, nil
}

func ImplicitEffective() Effective {
	effective, _ := NewEffective(DefaultName, ImplicitDefault(), nil)
	return effective
}

func (mode Effective) Name() string           { return mode.name }
func (mode Effective) Definition() Definition { return mode.definition }
func (mode Effective) Base() Base             { return mode.definition.Base }

func (mode Effective) AllowsPackagePath(module, relativePath string) (bool, error) {
	enabled, err := packageMatches(mode.enabled, module, relativePath)
	if err != nil {
		return false, err
	}
	disabled, err := packageMatches(mode.disabled, module, relativePath)
	if err != nil {
		return false, err
	}
	return mode.resolvePath(enabled, disabled), nil
}

func (mode Effective) AllowsDotAgentsPath(relativePath string) (bool, error) {
	enabled, err := dotAgentsMatches(mode.enabled, relativePath)
	if err != nil {
		return false, err
	}
	disabled, err := dotAgentsMatches(mode.disabled, relativePath)
	if err != nil {
		return false, err
	}
	return mode.resolvePath(enabled, disabled), nil
}

func (mode Effective) AllowsMCP(name string) bool {
	enabled, disabled := false, false
	for _, selector := range mode.enabled {
		enabled = enabled || selector.MatchesMCP(name)
	}
	for _, selector := range mode.disabled {
		disabled = disabled || selector.MatchesMCP(name)
	}
	if disabled {
		return false
	}
	if enabled {
		return true
	}
	return mode.definition.Base == BaseAll
}

func (mode Effective) FingerprintMaterial() string {
	definition := mode.definition
	definition.SortAndDeduplicate()
	return fmt.Sprintf("mode=%s\nbase=%s\nenable=%q\ndisable=%q\n", mode.name, definition.Base, definition.Enable, definition.Disable)
}

func (mode Effective) resolvePath(enabled, disabled []MatchSpecificity) bool {
	bestEnabled, hasEnabled := maximum(enabled)
	bestDisabled, hasDisabled := maximum(disabled)
	switch {
	case hasEnabled && hasDisabled && bestEnabled > bestDisabled:
		return true
	case hasEnabled && hasDisabled:
		return false
	case hasEnabled:
		return true
	case hasDisabled:
		return false
	default:
		return mode.definition.Base == BaseAll
	}
}

func packageMatches(selectors []Selector, module, path string) ([]MatchSpecificity, error) {
	var matches []MatchSpecificity
	for _, selector := range selectors {
		specificity, matched, err := selector.MatchesPackagePath(module, path)
		if err != nil {
			return nil, err
		}
		if matched {
			matches = append(matches, specificity)
		}
	}
	return matches, nil
}

func dotAgentsMatches(selectors []Selector, path string) ([]MatchSpecificity, error) {
	var matches []MatchSpecificity
	for _, selector := range selectors {
		specificity, matched, err := selector.MatchesDotAgentsPath(path)
		if err != nil {
			return nil, err
		}
		if matched {
			matches = append(matches, specificity)
		}
	}
	return matches, nil
}

func maximum(values []MatchSpecificity) (MatchSpecificity, bool) {
	if len(values) == 0 {
		return 0, false
	}
	best := values[0]
	for _, value := range values[1:] {
		if value > best {
			best = value
		}
	}
	return best, true
}
