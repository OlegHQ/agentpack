//! Rendering: draws the title/body/footer panes, the capability tree rows, and modal overlays.

use std::path::Path;

use ratatui::layout::{Alignment, Constraint, Direction, Layout, Rect};
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, List, ListItem, ListState, Paragraph, Wrap};

use crate::mode::filter::EffectiveMode;
use crate::mode::selectors::Selector;
use crate::mode::{is_reserved_mode, ModeBase};

use super::app::{Focus, MessageKind, Prompt, TuiApp, VisibleRow};
use super::state::{ModeEditorState, SelectorState};
use super::tree::TreeNode;

pub fn render(frame: &mut ratatui::Frame, app: &TuiApp) {
    let area = frame.area();
    let outer = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(1),
            Constraint::Min(3),
            Constraint::Length(3),
        ])
        .split(area);

    render_title(frame, outer[0], app);
    render_body(frame, outer[1], app);
    render_footer(frame, outer[2], app);

    if let Some(prompt) = &app.prompt {
        render_prompt(frame, area, prompt);
    } else if app.show_help {
        render_help(frame, area);
    }
}

fn render_title(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let dirty = if app.state.dirty { " *modified*" } else { "" };
    let title = format!(
        " agentpack mode tui  —  project: {}{}",
        app.project_root.display(),
        dirty
    );
    frame.render_widget(
        Paragraph::new(title).style(Style::default().add_modifier(Modifier::BOLD)),
        area,
    );
}

fn render_body(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let columns = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([
            Constraint::Percentage(22),
            Constraint::Percentage(48),
            Constraint::Percentage(30),
        ])
        .split(area);

    render_modes(frame, columns[0], app);
    render_tree(frame, columns[1], app);
    render_details(frame, columns[2], app);
}

fn render_modes(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let names = app.mode_names();
    let items: Vec<ListItem> = names
        .iter()
        .map(|name| {
            let mut spans = Vec::new();
            if is_reserved_mode(name) {
                spans.push(Span::styled("● ", Style::default().fg(Color::Yellow)));
            } else {
                spans.push(Span::raw("  "));
            }
            spans.push(Span::raw(name.clone()));
            if is_reserved_mode(name) {
                spans.push(Span::styled(
                    "  (read-only)",
                    Style::default().fg(Color::DarkGray),
                ));
            }
            ListItem::new(Line::from(spans))
        })
        .collect();
    let mut list_state = ListState::default();
    list_state.select(Some(app.modes_cursor));
    let block = Block::default()
        .borders(Borders::ALL)
        .title(focus_title(" Modes ", app.focus == Focus::Modes));
    let list = List::new(items)
        .block(block)
        .highlight_style(selection_style(app.focus == Focus::Modes))
        .highlight_symbol(" ▶ ");
    frame.render_stateful_widget(list, area, &mut list_state);
}

fn render_tree(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let visible = app.flatten_tree();
    let effective = app.effective_mode();
    let items: Vec<ListItem> = visible
        .iter()
        .map(|row| ListItem::new(tree_row_line(row, &app.state, &effective)))
        .collect();

    let mut list_state = ListState::default();
    if !visible.is_empty() {
        list_state.select(Some(app.tree_cursor.min(visible.len() - 1)));
    }

    let block = Block::default()
        .borders(Borders::ALL)
        .title(focus_title(" Capability tree ", app.focus == Focus::Tree));
    let list = List::new(items)
        .block(block)
        .highlight_style(selection_style(app.focus == Focus::Tree))
        .highlight_symbol(" ▶ ");
    frame.render_stateful_widget(list, area, &mut list_state);
}

/// Selection background that stays dark enough for the underlying span colors
/// (yellow reserved-mode bullet, default text, dimmed subtitles) to remain
/// readable. Avoids ratatui's bright `Color::Blue`, which renders close to a
/// light cyan on many terminals and washes out the highlighted row.
fn selection_style(focused: bool) -> Style {
    let bg = if focused {
        Color::Rgb(38, 70, 120)
    } else {
        Color::Rgb(48, 48, 48)
    };
    Style::default().bg(bg).add_modifier(Modifier::BOLD)
}

