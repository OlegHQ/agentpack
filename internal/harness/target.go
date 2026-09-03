package harness

import "fmt"

type Target string

const (
	Claude   Target = "claude"
	Cursor   Target = "cursor"
	Codex    Target = "codex"
	OpenCode Target = "opencode"
	Grok     Target = "grok"
	Agy      Target = "agy"
)

func AllTargets() []Target { return []Target{Claude, Cursor, Codex, OpenCode, Grok, Agy} }

func ParseTarget(value string) (Target, error) {
	for _, target := range AllTargets() {
		if string(target) == value {
			return target, nil
		}
	}
	return "", fmt.Errorf("unknown harness target %q", value)
}

func (target Target) UsesWorkspaceOverlay() bool { return target == Cursor || target == Agy }
