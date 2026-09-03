package modetui

import (
	"reflect"
	"testing"

	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mode"
)

func TestStateReducerProtectsDefaultAndMovesSelectors(t *testing.T) {
	state, err := LoadState(&manifest.Manifest{Modes: map[string]mode.Definition{}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Delete(); err == nil {
		t.Fatal("default deletion should fail")
	}
	if err := state.Create("design"); err != nil {
		t.Fatal(err)
	}
	if err := state.Apply("mcp:filesystem", true); err != nil {
		t.Fatal(err)
	}
	if err := state.Apply("mcp:filesystem", true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.Definition().Enable, []string{"mcp:filesystem"}) {
		t.Fatalf("enable=%#v", state.Definition().Enable)
	}
	if err := state.Apply("mcp:filesystem", false); err != nil {
		t.Fatal(err)
	}
	if len(state.Definition().Enable) != 0 || !reflect.DeepEqual(state.Definition().Disable, []string{"mcp:filesystem"}) {
		t.Fatalf("definition=%#v", state.Definition())
	}
	if err := state.Clear("mcp:filesystem"); err != nil {
		t.Fatal(err)
	}
	if len(state.Definition().Disable) != 0 {
		t.Fatalf("definition=%#v", state.Definition())
	}
}