fn tree_row_line<'a>(
    row: &VisibleRow<'a>,
    state: &ModeEditorState,
    effective: &EffectiveMode,
) -> Line<'a> {
    let indent = "  ".repeat(row.depth);
    let caret = if row.expandable {
        if row.expanded {
            "▼ "
        } else {
            "▶ "
        }
    } else {
        "• "
    };
    let (glyph, glyph_style) = row_glyph(row.node, state, effective);
    let label_style = if row.node.selector.is_none() {
        Style::default().add_modifier(Modifier::BOLD)
    } else {
        Style::default()
    };
    let mut spans = vec![
        Span::raw(indent),
        Span::raw(caret),
        Span::styled(glyph, glyph_style),
        Span::raw(" "),
        Span::styled(row.node.label.clone(), label_style),
    ];
    if let Some(subtitle) = &row.node.subtitle {
        spans.push(Span::styled(
            format!("  {subtitle}"),
            Style::default().fg(Color::DarkGray),
        ));
    }
    Line::from(spans)
}

fn row_glyph(
    node: &TreeNode,
    state: &ModeEditorState,
    effective: &EffectiveMode,
) -> (&'static str, Style) {
    let Some(selector) = &node.selector else {
        return ("[ ]", Style::default().fg(Color::DarkGray));
    };
    let canonical = selector.canonical_string();
    let explicit = state.selector_state(&canonical);
    let allowed = selector_is_allowed(effective, selector);
    let base_color = match allowed {
        Some(true) => Color::Green,
        Some(false) => Color::Red,
        None => Color::DarkGray,
    };
    let glyph = match (explicit, allowed) {
        (SelectorState::ExplicitEnable, _) => "[+]",
        (SelectorState::ExplicitDisable, _) => "[-]",
        (SelectorState::Neutral, Some(true)) => "[✓]",
        (SelectorState::Neutral, Some(false)) => "[✗]",
        (SelectorState::Neutral, None) => "[ ]",
    };
    (
        glyph,
        Style::default().fg(base_color).add_modifier(Modifier::BOLD),
    )
}

fn selector_is_allowed(effective: &EffectiveMode, selector: &Selector) -> Option<bool> {
    match selector {
        Selector::Package { module } => effective.allows_package_path(module, Path::new("")).ok(),
        Selector::PackagePath { module, rel_path } => effective
            .allows_package_path(module, Path::new(rel_path))
            .ok(),
        Selector::Mcp { name } => Some(effective.allows_mcp(name)),
        Selector::DotAgents { rel_path } => {
            effective.allows_dot_agents_path(Path::new(rel_path)).ok()
        }
    }
}

fn render_details(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let mut lines: Vec<Line> = Vec::new();
    let definition = app.state.selected_definition();
    let mut mode_spans = vec![
        Span::styled("Mode: ", Style::default().add_modifier(Modifier::BOLD)),
        Span::raw(app.state.selected_mode.clone()),
    ];
    if app.state.selected_is_read_only() {
        mode_spans.push(Span::styled(
            "  (read-only)",
            Style::default().fg(Color::DarkGray),
        ));
    }
    lines.push(Line::from(mode_spans));
    lines.push(Line::from(vec![
        Span::styled("Base: ", Style::default().add_modifier(Modifier::BOLD)),
        Span::styled(
            definition.base.to_string(),
            Style::default().fg(match definition.base {
                ModeBase::All => Color::Green,
                ModeBase::None => Color::Red,
            }),
        ),
    ]));
    lines.push(Line::from(""));

    let visible = app.flatten_tree();
    if let Some(row) = visible.get(app.tree_cursor) {
        lines.push(Line::from(vec![Span::styled(
            "Cursor",
            Style::default().add_modifier(Modifier::BOLD),
        )]));
        lines.push(Line::from(format!("label: {}", row.node.label)));
        if let Some(selector) = &row.node.selector {
            lines.push(Line::from(format!(
                "selector: {}",
                selector.canonical_string()
            )));
        } else {
            lines.push(Line::from("selector: (section header)"));
        }
        lines.push(Line::from(""));
    }

    lines.push(Line::from(vec![Span::styled(
        format!("Enable ({})", definition.enable.len()),
        Style::default().add_modifier(Modifier::BOLD),
    )]));
    if definition.enable.is_empty() {
        lines.push(Line::from(Span::styled(
            "  (none)",
            Style::default().fg(Color::DarkGray),
        )));
    } else {
        for entry in &definition.enable {
            lines.push(Line::from(format!("  + {entry}")));
        }
    }
    lines.push(Line::from(""));
    lines.push(Line::from(vec![Span::styled(
        format!("Disable ({})", definition.disable.len()),
        Style::default().add_modifier(Modifier::BOLD),
    )]));
    if definition.disable.is_empty() {
        lines.push(Line::from(Span::styled(
            "  (none)",
            Style::default().fg(Color::DarkGray),
        )));
    } else {
        for entry in &definition.disable {
            lines.push(Line::from(format!("  - {entry}")));
        }
    }

    let paragraph = Paragraph::new(lines)
        .wrap(Wrap { trim: false })
        .block(Block::default().borders(Borders::ALL).title(" Details "));
    frame.render_widget(paragraph, area);
}

