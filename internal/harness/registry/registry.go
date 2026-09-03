package registry

import (
	"fmt"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/harness/agy"
	"github.com/OlegHQ/agentpack/internal/harness/claude"
	"github.com/OlegHQ/agentpack/internal/harness/codex"
	"github.com/OlegHQ/agentpack/internal/harness/cursor"
	"github.com/OlegHQ/agentpack/internal/harness/grok"
	"github.com/OlegHQ/agentpack/internal/harness/opencode"
	"github.com/OlegHQ/agentpack/internal/hooks"
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
	default:
		return nil
	}
}
