package harness

import (
	"fmt"

	"github.com/OlegHQ/agentpack/internal/mcp"
	"github.com/OlegHQ/agentpack/internal/mode"
)

type StageContext struct {
	ProjectRoot  string
	Mode         mode.Effective
	LaunchTarget *Target
}

type Harness interface {
	ID() Target
	StagedRoot(StageContext) (string, error)
	ResetPaths(StageContext) ([]string, error)
	PreReset(StageContext) error
	Prepare(StageContext) error
	WriteMCP(mcp.Entries, StageContext) error
	InjectGuidance(string, StageContext) error
	Finalize(mcp.Entries, StageContext) error
	FinalizeWorkspaceOverlay(StageContext) error
	Verify(StageContext) error
}

type Definition struct {
	Target           Target
	Root             func(StageContext) (string, error)
	Reset            func(StageContext) ([]string, error)
	BeforeReset      func(StageContext) error
	Setup            func(StageContext) error
	MCP              func(mcp.Entries, StageContext) error
	Guidance         func(string, StageContext) error
	AfterStage       func(mcp.Entries, StageContext) error
	WorkspaceOverlay func(StageContext) error
	Check            func(StageContext) error
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
