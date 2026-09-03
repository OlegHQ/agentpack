package modetui

import (
	"strings"
	"testing"

	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/modecatalog"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestModeEditorRendersResponsiveThreePaneInterface(t *testing.T) {
	state, err := LoadState(&manifest.Manifest{Modes: map[string]mode.Definition{
		"focused": {Base: mode.BaseNone, Enable: []string{"mcp:docs"}},
	}}, "focused")
	if err != nil {
		t.Fatal(err)
	}
	catalog := modecatalog.CapabilityCatalog{
		PackageModules: map[string]struct{}{"github.com/acme/tools": {}},
		PackagePaths: map[string]map[string]struct{}{
			"github.com/acme/tools": {"skills/review/SKILL.md": {}},
		},
		MCPNames:       map[string]struct{}{"docs": {}},
		DotAgentsPaths: map[string]struct{}{"rules/backend.mdc": {}},
	}
	app := newApplication("/work/demo", state, catalog)
	app.Update(tea.WindowSizeMsg{Width: 140, Height: 34})
	view := ansi.Strip(app.View())
	for _, expected := range []string{
		"agentpack", "MODE EDITOR", "project  demo", "Modes", "Capability tree", "Details",
		"focused", "Base  none", "MCP servers", "docs", "Enabled (1)",
		"Tab capabilities", "s save", "? help",
	} {
		if !strings.Contains(view, expected) {
			t.Errorf("rendered TUI missing %q:\n%s", expected, view)
		}
	}
	if got := strings.Count(view, "\n") + 1; got > 34 {
		t.Errorf("rendered TUI exceeds terminal height: %d lines", got)
	}
}

func TestModeEditorKeyboardWorkflowMatchesRustTUI(t *testing.T) {
	state, err := LoadState(&manifest.Manifest{Modes: map[string]mode.Definition{}}, "")
	if err != nil {
		t.Fatal(err)
	}
	app := newApplication(t.TempDir(), state, modecatalog.CapabilityCatalog{MCPNames: map[string]struct{}{"docs": {}}})

	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	for _, character := range "focused" {
		app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}})
	}
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if app.state.Selected != "focused" || !app.state.Dirty {
		t.Fatalf("new mode state = %#v", app.state)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyTab})
	app.Update(tea.KeyMsg{Type: tea.KeyDown})
	app.Update(tea.KeyMsg{Type: tea.KeySpace})
	if got := app.state.Definition().Enable; len(got) != 1 || got[0] != "mcp:docs" {
		t.Fatalf("first cycle enable = %v", got)
	}
	app.Update(tea.KeyMsg{Type: tea.KeySpace})
	if got := app.state.Definition().Disable; len(got) != 1 || got[0] != "mcp:docs" {
		t.Fatalf("second cycle disable = %v", got)
	}
	app.Update(tea.KeyMsg{Type: tea.KeySpace})
	if len(app.state.Definition().Enable)+len(app.state.Definition().Disable) != 0 {
		t.Fatalf("third cycle did not restore inherited state: %#v", app.state.Definition())
	}
}

func TestThemeOverrideProducesFixedPalette(t *testing.T) {
	t.Setenv("AGENTPACK_TUI_THEME", "light")
	light := newPalette()
	if light.accent.Light != light.accent.Dark {
		t.Fatalf("light override remains adaptive: %#v", light.accent)
	}
	t.Setenv("AGENTPACK_TUI_THEME", "dark")
	dark := newPalette()
	if dark.accent.Light != dark.accent.Dark || dark.accent == light.accent {
		t.Fatalf("dark override not applied: %#v", dark.accent)
	}
}
