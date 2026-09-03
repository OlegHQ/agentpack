package modetui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/modecatalog"
	"golang.org/x/term"
)

type focus uint8

const (
	modesFocus focus = iota
	treeFocus
)

type application struct {
	root                   string
	state                  State
	catalog                modecatalog.CapabilityCatalog
	tree                   []Node
	focus                  focus
	modeCursor, treeCursor int
	expanded               map[string]bool
	message                string
}

func Run(root string, project *manifest.Manifest, catalog modecatalog.CapabilityCatalog, selected string) error {
	state, err := LoadState(project, selected)
	if err != nil {
		return err
	}
	app := application{root: root, state: state, catalog: catalog, tree: BuildTree(catalog), expanded: map[string]bool{}}
	for _, node := range app.tree {
		app.expanded[node.ID] = true
	}
	for index, name := range app.state.Names() {
		if name == app.state.Selected {
			app.modeCursor = index
		}
	}
	input, output := os.Stdin, os.Stdout
	if !term.IsTerminal(int(input.Fd())) || !term.IsTerminal(int(output.Fd())) {
		return fmt.Errorf("mode: TUI requires an interactive terminal")
	}
	old, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(input.Fd()), old)
	fmt.Fprint(output, "\x1b[?1049h\x1b[?25l")
	defer fmt.Fprint(output, "\x1b[?25h\x1b[?1049l")
	reader := bufio.NewReader(input)
	for {
		app.render(output)
		key, err := readKey(reader)
		if err != nil {
			return err
		}
		quit, err := app.handle(key, reader, output)
		if err != nil {
			app.message = err.Error()
		}
		if quit {
			return nil
		}
	}
}

func (app *application) handle(key string, reader *bufio.Reader, output io.Writer) (bool, error) {
	if key == "q" || key == "ctrl-c" {
		if !app.state.Dirty {
			return true, nil
		}
		answer, err := app.prompt(reader, output, "Unsaved changes. Save before quitting? [y/n/esc] ", "")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "y":
			if err := app.save(); err != nil {
				return false, err
			}
			return true, nil
		case "n":
			return true, nil
		}
		return false, nil
	}
	if key == "tab" {
		if app.focus == modesFocus {
			app.focus = treeFocus
		} else {
			app.focus = modesFocus
		}
		return false, nil
	}
	if key == "s" {
		return false, app.save()
	}
	if app.focus == modesFocus {
		return false, app.handleModes(key, reader, output)
	}
	return false, app.handleTree(key, reader, output)
}

func (app *application) handleModes(key string, reader *bufio.Reader, output io.Writer) error {
	names := app.state.Names()
	switch key {
	case "up", "k":
		if app.modeCursor > 0 {
			app.modeCursor--
			app.state.Selected = names[app.modeCursor]
		}
	case "down", "j":
		if app.modeCursor+1 < len(names) {
			app.modeCursor++
			app.state.Selected = names[app.modeCursor]
		}
	case "right", "enter":
		app.focus = treeFocus
	case "n":
		value, err := app.prompt(reader, output, "New mode: ", "")
		if err != nil {
			return err
		}
		if value != "" {
			if err := app.state.Create(value); err != nil {
				return err
			}
			app.syncModeCursor()
		}
	case "r":
		value, err := app.prompt(reader, output, "Rename mode: ", app.state.Selected)
		if err != nil {
			return err
		}
		if value != "" {
			if err := app.state.Rename(value); err != nil {
				return err
			}
			app.syncModeCursor()
		}
	case "d", "delete":
		answer, err := app.prompt(reader, output, "Delete "+app.state.Selected+"? [y/N] ", "")
		if err != nil {
			return err
		}
		if strings.EqualFold(answer, "y") {
			if err := app.state.Delete(); err != nil {
				return err
			}
			app.syncModeCursor()
		}
	case "b":
		base := mode.BaseAll
		if app.state.Definition().Base == mode.BaseAll {
			base = mode.BaseNone
		}
		return app.state.SetBase(base)
	}
	return nil
}