fn render_footer(frame: &mut ratatui::Frame, area: Rect, app: &TuiApp) {
    let layout = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(1), Constraint::Length(2)])
        .split(area);

    let message = match &app.message {
        Some((text, MessageKind::Info)) => Line::from(Span::styled(
            text.clone(),
            Style::default().fg(Color::LightGreen),
        )),
        Some((text, MessageKind::Error)) => Line::from(Span::styled(
            text.clone(),
            Style::default().fg(Color::LightRed),
        )),
        None => Line::from(""),
    };
    frame.render_widget(Paragraph::new(message), layout[0]);

    let hints = match app.focus {
        Focus::Modes => {
            "[↑/↓] select  [n]ew  [r]ename  [d]elete  [b]ase all/none  [Tab] tree  [s]ave  [?] help  [q]uit"
        }
        Focus::Tree => {
            "[↑/↓] move  [←/→] fold  [t] cycle  [e] enable  [x] disable  [c] clear  [E/X] subtree  [a/A] add  [Tab] modes  [s]ave  [q]uit"
        }
    };
    frame.render_widget(
        Paragraph::new(Line::from(Span::styled(
            hints,
            Style::default().fg(Color::Gray),
        )))
        .block(Block::default().borders(Borders::TOP)),
        layout[1],
    );
}

fn focus_title(text: &str, focused: bool) -> Span<'_> {
    if focused {
        Span::styled(
            text,
            Style::default()
                .fg(Color::LightCyan)
                .add_modifier(Modifier::BOLD),
        )
    } else {
        Span::styled(text, Style::default().fg(Color::Gray))
    }
}

fn render_prompt(frame: &mut ratatui::Frame, area: Rect, prompt: &Prompt) {
    let modal = centered_rect(60, 20, area);
    frame.render_widget(ratatui::widgets::Clear, modal);
    let (title, body) = match prompt {
        Prompt::CreateMode { buffer } => (
            " Create mode ",
            vec![
                Line::from("Enter a name for the new mode."),
                Line::from(""),
                Line::from(vec![
                    Span::raw("> "),
                    Span::styled(buffer.clone(), Style::default().fg(Color::LightCyan)),
                    Span::styled("_", Style::default().add_modifier(Modifier::SLOW_BLINK)),
                ]),
                Line::from(""),
                Line::from(Span::styled(
                    "[Enter] confirm  [Esc] cancel",
                    Style::default().fg(Color::Gray),
                )),
            ],
        ),
        Prompt::RenameMode { buffer } => (
            " Rename mode ",
            vec![
                Line::from("Enter the new mode name."),
                Line::from(""),
                Line::from(vec![
                    Span::raw("> "),
                    Span::styled(buffer.clone(), Style::default().fg(Color::LightCyan)),
                    Span::styled("_", Style::default().add_modifier(Modifier::SLOW_BLINK)),
                ]),
                Line::from(""),
                Line::from(Span::styled(
                    "[Enter] confirm  [Esc] cancel",
                    Style::default().fg(Color::Gray),
                )),
            ],
        ),
        Prompt::AddSelector { buffer, enable } => (
            if *enable {
                " Enable selector "
            } else {
                " Disable selector "
            },
            vec![
                Line::from(
                    "Type a selector: package:<mod> | package-path:<mod>:<rel> | mcp:<name> | .agents:<rel>",
                ),
                Line::from(""),
                Line::from(vec![
                    Span::raw("> "),
                    Span::styled(buffer.clone(), Style::default().fg(Color::LightCyan)),
                    Span::styled("_", Style::default().add_modifier(Modifier::SLOW_BLINK)),
                ]),
                Line::from(""),
                Line::from(Span::styled(
                    "[Enter] confirm  [Esc] cancel",
                    Style::default().fg(Color::Gray),
                )),
            ],
        ),
        Prompt::ConfirmDelete { mode } => (
            " Delete mode ",
            vec![
                Line::from(format!("Delete mode \"{mode}\"? This cannot be undone.")),
                Line::from(""),
                Line::from(Span::styled(
                    "[y] confirm  [n/Esc] cancel",
                    Style::default().fg(Color::Gray),
                )),
            ],
        ),
        Prompt::ConfirmQuitDirty => (
            " Unsaved changes ",
            vec![
                Line::from("You have unsaved changes. Save before exit?"),
                Line::from(""),
                Line::from(Span::styled(
                    "[y] save & quit  [n] discard & quit  [Esc] cancel",
                    Style::default().fg(Color::Gray),
                )),
            ],
        ),
    };
    let paragraph = Paragraph::new(body)
        .wrap(Wrap { trim: false })
        .alignment(Alignment::Left)
        .block(
            Block::default().borders(Borders::ALL).title(Span::styled(
                title,
                Style::default()
                    .fg(Color::LightYellow)
                    .add_modifier(Modifier::BOLD),
            )),
        );
    frame.render_widget(paragraph, modal);
}

