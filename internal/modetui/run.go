package modetui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/OlegHQ/agentpack/internal/manifest"
	"github.com/OlegHQ/agentpack/internal/mode"
	"github.com/OlegHQ/agentpack/internal/modecatalog"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

type focus uint8

const (
	modesFocus focus = iota
	treeFocus
)

type promptKind uint8

const (
	noPrompt promptKind = iota
	createPrompt
	renamePrompt
	selectorEnablePrompt
	selectorDisablePrompt
	deletePrompt
	quitPrompt
)

type messageKind uint8

const (
	infoMessage messageKind = iota
	errorMessage
)

type application struct {
	root                   string
	state                  State
	catalog                modecatalog.CapabilityCatalog
	tree                   []Node
	focus                  focus
	modeCursor, treeCursor int
	modeOffset, treeOffset int
	expanded               map[string]bool
	message                string
	messageKind            messageKind
	prompt                 promptKind
	input                  string
	showHelp               bool
	width, height          int
}

type palette struct {
	accent, dim, warn, enabled, disabled, neutral lipgloss.AdaptiveColor
	success, failure, focused, unfocused          lipgloss.AdaptiveColor
}

var colors = newPalette()

func newPalette() palette {
	color := func(light, dark string) lipgloss.AdaptiveColor {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENTPACK_TUI_THEME"))) {
		case "light":
			return lipgloss.AdaptiveColor{Light: light, Dark: light}
		case "dark":
			return lipgloss.AdaptiveColor{Light: dark, Dark: dark}
		default:
			return lipgloss.AdaptiveColor{Light: light, Dark: dark}
		}
	}
	return palette{
		accent: color("#005FAF", "#5FD7FF"), dim: color("#666666", "#767676"),
		warn: color("#9A5D00", "#FFD75F"), enabled: color("#007A00", "#5FD75F"),
		disabled: color("#AF0000", "#FF5F5F"), neutral: color("#777777", "#808080"),
		success: color("#007A00", "#87FF87"), failure: color("#AF0000", "#FF8787"),
		focused: color("#005FAF", "#5FD7FF"), unfocused: color("#AAAAAA", "#4A4A4A"),
	}
}

func Run(root string, project *manifest.Manifest, catalog modecatalog.CapabilityCatalog, selected string) error {
	state, err := LoadState(project, selected)
	if err != nil {
		return err
	}
	app := newApplication(root, state, catalog)
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("mode: TUI requires an interactive terminal")
	}
	_, err = tea.NewProgram(app, tea.WithAltScreen()).Run()
	return err
}

func newApplication(root string, state State, catalog modecatalog.CapabilityCatalog) *application {
	app := &application{
		root: root, state: state, catalog: catalog, tree: BuildTree(catalog),
		expanded: make(map[string]bool), width: 120, height: 32,
	}
	for _, node := range app.tree {
		app.expanded[node.ID] = true
	}
	app.syncModeCursor()
	return app
}

func (app *application) Init() tea.Cmd { return nil }

func (app *application) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch value := message.(type) {
	case tea.WindowSizeMsg:
		app.width, app.height = value.Width, value.Height
		app.clampCursors()
	case tea.KeyMsg:
		if app.prompt != noPrompt {
			return app.updatePrompt(value)
		}
		app.message = ""
		key := value.String()
		switch key {
		case "ctrl+c", "q":
			if app.state.Dirty {
				app.openPrompt(quitPrompt, "")
				return app, nil
			}
			return app, tea.Quit
		case "?", "f1":
			app.showHelp = !app.showHelp
			return app, nil
		case "esc":
			if app.showHelp {
				app.showHelp = false
			}
			return app, nil
		case "tab", "shift+tab":
			if app.focus == modesFocus {
				app.focus = treeFocus
			} else {
				app.focus = modesFocus
			}
		case "s", "ctrl+s":
			app.save()
		default:
			if app.showHelp {
				return app, nil
			}
			if app.focus == modesFocus {
				app.updateModes(key)
			} else {
				app.updateTree(key)
			}
		}
		app.clampCursors()
	}
	return app, nil
}

func (app *application) updateModes(key string) {
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
	case "enter", "right", "l":
		app.focus = treeFocus
	case "n":
		app.openPrompt(createPrompt, "")
	case "r":
		if app.state.ReadOnly() {
			app.setError("default is reserved and cannot be renamed")
		} else {
			app.openPrompt(renamePrompt, app.state.Selected)
		}
	case "d", "delete":
		if app.state.ReadOnly() {
			app.setError("default is reserved and cannot be deleted")
		} else {
			app.openPrompt(deletePrompt, "")
		}
	case "b":
		base := mode.BaseAll
		if app.state.Definition().Base == mode.BaseAll {
			base = mode.BaseNone
		}
		if err := app.state.SetBase(base); err != nil {
			app.setError(err.Error())
		} else {
			app.setInfo("base = " + string(base))
		}
	}
}

