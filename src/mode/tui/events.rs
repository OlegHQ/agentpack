//! Event handling: translates key events into reducer calls and prompt/focus transitions.

use crossterm::event::{self, Event, KeyCode, KeyModifiers};

use crate::error::Result;

use crate::mode::selectors::Selector;
use crate::mode::{is_reserved_mode, ModeBase, DEFAULT_MODE_NAME};

use super::app::{Focus, Prompt, TuiApp};
use super::state::SelectorState;
use super::tree::{collect_subtree_selectors, find_node};

pub fn handle_event(app: &mut TuiApp, event: Event) {
    let key = match event {
        Event::Key(key) if key.kind == event::KeyEventKind::Press => key,
        _ => return,
    };

    if app.prompt.is_some() {
        handle_prompt_key(app, key.code, key.modifiers);
        return;
    }

    if key.modifiers.contains(KeyModifiers::CONTROL) && matches!(key.code, KeyCode::Char('c')) {
        request_quit(app);
        return;
    }

    app.message = None;

    match key.code {
        KeyCode::Char('q') => request_quit(app),
        KeyCode::Char('s') => app.save(),
        KeyCode::Char('?') | KeyCode::F(1) => app.show_help = !app.show_help,
        KeyCode::Tab => {
            app.focus = match app.focus {
                Focus::Modes => Focus::Tree,
                Focus::Tree => Focus::Modes,
            };
        }
        _ => match app.focus {
            Focus::Modes => handle_modes_key(app, key.code),
            Focus::Tree => handle_tree_key(app, key.code),
        },
    }
}

fn request_quit(app: &mut TuiApp) {
    if app.state.dirty {
        app.prompt = Some(Prompt::ConfirmQuitDirty);
    } else {
        app.quit = true;
    }
}

fn handle_modes_key(app: &mut TuiApp, code: KeyCode) {
    let names = app.mode_names();
    match code {
        KeyCode::Up | KeyCode::Char('k') if app.modes_cursor > 0 => {
            app.modes_cursor -= 1;
            app.state.selected_mode = names[app.modes_cursor].clone();
        }
        KeyCode::Down | KeyCode::Char('j') if app.modes_cursor + 1 < names.len() => {
            app.modes_cursor += 1;
            app.state.selected_mode = names[app.modes_cursor].clone();
        }
        KeyCode::Enter | KeyCode::Right => app.focus = Focus::Tree,
        KeyCode::Char('n') => {
            app.prompt = Some(Prompt::CreateMode {
                buffer: String::new(),
            });
        }
        KeyCode::Char('r') => {
            if is_reserved_mode(&app.state.selected_mode) {
                app.error(format!(
                    "{DEFAULT_MODE_NAME} is reserved and cannot be renamed"
                ));
            } else {
                app.prompt = Some(Prompt::RenameMode {
                    buffer: app.state.selected_mode.clone(),
                });
            }
        }
        KeyCode::Char('d') | KeyCode::Delete => {
            if is_reserved_mode(&app.state.selected_mode) {
                app.error(format!(
                    "{DEFAULT_MODE_NAME} is reserved and cannot be deleted"
                ));
            } else {
                app.prompt = Some(Prompt::ConfirmDelete {
                    mode: app.state.selected_mode.clone(),
                });
            }
        }
        KeyCode::Char('b') => {
            let next = match app.state.selected_definition().base {
                ModeBase::All => ModeBase::None,
                ModeBase::None => ModeBase::All,
            };
            match app.state.set_base(next) {
                Ok(()) => app.info(format!("base = {next}")),
                Err(error) => app.error(error.to_string()),
            }
        }
        _ => {}
    }
}

