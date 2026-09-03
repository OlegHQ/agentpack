package mode

import (
	"fmt"
	"sort"
	"strings"
)

const DefaultName = "default"

func IsReserved(name string) bool { return name == DefaultName }

func ValidateName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errorsf("mode name cannot be empty")
	}
	if strings.IndexFunc(trimmed, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' }) >= 0 {
		return "", errorsf("mode name cannot contain whitespace: %q", trimmed)
	}
	return trimmed, nil
}

type Base string

const (
	BaseAll  Base = "all"
	BaseNone Base = "none"
)

func (base *Base) UnmarshalText(text []byte) error {
	value := Base(text)
	if value != BaseAll && value != BaseNone {
		return errorsf("invalid mode base %q: expected all or none", text)
	}
	*base = value
	return nil
}

type Definition struct {
	Base    Base     `toml:"base,omitempty"`
	Enable  []string `toml:"enable,omitempty"`
	Disable []string `toml:"disable,omitempty"`
}

func ImplicitDefault() Definition { return Definition{Base: BaseAll} }

func (definition *Definition) ApplyDefaults() {
	if definition.Base == "" {
		definition.Base = BaseAll
	}
}

func (definition *Definition) SortAndDeduplicate() {
	definition.Enable = sortedUnique(definition.Enable)
	definition.Disable = sortedUnique(definition.Disable)
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return values
	}
	sort.Strings(values)
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func errorsf(format string, args ...any) error { return fmt.Errorf("mode: "+format, args...) }