func (app *application) updateTree(key string) {
	rows := app.visibleRows()
	if len(rows) == 0 {
		return
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
		if app.expanded[row.Node.ID] && len(row.Node.Children) > 0 {
			delete(app.expanded, row.Node.ID)
		} else {
			app.focus = modesFocus
		}
	case "right", "l":
		if len(row.Node.Children) > 0 {
			app.expanded[row.Node.ID] = true
		}
	case "enter", " ":
		if row.Node.Selector == nil {
			app.expanded[row.Node.ID] = !app.expanded[row.Node.ID]
			return
		}
		canonical := row.Node.Selector.CanonicalString()
		var err error
		switch app.state.SelectorState(canonical) {
		case Neutral:
			err = app.state.Apply(canonical, true)
		case ExplicitEnable:
			err = app.state.Apply(canonical, false)
		case ExplicitDisable:
			err = app.state.Clear(canonical)
		}
		app.capture(err)
	case "e", "x", "c":
		if row.Node.Selector == nil {
			return
		}
		canonical := row.Node.Selector.CanonicalString()
		if key == "c" {
			app.capture(app.state.Clear(canonical))
		} else {
			app.capture(app.state.Apply(canonical, key == "e"))
		}
	case "E", "X":
		selectors := CollectSelectors(row.Node)
		if len(selectors) == 0 {
			return
		}
		for _, selector := range selectors[1:] {
			_ = app.state.Clear(selector)
		}
		app.capture(app.state.Apply(selectors[0], key == "E"))
	case "a":
		app.openPrompt(selectorEnablePrompt, "")
	case "A":
		app.openPrompt(selectorDisablePrompt, "")
	}
}

func (app *application) updatePrompt(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	name := key.String()
	if name == "ctrl+c" {
		app.prompt = noPrompt
		return app, nil
	}
	if app.prompt == deletePrompt || app.prompt == quitPrompt {
		switch strings.ToLower(name) {
		case "esc", "n":
			app.prompt = noPrompt
		case "y":
			kind := app.prompt
			app.prompt = noPrompt
			if kind == deletePrompt {
				app.capture(app.state.Delete())
				app.syncModeCursor()
				return app, nil
			}
			app.save()
			if app.state.Dirty {
				return app, nil
			}
			return app, tea.Quit
		case "d":
			if app.prompt == quitPrompt {
				app.prompt = noPrompt
				return app, tea.Quit
			}
		}
		return app, nil
	}
	switch name {
	case "esc":
		app.prompt, app.input = noPrompt, ""
	case "enter":
		value, kind := strings.TrimSpace(app.input), app.prompt
		if value == "" {
			app.setError("a value is required")
			return app, nil
		}
		var err error
		switch kind {
		case createPrompt:
			err = app.state.Create(value)
		case renamePrompt:
			err = app.state.Rename(value)
		case selectorEnablePrompt, selectorDisablePrompt:
			var selector mode.Selector
			selector, err = mode.ParseSelector(value)
			if err == nil {
				err = app.catalog.Validate(selector)
			}
			if err == nil {
				err = app.state.Apply(selector.CanonicalString(), kind == selectorEnablePrompt)
			}
		}
		if err != nil {
			app.setError(err.Error())
			return app, nil
		}
		app.prompt, app.input = noPrompt, ""
		app.syncModeCursor()
	case "backspace":
		if app.input != "" {
			_, size := utf8.DecodeLastRuneInString(app.input)
			app.input = app.input[:len(app.input)-size]
		}
	default:
		if key.Type == tea.KeyRunes {
			app.input += string(key.Runes)
		}
	}
	return app, nil
}

func (app *application) View() string {
	width, height := max(app.width, 56), max(app.height, 16)
	header := app.renderHeader(width)
	footer := app.renderFooter(width)
	bodyHeight := max(7, height-lipgloss.Height(header)-lipgloss.Height(footer))
	body := app.renderBody(width, bodyHeight)
	view := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
	if app.showHelp {
		return app.renderHelp(width, height)
	}
	if app.prompt != noPrompt {
		return app.renderPrompt(width, height)
	}
	return view
}

