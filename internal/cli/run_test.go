package cli

import (
	"bytes"
	"context"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/OlegHQ/agentpack/internal/lockfile"
	"github.com/OlegHQ/agentpack/internal/manifest"
)

func TestInitMCPAndModeCommands(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTPACK_HOME", filepath.Join(t.TempDir(), "home"))
	var stdout, stderr bytes.Buffer
	runner := NewRunner()
	runner.Stdout, runner.Stderr = &stdout, &stderr
	if code, err := runner.Run(context.Background(), []string{"--project-root", root, "init", "--name", "demo", "--version", "1.2.3"}); err != nil || code != 0 {
		t.Fatalf("init code=%d err=%v", code, err)
	}
	if code, err := runner.Run(context.Background(), []string{"--project-root", root, "init"}); err == nil || code != 1 {
		t.Fatalf("second init code=%d err=%v", code, err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "agentpack.toml"))
	if err != nil || !strings.Contains(string(manifest), `name = "demo"`) {
		t.Fatalf("manifest=%s err=%v", manifest, err)
	}
	if code, err := runner.Run(context.Background(), []string{"--project-root", root, "mcp", "add", "docs", "--command", "npx", "--args", "-y", "server", "--env", "TOKEN=value", "--no-sync"}); err != nil || code != 0 {
		t.Fatalf("mcp add code=%d err=%v", code, err)
	}
	if code, err := runner.Run(context.Background(), []string{"--project-root", root, "mode", "create", "review"}); err != nil || code != 0 {
		t.Fatalf("mode create code=%d err=%v", code, err)
	}
	if code, err := runner.Run(context.Background(), []string{"--project-root", root, "mode", "base", "review", "none"}); err != nil || code != 0 {
		t.Fatalf("mode base code=%d err=%v", code, err)
	}
}

func TestListShowsSelectorAndResolvedCommit(t *testing.T) {
	root := t.TempDir()
	if err := manifest.WriteStub(root, "demo", "0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := manifest.AppendDependencyPin(root, "github.com/acme/nudge-cli", "dev"); err != nil {
		t.Fatal(err)
	}
	lock := lockfile.EmptyForProject(root)
	lock.Packages = []lockfile.Package{{Module: "github.com/acme/nudge-cli", Kind: lockfile.PackageSkill, Direct: true, Commit: "1234567890abcdef"}}
	if err := lock.Save(root); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	runner := NewRunner()
	runner.Stdout = &output
	if code, err := runner.Run(context.Background(), []string{"--project-root", root, "list"}); err != nil || code != 0 {
		t.Fatalf("list code=%d err=%v", code, err)
	}
	if got := output.String(); !strings.Contains(got, "SELECTOR") || !strings.Contains(got, "github.com/acme/nudge-cli\tskill\tdirect\tdev\t1234567890ab") {
		t.Fatalf("list output=%q", got)
	}
}

func TestProxyIsRejectedOutsideClaude(t *testing.T) {
	runner := NewRunner()
	code, err := runner.Run(context.Background(), []string{"--proxy", "sync"})
	if code != 2 || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestNestedHelpAndGlobalVersionDoNotRequireProject(t *testing.T) {
	var output bytes.Buffer
	runner := NewRunner()
	runner.Stdout = &output
	if code, err := runner.Execute(context.Background(), []string{"mcp", "add", "--help"}); err != nil || code != 0 || !strings.Contains(output.String(), "--command") {
		t.Fatalf("code=%d err=%v output=%q", code, err, output.String())
	}
	output.Reset()
	if code, err := runner.Execute(context.Background(), []string{"sync", "--version"}); err != nil || code != 0 || output.String() != "agentpack "+Version+"\n" {
		t.Fatalf("code=%d err=%v output=%q", code, err, output.String())
	}
	output.Reset()
	if code, err := runner.Execute(context.Background(), []string{"-V"}); err != nil || code != 0 || output.String() != "agentpack "+Version+"\n" {
		t.Fatalf("-V code=%d err=%v output=%q", code, err, output.String())
	}
}

func TestRenderedCommandTreePreservesCursorAgentAlias(t *testing.T) {
	runner := NewRunner()
	command, _, err := runner.rootCommand(nil, new(int)).Find([]string{"cursor-agent"})
	if err != nil || command.Name() != "agent" {
		t.Fatalf("cursor-agent resolved to %q: %v", command.Name(), err)
	}
}

func TestRenderedCLIUsesTerminalDefaultColors(t *testing.T) {
	for _, dark := range []bool{false, true} {
		scheme := monochromeColorScheme(lipgloss.LightDark(dark))
		for name, got := range map[string]color.Color{
			"base": scheme.Base, "title": scheme.Title, "description": scheme.Description,
			"program": scheme.Program, "argument": scheme.Argument, "dimmed argument": scheme.DimmedArgument, "command": scheme.Command,
			"flag": scheme.Flag, "flag default": scheme.FlagDefault, "comment": scheme.Comment,
			"quoted string": scheme.QuotedString, "help": scheme.Help, "dash": scheme.Dash,
			"error details": scheme.ErrorDetails,
		} {
			if _, ok := got.(lipgloss.NoColor); !ok {
				t.Errorf("dark=%t %s overrides the terminal color: %#v", dark, name, got)
			}
		}
		if _, ok := scheme.Codeblock.(lipgloss.NoColor); !ok {
			t.Errorf("dark=%t code block overrides the terminal background: %#v", dark, scheme.Codeblock)
		}
		for _, got := range scheme.ErrorHeader {
			if _, ok := got.(lipgloss.NoColor); !ok {
				t.Errorf("dark=%t error header overrides the terminal color: %#v", dark, scheme.ErrorHeader)
			}
		}
	}
}