fn handle_tree_key(app: &mut TuiApp, code: KeyCode) {
    let snapshot: Vec<(String, bool, bool)> = app
        .flatten_tree()
        .iter()
        .map(|row| (row.node.id.clone(), row.expandable, row.expanded))
        .collect();
    if snapshot.is_empty() {
        return;
    }
    if app.tree_cursor >= snapshot.len() {
        app.tree_cursor = snapshot.len() - 1;
    }

    match code {
        KeyCode::Up | KeyCode::Char('k') if app.tree_cursor > 0 => {
            app.tree_cursor -= 1;
        }
        KeyCode::Down | KeyCode::Char('j') if app.tree_cursor + 1 < snapshot.len() => {
            app.tree_cursor += 1;
        }
        KeyCode::Left | KeyCode::Char('h') => {
            let (id, expandable, expanded) = &snapshot[app.tree_cursor];
            if *expandable && *expanded {
                app.expanded.remove(id);
            } else {
                app.focus = Focus::Modes;
            }
        }
        KeyCode::Right | KeyCode::Char('l') => {
            let (id, expandable, expanded) = &snapshot[app.tree_cursor];
            if *expandable && !*expanded {
                app.expanded.insert(id.clone());
            }
        }
        KeyCode::Enter | KeyCode::Char(' ') => {
            // Primary action: cycle the state of a selectable row; a bare
            // section header has nothing to set, so it folds instead.
            if cursor_selector(app).is_some() {
                cycle_cursor_selector(app);
            } else {
                toggle_fold_at_cursor(app);
            }
        }
        KeyCode::Char('e') => {
            apply_cursor_selector(app, Some(true));
        }
        KeyCode::Char('x') => {
            apply_cursor_selector(app, Some(false));
        }
        KeyCode::Char('c') => {
            clear_cursor_selector(app);
        }
        KeyCode::Char('E') => {
            apply_cursor_subtree(app, true);
        }
        KeyCode::Char('X') => {
            apply_cursor_subtree(app, false);
        }
        KeyCode::Char('a') => {
            if app.state.selected_is_read_only() {
                app.error(format!("{DEFAULT_MODE_NAME} is read-only"));
            } else {
                app.prompt = Some(Prompt::AddSelector {
                    buffer: String::new(),
                    enable: true,
                });
            }
        }
        KeyCode::Char('A') => {
            if app.state.selected_is_read_only() {
                app.error(format!("{DEFAULT_MODE_NAME} is read-only"));
            } else {
                app.prompt = Some(Prompt::AddSelector {
                    buffer: String::new(),
                    enable: false,
                });
            }
        }
        _ => {}
    }
}

fn cursor_selector(app: &TuiApp) -> Option<Selector> {
    let visible = app.flatten_tree();
    visible
        .get(app.tree_cursor)
        .and_then(|row| row.node.selector.clone())
}

fn cursor_node_id(app: &TuiApp) -> Option<String> {
    let visible = app.flatten_tree();
    visible.get(app.tree_cursor).map(|row| row.node.id.clone())
}

fn toggle_fold_at_cursor(app: &mut TuiApp) {
    let Some(id) = cursor_node_id(app) else {
        return;
    };
    let expandable = find_node(&app.tree, &id).is_some_and(|node| !node.children.is_empty());
    if !expandable {
        return;
    }
    if app.expanded.contains(&id) {
        app.expanded.remove(&id);
    } else {
        app.expanded.insert(id);
    }
}

fn apply_cursor_selector(app: &mut TuiApp, enable: Option<bool>) {
    let Some(selector) = cursor_selector(app) else {
        app.error("nothing selectable here");
        return;
    };
    let canonical = selector.canonical_string();
    let result = match enable {
        Some(true) => app.state.apply_selector(&canonical, true),
        Some(false) => app.state.apply_selector(&canonical, false),
        None => app.state.clear_selector(&canonical),
    };
    if let Err(error) = result {
        app.error(error.to_string());
    } else {
        app.info(match enable {
            Some(true) => format!("enable {canonical}"),
            Some(false) => format!("disable {canonical}"),
            None => format!("cleared {canonical}"),
        });
    }
}

fn clear_cursor_selector(app: &mut TuiApp) {
    apply_cursor_selector(app, None);
}

fn cycle_cursor_selector(app: &mut TuiApp) {
    let Some(selector) = cursor_selector(app) else {
        app.error("nothing selectable here");
        return;
    };
    let canonical = selector.canonical_string();
    let state = app.state.selector_state(&canonical);
    let result = match state {
        SelectorState::Neutral => app.state.apply_selector(&canonical, true),
        SelectorState::ExplicitEnable => app.state.apply_selector(&canonical, false),
        SelectorState::ExplicitDisable => app.state.clear_selector(&canonical),
    };
    if let Err(error) = result {
        app.error(error.to_string());
    }
}

/// Force every selector under the cursor node into a uniform state.
///
/// Because the selector specificity model guarantees that a broader selector
/// matches any descendant, we only need to (a) clear every explicit enable /
/// disable under the subtree (so no more-specific override remains) and
/// (b) apply the cursor node's own selector in the target state. Emitting the
/// descendants too would be correct but bloats `agentpack.toml` with redundant
/// entries.
fn apply_cursor_subtree(app: &mut TuiApp, enable: bool) {
    if app.state.selected_is_read_only() {
        app.error(format!("{DEFAULT_MODE_NAME} is read-only"));
        return;
    }
    let Some(id) = cursor_node_id(app) else {
        return;
    };
    let Some(node) = find_node(&app.tree, &id) else {
        return;
    };
    let mut descendants = Vec::new();
    collect_subtree_selectors(node, &mut descendants);
    let Some(root_selector) = node.selector.as_ref().map(Selector::canonical_string) else {
        app.error("nothing selectable here");
        return;
    };

    for canonical in &descendants {
        if canonical != &root_selector {
            let _ = app.state.clear_selector(canonical);
        }
    }
    if let Err(error) = app.state.apply_selector(&root_selector, enable) {
        app.error(error.to_string());
        return;
    }
    app.info(format!(
        "{} {root_selector}",
        if enable { "enabled" } else { "disabled" }
    ));
}