func (app *application) renderHeader(width int) string {
	brand := lipgloss.NewStyle().Bold(true).Foreground(colors.accent).Render(" agentpack ")
	title := lipgloss.NewStyle().Bold(true).Render("MODE EDITOR")
	project := lipgloss.NewStyle().Foreground(colors.dim).Render("project  " + filepath.Base(app.root))
	dirty := ""
	if app.state.Dirty {
		dirty = lipgloss.NewStyle().Bold(true).Foreground(colors.warn).Render("  ● unsaved")
	}
	left := brand + " " + title + dirty
	space := max(1, width-lipgloss.Width(left)-lipgloss.Width(project))
	return left + strings.Repeat(" ", space) + project + "\n"
}

func (app *application) renderBody(width, height int) string {
	if width < 92 {
		leftWidth := max(20, width/3)
		rightWidth := width - leftWidth
		return lipgloss.JoinHorizontal(lipgloss.Top,
			app.renderModes(leftWidth, height), app.renderTree(rightWidth, height))
	}
	leftWidth, detailWidth := max(22, width*22/100), max(28, width*30/100)
	treeWidth := width - leftWidth - detailWidth
	return lipgloss.JoinHorizontal(lipgloss.Top,
		app.renderModes(leftWidth, height), app.renderTree(treeWidth, height), app.renderDetails(detailWidth, height))
}

func panel(width, height int, title, content string, focused bool) string {
	border := colors.unfocused
	if focused {
		border = colors.focused
		title = lipgloss.NewStyle().Bold(true).Foreground(colors.accent).Render(title)
	} else {
		title = lipgloss.NewStyle().Foreground(colors.dim).Render(title)
	}
	return lipgloss.NewStyle().Width(max(1, width-2)).Height(max(1, height-2)).
		Border(lipgloss.RoundedBorder()).BorderForeground(border).Render(" " + title + " \n" + content)
}

func (app *application) renderModes(width, height int) string {
	names := app.state.Names()
	visible := max(1, height-4)
	app.modeOffset = scrollOffset(app.modeCursor, app.modeOffset, visible, len(names))
	lines := make([]string, 0, visible)
	for index := app.modeOffset; index < len(names) && len(lines) < visible; index++ {
		name := names[index]
		prefix := "  "
		if name == mode.DefaultName {
			prefix = lipgloss.NewStyle().Foreground(colors.warn).Render("● ")
		}
		line := prefix + name
		if name == mode.DefaultName && width > 25 {
			line += lipgloss.NewStyle().Foreground(colors.dim).Render("  read-only")
		}
		if index == app.modeCursor {
			marker := "  "
			style := lipgloss.NewStyle()
			if app.focus == modesFocus {
				marker, style = "› ", style.Bold(true).Foreground(colors.accent)
			}
			line = style.Render(marker + line)
		} else {
			line = "  " + line
		}
		lines = append(lines, truncate(line, width-4))
	}
	return panel(width, height, "Modes", strings.Join(lines, "\n"), app.focus == modesFocus)
}

func (app *application) renderTree(width, height int) string {
	rows := app.visibleRows()
	visible := max(1, height-4)
	app.treeOffset = scrollOffset(app.treeCursor, app.treeOffset, visible, len(rows))
	lines := make([]string, 0, visible)
	for index := app.treeOffset; index < len(rows) && len(lines) < visible; index++ {
		row := rows[index]
		fold := "• "
		if len(row.Node.Children) > 0 {
			fold = "▸ "
			if app.expanded[row.Node.ID] {
				fold = "▾ "
			}
		}
		glyph, glyphStyle := app.selectorGlyph(row.Node.Selector)
		label := row.Node.Label
		if row.Node.Selector == nil {
			label = lipgloss.NewStyle().Bold(true).Render(label)
		}
		if row.Node.Subtitle != "" {
			label += lipgloss.NewStyle().Foreground(colors.dim).Render("  " + row.Node.Subtitle)
		}
		line := strings.Repeat("  ", row.Depth) + fold + glyphStyle.Render(glyph) + " " + label
		if index == app.treeCursor {
			marker := "  "
			style := lipgloss.NewStyle()
			if app.focus == treeFocus {
				marker, style = "› ", style.Bold(true).Foreground(colors.accent)
			}
			line = style.Render(marker + line)
		} else {
			line = "  " + line
		}
		lines = append(lines, truncate(line, width-4))
	}
	if len(rows) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(colors.dim).Render("  No capabilities discovered"))
	}
	return panel(width, height, "Capability tree", strings.Join(lines, "\n"), app.focus == treeFocus)
}