fn render_help(frame: &mut ratatui::Frame, area: Rect) {
    let modal = centered_rect(60, 60, area);
    frame.render_widget(ratatui::widgets::Clear, modal);
    let help = vec![
        Line::from(Span::styled(
            " agentpack mode tui ",
            Style::default()
                .fg(Color::LightYellow)
                .add_modifier(Modifier::BOLD),
        )),
        Line::from(""),
        Line::from("Global:"),
        Line::from("  Tab       switch focus between Modes and Capability tree"),
        Line::from("  s         save modes to agentpack.toml"),
        Line::from("  q, Ctrl-C quit (prompts if unsaved)"),
        Line::from("  ?, F1     toggle this help"),
        Line::from(""),
        Line::from("Modes pane:"),
        Line::from("  ↑/↓ k/j   move selection"),
        Line::from("  Enter/→   focus capability tree"),
        Line::from("  n         new mode"),
        Line::from("  r         rename (default is reserved)"),
        Line::from("  d / Del   delete (default is reserved)"),
        Line::from("  b         toggle base between all and none"),
        Line::from(""),
        Line::from("Capability tree pane:"),
        Line::from("  ↑/↓ k/j   move cursor"),
        Line::from("  ←/→ h/l   fold / unfold"),
        Line::from("  Enter/Sp  toggle fold at cursor"),
        Line::from("  t         cycle: neutral → enable → disable → neutral"),
        Line::from("  e         force-enable selector at cursor"),
        Line::from("  x         force-disable selector at cursor"),
        Line::from("  c         clear selector at cursor"),
        Line::from("  E         enable every selector under cursor (subtree)"),
        Line::from("  X         disable every selector under cursor (subtree)"),
        Line::from("  a         prompt to add an enable selector"),
        Line::from("  A         prompt to add a disable selector"),
        Line::from(""),
        Line::from(
            "Selectors: package:<mod> · package-path:<mod>:<rel> · mcp:<name> · .agents:<rel>",
        ),
    ];
    let paragraph = Paragraph::new(help)
        .wrap(Wrap { trim: false })
        .block(Block::default().borders(Borders::ALL).title(" Help "));
    frame.render_widget(paragraph, modal);
}

fn centered_rect(percent_x: u16, percent_y: u16, area: Rect) -> Rect {
    let vertical = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Percentage((100 - percent_y) / 2),
            Constraint::Percentage(percent_y),
            Constraint::Percentage((100 - percent_y) / 2),
        ])
        .split(area);
    Layout::default()
        .direction(Direction::Horizontal)
        .constraints([
            Constraint::Percentage((100 - percent_x) / 2),
            Constraint::Percentage(percent_x),
            Constraint::Percentage((100 - percent_x) / 2),
        ])
        .split(vertical[1])[1]
}