func (app *application) handleTree(key string, reader *bufio.Reader, output io.Writer) error {
	rows := Flatten(app.tree, app.expanded)
	if len(rows) == 0 {
		return nil
	}
	if app.treeCursor >= len(rows) {
		app.treeCursor = len(rows) - 1
	}
	row := rows[app.treeCursor]
	switch key {
	case "up", "k":
		if app.treeCursor > 0 {
			app.treeCursor--
		}
	case "down", "j":
		if app.treeCursor+1 < len(rows) {
			app.treeCursor++
		}
	case "left", "h":
		if app.expanded[row.Node.ID] {
			delete(app.expanded, row.Node.ID)
		} else {
			app.focus = modesFocus
		}
	case "right", "l":
		if len(row.Node.Children) != 0 {
			app.expanded[row.Node.ID] = true
		}
	case "enter", " ":
		if row.Node.Selector == nil {
			app.expanded[row.Node.ID] = !app.expanded[row.Node.ID]
			break
		}
		canonical := row.Node.Selector.CanonicalString()
		switch app.state.SelectorState(canonical) {
		case Neutral:
			return app.state.Apply(canonical, true)
		case ExplicitEnable:
			return app.state.Apply(canonical, false)
		default:
			return app.state.Clear(canonical)
		}
	case "e":
		if row.Node.Selector != nil {
			return app.state.Apply(row.Node.Selector.CanonicalString(), true)
		}
	case "x":
		if row.Node.Selector != nil {
			return app.state.Apply(row.Node.Selector.CanonicalString(), false)
		}
	case "c":
		if row.Node.Selector != nil {
			return app.state.Clear(row.Node.Selector.CanonicalString())
		}
	case "E", "X":
		if row.Node.Selector != nil {
			selectors := CollectSelectors(row.Node)
			for _, selector := range selectors[1:] {
				_ = app.state.Clear(selector)
			}
			return app.state.Apply(selectors[0], key == "E")
		}
	case "a", "A":
		value, err := app.prompt(reader, output, "Selector: ", "")
		if err != nil {
			return err
		}
		selector, err := mode.ParseSelector(value)
		if err != nil {
			return err
		}
		if err := app.catalog.Validate(selector); err != nil {
			return err
		}
		return app.state.Apply(selector.CanonicalString(), key == "a")
	}
	return nil
}

func (app *application) save() error {
	modes := make(map[string]mode.Definition, len(app.state.Modes))
	for name, definition := range app.state.Modes {
		modes[name] = definition
	}
	if err := manifest.ReplaceModes(app.root, modes); err != nil {
		return err
	}
	app.state.Dirty = false
	app.message = "Saved."
	return nil
}
func (app *application) syncModeCursor() {
	names := app.state.Names()
	for index, name := range names {
		if name == app.state.Selected {
			app.modeCursor = index
			return
		}
	}
}

func (app *application) render(output io.Writer) {
	fmt.Fprint(output, "\x1b[H\x1b[2Jagentpack mode editor")
	if app.state.Dirty {
		fmt.Fprint(output, "  [unsaved]")
	}
	fmt.Fprintln(output, "\r")
	fmt.Fprintln(output, "\rModes                         Capabilities\r")
	names, rows := app.state.Names(), Flatten(app.tree, app.expanded)
	lines := len(names)
	if len(rows) > lines {
		lines = len(rows)
	}
	if lines > 24 {
		lines = 24
	}
	for index := 0; index < lines; index++ {
		left := ""
		if index < len(names) {
			marker := "  "
			if app.focus == modesFocus && index == app.modeCursor {
				marker = "> "
			}
			readOnly := ""
			if names[index] == mode.DefaultName {
				readOnly = " (read-only)"
			}
			left = marker + names[index] + readOnly
		}
		right := ""
		if index < len(rows) {
			row := rows[index]
			marker := "  "
			if app.focus == treeFocus && index == app.treeCursor {
				marker = "> "
			}
			fold := "  "
			if len(row.Node.Children) != 0 {
				if app.expanded[row.Node.ID] {
					fold = "▾ "
				} else {
					fold = "▸ "
				}
			}
			glyph := " "
			if row.Node.Selector != nil {
				switch app.state.SelectorState(row.Node.Selector.CanonicalString()) {
				case ExplicitEnable:
					glyph = "+"
				case ExplicitDisable:
					glyph = "-"
				default:
					glyph = "·"
				}
			}
			right = marker + strings.Repeat("  ", row.Depth) + fold + glyph + " " + row.Node.Label
			if row.Node.Subtitle != "" {
				right += "  " + row.Node.Subtitle
			}
		}
		fmt.Fprintf(output, "%-30s %s\r\n", left, right)
	}
	fmt.Fprintln(output, "\rTab focus · arrows/jk move · enter cycle · e/x/c set · E/X subtree · n/r/d/b modes · s save · q quit\r")
	if app.message != "" {
		fmt.Fprintln(output, app.message+"\r")
	}
}

func (app *application) prompt(reader *bufio.Reader, output io.Writer, label, initial string) (string, error) {
	buffer := []rune(initial)
	for {
		fmt.Fprintf(output, "\x1b[999;1H\x1b[2K%s%s", label, string(buffer))
		key, err := readKey(reader)
		if err != nil {
			return "", err
		}
		switch key {
		case "enter":
			return strings.TrimSpace(string(buffer)), nil
		case "esc":
			return "", nil
		case "backspace":
			if len(buffer) != 0 {
				buffer = buffer[:len(buffer)-1]
			}
		default:
			if len([]rune(key)) == 1 {
				buffer = append(buffer, []rune(key)[0])
			}
		}
	}
}

func readKey(reader *bufio.Reader) (string, error) {
	value, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	switch value {
	case 3:
		return "ctrl-c", nil
	case '\t':
		return "tab", nil
	case '\r', '\n':
		return "enter", nil
	case 127, 8:
		return "backspace", nil
	case 27:
		if reader.Buffered() == 0 {
			return "esc", nil
		}
		second, _ := reader.ReadByte()
		if second != '[' {
			return "esc", nil
		}
		third, _ := reader.ReadByte()
		return map[byte]string{'A': "up", 'B': "down", 'C': "right", 'D': "left", '3': "delete"}[third], nil
	default:
		return string(value), nil
	}
}