func (app *application) renderDetails(width, height int) string {
	definition := app.state.Definition()
	baseStyle := lipgloss.NewStyle().Bold(true).Foreground(colors.enabled)
	if definition.Base == mode.BaseNone {
		baseStyle = baseStyle.Foreground(colors.disabled)
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render("Mode") + "  " + app.state.Selected,
		lipgloss.NewStyle().Bold(true).Render("Base") + "  " + baseStyle.Render(string(definition.Base)),
		"",
	}
	rows := app.visibleRows()
	if len(rows) > 0 {
		row := rows[min(app.treeCursor, len(rows)-1)]
		lines = append(lines, lipgloss.NewStyle().Bold(true).Render("Cursor"), "  "+row.Node.Label)
		if row.Node.Selector != nil {
			lines = append(lines, wrapIndent(row.Node.Selector.CanonicalString(), max(10, width-6), "  ")...)
		}
		lines = append(lines, "")
	}
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Enabled (%d)", len(definition.Enable))))
	lines = append(lines, selectorSummary(definition.Enable, "+", width)...)
	lines = append(lines, "", lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Disabled (%d)", len(definition.Disable))))
	lines = append(lines, selectorSummary(definition.Disable, "−", width)...)
	return panel(width, height, "Details", strings.Join(lines, "\n"), false)
}

func (app *application) renderFooter(width int) string {
	message := ""
	if app.message != "" {
		color := colors.success
		if app.messageKind == errorMessage {
			color = colors.failure
		}
		message = lipgloss.NewStyle().Foreground(color).Render(app.message)
	}
	hints := "↑↓ move  Tab focus  s save  ? help  q quit"
	if app.focus == modesFocus {
		hints = "↑↓ select  n new  r rename  d delete  b base  Tab capabilities  s save  ? help"
	} else {
		hints = "↑↓ move  ←→ fold  Space cycle  e enable  x disable  E/X subtree  Tab modes  s save  ? help"
	}
	return lipgloss.NewStyle().Width(width).Border(lipgloss.NormalBorder(), true, false, false, false).
		BorderForeground(colors.unfocused).Foreground(colors.dim).Render(message + "\n" + truncate(hints, width))
}

func (app *application) renderHelp(width, height int) string {
	body := []string{
		lipgloss.NewStyle().Bold(true).Foreground(colors.accent).Render("Keyboard shortcuts"), "",
		"Global", "  Tab / Shift+Tab   switch panes", "  s / Ctrl+S        save", "  q / Ctrl+C        quit", "  ? / F1            close help", "",
		"Modes", "  ↑↓ / j k          select", "  n / r / d         new, rename, delete", "  b                 toggle base all/none", "",
		"Capabilities", "  ←→ / h l          collapse or expand", "  Space / Enter     enable → disable → inherit", "  e / x / c         enable, disable, clear", "  E / X             enable or disable subtree", "  a / A             add selector",
		"", "[+] explicit enable   [-] explicit disable   [✓]/[✗] inherited",
	}
	return centeredBox(width, height, "Help", strings.Join(body, "\n"), min(76, width-4))
}

func (app *application) renderPrompt(width, height int) string {
	title, instruction, hint := "Input", "Enter a value.", "Enter confirm · Esc cancel"
	switch app.prompt {
	case createPrompt:
		title, instruction = "Create mode", "Name the new mode."
	case renamePrompt:
		title, instruction = "Rename mode", "Enter the new mode name."
	case selectorEnablePrompt, selectorDisablePrompt:
		title, instruction = "Add selector", "package:, package-path:, mcp:, or .agents:"
	case deletePrompt:
		title, instruction, hint = "Delete mode", fmt.Sprintf("Delete mode %q?", app.state.Selected), "y confirm · n/Esc cancel"
	case quitPrompt:
		title, instruction, hint = "Unsaved changes", "Save changes before quitting?", "y save · d discard · Esc cancel"
	}
	body := instruction
	if app.prompt != deletePrompt && app.prompt != quitPrompt {
		body += "\n\n" + lipgloss.NewStyle().Foreground(colors.accent).Render("› "+app.input+"█")
	}
	if app.message != "" {
		body += "\n\n" + lipgloss.NewStyle().Foreground(colors.failure).Render(app.message)
	}
	body += "\n\n" + lipgloss.NewStyle().Foreground(colors.dim).Render(hint)
	return centeredBox(width, height, title, body, min(68, width-4))
}

func centeredBox(width, height int, title, body string, boxWidth int) string {
	box := lipgloss.NewStyle().Width(max(30, boxWidth-4)).Padding(1, 2).
		Border(lipgloss.RoundedBorder()).BorderForeground(colors.focused).
		Render(lipgloss.NewStyle().Bold(true).Foreground(colors.accent).Render(title) + "\n\n" + body)
	boxHeight := lipgloss.Height(box)
	return strings.Repeat("\n", max(0, (height-boxHeight)/2)) + lipgloss.PlaceHorizontal(width, lipgloss.Center, box)
}

func (app *application) selectorGlyph(selector *mode.Selector) (string, lipgloss.Style) {
	if selector == nil {
		return "[ ]", lipgloss.NewStyle().Foreground(colors.neutral)
	}
	allowed := app.selectorAllowed(*selector)
	style := lipgloss.NewStyle().Bold(true)
	switch app.state.SelectorState(selector.CanonicalString()) {
	case ExplicitEnable:
		return "[+]", style.Foreground(colors.enabled)
	case ExplicitDisable:
		return "[-]", style.Foreground(colors.disabled)
	case Neutral:
		if allowed {
			return "[✓]", style.Foreground(colors.enabled)
		}
		return "[✗]", style.Foreground(colors.disabled)
	default:
		return "[ ]", style.Foreground(colors.neutral)
	}
}

func (app *application) selectorAllowed(selector mode.Selector) bool {
	effective, err := mode.NewEffective(app.state.Selected, app.state.Definition(), nil)
	if err != nil {
		return false
	}
	switch selector.Kind {
	case mode.SelectorPackage:
		allowed, _ := effective.AllowsPackagePath(selector.Module, "")
		return allowed
	case mode.SelectorPackagePath:
		allowed, _ := effective.AllowsPackagePath(selector.Module, selector.RelativePath)
		return allowed
	case mode.SelectorMCP:
		return effective.AllowsMCP(selector.MCPName)
	case mode.SelectorDotAgents:
		allowed, _ := effective.AllowsDotAgentsPath(selector.RelativePath)
		return allowed
	default:
		return false
	}
}

func (app *application) visibleRows() []Row {
	rows := Flatten(app.tree, app.expanded)
	if len(rows) == 0 {
		app.treeCursor = 0
	} else if app.treeCursor >= len(rows) {
		app.treeCursor = len(rows) - 1
	}
	return rows
}

func (app *application) save() {
	modes := make(map[string]mode.Definition, len(app.state.Modes))
	for name, definition := range app.state.Modes {
		modes[name] = definition
	}
	if err := manifest.ReplaceModes(app.root, modes); err != nil {
		app.setError("Save failed: " + err.Error())
		return
	}
	app.state.Dirty = false
	app.setInfo("Saved " + filepath.Join(app.root, "agentpack.toml"))
}

func (app *application) syncModeCursor() {
	for index, name := range app.state.Names() {
		if name == app.state.Selected {
			app.modeCursor = index
			return
		}
	}
}

func (app *application) clampCursors() {
	app.modeCursor = min(max(0, app.modeCursor), max(0, len(app.state.Names())-1))
	rows := app.visibleRows()
	app.treeCursor = min(max(0, app.treeCursor), max(0, len(rows)-1))
}

func (app *application) openPrompt(kind promptKind, initial string) {
	app.prompt, app.input, app.message = kind, initial, ""
}
func (app *application) capture(err error) {
	if err != nil {
		app.setError(err.Error())
	}
}
func (app *application) setInfo(message string) {
	app.message, app.messageKind = message, infoMessage
}
func (app *application) setError(message string) {
	app.message, app.messageKind = message, errorMessage
}

func scrollOffset(cursor, offset, visible, total int) int {
	if total <= visible {
		return 0
	}
	if cursor < offset {
		return cursor
	}
	if cursor >= offset+visible {
		return cursor - visible + 1
	}
	return min(offset, total-visible)
}

func selectorSummary(values []string, glyph string, width int) []string {
	if len(values) == 0 {
		return []string{lipgloss.NewStyle().Foreground(colors.dim).Render("  (none)")}
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, truncate("  "+glyph+" "+value, width-4))
	}
	return result
}

func wrapIndent(value string, width int, indent string) []string {
	if width <= len(indent) {
		return []string{indent + value}
	}
	var lines []string
	for value != "" {
		limit := min(len(value), width-len(indent))
		lines = append(lines, indent+value[:limit])
		value = value[limit:]
	}
	return lines
}

func truncate(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(value)
}
