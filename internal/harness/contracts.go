package harness

import (
	"fmt"
	"os/exec"

	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/mode"
)

type StageContext struct {
	ProjectRoot    string
	WorkspaceRoot  string
	Mode           mode.Effective
	LaunchTarget   *Target
	StagedRoots    map[Target]string
	StrictExternal bool
}

type LaunchContext struct {
	ProjectRoot    string
	WorkspaceRoot  string
	Arguments      []string
	Mode           mode.Effective
	Yolo           bool
	StrictExternal bool
	Command        *exec.Cmd
}

// RebuildTransaction keeps a newly staged root private until all staging and
// verification succeeds. Abort must leave the previously published root intact.
type RebuildTransaction interface {
	Root() string
	Commit() error
	Abort() error
}

type Harness interface {
	ID() Target
	StagedRoot(StageContext) (string, error)
	ResetPaths(StageContext) ([]string, error)
	PreReset(StageContext) error
	BeginRebuild(StageContext) (RebuildTransaction, error)
	Prepare(StageContext) error
	WriteMCP(mcp.Entries, StageContext) error
	InjectGuidance(string, StageContext) error
	Finalize(mcp.Entries, StageContext) error
	FinalizeWorkspaceOverlay(StageContext) error
	Verify(StageContext) error
	LaunchCommand(LaunchContext) (*exec.Cmd, error)
	AfterLaunch(LaunchContext) error
}

type Definition struct {
	Target           Target
	Root             func(StageContext) (string, error)
	Reset            func(StageContext) ([]string, error)
	BeforeReset      func(StageContext) error
	Begin            func(StageContext) (RebuildTransaction, error)
	Setup            func(StageContext) error
	MCP              func(mcp.Entries, StageContext) error
	Guidance         func(string, StageContext) error
	AfterStage       func(mcp.Entries, StageContext) error
	WorkspaceOverlay func(StageContext) error
	Check            func(StageContext) error
	Launch           func(LaunchContext) (*exec.Cmd, error)
	LaunchFinished   func(LaunchContext) error
}

func (definition Definition) ID() Target { return definition.Target }
func (definition Definition) StagedRoot(ctx StageContext) (string, error) {
	return definition.Root(ctx)
}
func (definition Definition) ResetPaths(ctx StageContext) ([]string, error) {
	if definition.Reset != nil {
		return definition.Reset(ctx)
	}
	root, err := definition.Root(ctx)
	if err != nil {
		return nil, err
	}
	return []string{root}, nil
}
func (definition Definition) PreReset(ctx StageContext) error {
	if definition.BeforeReset != nil {
		return definition.BeforeReset(ctx)
	}
	return nil
}
func (definition Definition) BeginRebuild(ctx StageContext) (RebuildTransaction, error) {
	if definition.Begin != nil {
		return definition.Begin(ctx)
	}
	return nil, nil
}
func (definition Definition) Prepare(ctx StageContext) error {
	if definition.Setup == nil {
		return fmt.Errorf("%s harness has no prepare function", definition.Target)
	}
	return definition.Setup(ctx)
}
func (definition Definition) WriteMCP(entries mcp.Entries, ctx StageContext) error {
	if definition.MCP != nil {
		return definition.MCP(entries, ctx)
	}
	return nil
}
func (definition Definition) InjectGuidance(blob string, ctx StageContext) error {
	if definition.Guidance != nil {
		return definition.Guidance(blob, ctx)
	}
	return nil
}
func (definition Definition) Finalize(entries mcp.Entries, ctx StageContext) error {
	if definition.AfterStage != nil {
		return definition.AfterStage(entries, ctx)
	}
	return nil
}
func (definition Definition) FinalizeWorkspaceOverlay(ctx StageContext) error {
	if definition.WorkspaceOverlay != nil {
		return definition.WorkspaceOverlay(ctx)
	}
	return nil
}
func (definition Definition) Verify(ctx StageContext) error {
	if definition.Check == nil {
		return fmt.Errorf("%s harness has no verifier", definition.Target)
	}
	return definition.Check(ctx)
}
func (definition Definition) LaunchCommand(ctx LaunchContext) (*exec.Cmd, error) {
	if definition.Launch == nil {
		return nil, fmt.Errorf("%s harness has no launcher", definition.Target)
	}
	return definition.Launch(ctx)
}
func (definition Definition) AfterLaunch(ctx LaunchContext) error {
	if definition.LaunchFinished != nil {
		return definition.LaunchFinished(ctx)
	}
	return nil
}