fn handle_prompt_key(app: &mut TuiApp, code: KeyCode, modifiers: KeyModifiers) {
    let Some(prompt) = app.prompt.clone() else {
        return;
    };
    match prompt {
        Prompt::ConfirmDelete { mode } => match code {
            KeyCode::Char('y') | KeyCode::Char('Y') => {
                app.prompt = None;
                app.state.selected_mode = mode.clone();
                match app.state.delete_selected_mode() {
                    Ok(()) => {
                        app.sync_mode_cursor();
                        app.info(format!("deleted mode {mode}"));
                    }
                    Err(error) => app.error(error.to_string()),
                }
            }
            KeyCode::Esc | KeyCode::Char('n') | KeyCode::Char('N') => app.prompt = None,
            _ => {}
        },
        Prompt::ConfirmQuitDirty => match code {
            KeyCode::Char('y') | KeyCode::Char('Y') => {
                app.save();
                if !app.state.dirty {
                    app.quit = true;
                }
                app.prompt = None;
            }
            KeyCode::Char('n') | KeyCode::Char('N') => {
                app.quit = true;
                app.prompt = None;
            }
            KeyCode::Esc => app.prompt = None,
            _ => {}
        },
        Prompt::CreateMode { mut buffer } => match text_input_key(code, modifiers, &mut buffer) {
            TextInputAction::Cancel => app.prompt = None,
            TextInputAction::Submit => match app.state.create_mode(buffer.trim()) {
                Ok(()) => {
                    app.sync_mode_cursor();
                    app.info(format!("created mode {}", buffer.trim()));
                    app.prompt = None;
                }
                Err(error) => {
                    app.error(error.to_string());
                    app.prompt = Some(Prompt::CreateMode { buffer });
                }
            },
            TextInputAction::Continue => {
                app.prompt = Some(Prompt::CreateMode { buffer });
            }
        },
        Prompt::RenameMode { mut buffer } => match text_input_key(code, modifiers, &mut buffer) {
            TextInputAction::Cancel => app.prompt = None,
            TextInputAction::Submit => match app.state.rename_selected_mode(buffer.trim()) {
                Ok(()) => {
                    app.sync_mode_cursor();
                    app.info(format!("renamed to {}", buffer.trim()));
                    app.prompt = None;
                }
                Err(error) => {
                    app.error(error.to_string());
                    app.prompt = Some(Prompt::RenameMode { buffer });
                }
            },
            TextInputAction::Continue => {
                app.prompt = Some(Prompt::RenameMode { buffer });
            }
        },
        Prompt::AddSelector { mut buffer, enable } => {
            match text_input_key(code, modifiers, &mut buffer) {
                TextInputAction::Cancel => app.prompt = None,
                TextInputAction::Submit => match submit_selector(app, buffer.trim(), enable) {
                    Ok(canonical) => {
                        app.info(format!(
                            "{} {canonical}",
                            if enable { "enable" } else { "disable" }
                        ));
                        app.prompt = None;
                    }
                    Err(error) => {
                        app.error(error.to_string());
                        app.prompt = Some(Prompt::AddSelector { buffer, enable });
                    }
                },
                TextInputAction::Continue => {
                    app.prompt = Some(Prompt::AddSelector { buffer, enable });
                }
            }
        }
    }
}

enum TextInputAction {
    Cancel,
    Submit,
    Continue,
}

fn text_input_key(code: KeyCode, modifiers: KeyModifiers, buffer: &mut String) -> TextInputAction {
    match code {
        KeyCode::Esc => TextInputAction::Cancel,
        KeyCode::Enter => TextInputAction::Submit,
        KeyCode::Backspace => {
            buffer.pop();
            TextInputAction::Continue
        }
        KeyCode::Char(c) if !modifiers.contains(KeyModifiers::CONTROL) => {
            buffer.push(c);
            TextInputAction::Continue
        }
        _ => TextInputAction::Continue,
    }
}

fn submit_selector(app: &mut TuiApp, raw: &str, enable: bool) -> Result<String> {
    let selector = Selector::parse(raw)?;
    app.catalog.validate_selector(&selector)?;
    let canonical = selector.canonical_string();
    app.state.apply_selector(&canonical, enable)?;
    Ok(canonical)
}
