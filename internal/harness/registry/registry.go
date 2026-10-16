package registry

import (
	"fmt"

	base "github.com/olegpustovit/agentpack/internal/harness"
	"github.com/olegpustovit/agentpack/internal/harness/agy"
	"github.com/olegpustovit/agentpack/internal/harness/claude"
	"github.com/olegpustovit/agentpack/internal/harness/codex"
	"github.com/olegpustovit/agentpack/internal/harness/cursor"
	"github.com/olegpustovit/agentpack/internal/harness/grok"
	"github.com/olegpustovit/agentpack/internal/harness/opencode"
	"github.com/olegpustovit/agentpack/internal/hooks"
)

func All() []base.Harness {
	return []base.Harness{claude.New(), cursor.New(), codex.New(), opencode.New(), grok.New(), agy.New()}
}
func ByTarget(target base.Target) (base.Harness, error) {
	for _, candidate := range All() {
		if candidate.ID() == target {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("unknown harness %s", target)
}
func Renderer(target base.Target) hooks.Renderer {
	switch target {
	case base.Claude:
		return claude.HookRenderer{}
	case base.Cursor:
		return cursor.HookRenderer{}
	case base.Codex:
		return codex.HookRenderer{}
	case base.OpenCode:
		return opencode.HookRenderer{}
	case base.Agy:
		return agy.HookRenderer{}
	default:
		return nil
	}
}
