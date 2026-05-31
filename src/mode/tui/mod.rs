//! Interactive mode editor (ratatui TUI).
//!
//! Split by responsibility:
//! * [`state`] — pure reducer over the modes map (unit-tested in isolation),
//! * [`tree`] — capability-tree data model + builder,
//! * [`app`] — `TuiApp` runtime state shared by events and rendering,
//! * [`events`] — key-event handling,
//! * [`render`] — ratatui drawing.

mod app;
mod events;
mod render;
mod state;
mod theme;
mod tree;

use std::io;
use std::path::Path;

use crossterm::event;
use crossterm::execute;
use crossterm::terminal::{
    disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen,
};
use ratatui::backend::CrosstermBackend;
use ratatui::Terminal;

use crate::error::{AgentpackError, Result};
use crate::manifest::AgentpackManifest;

use super::catalog::CapabilityCatalog;

use app::TuiApp;
use events::handle_event;
use render::render;
use theme::Theme;

pub fn run_mode_tui(
    project_root: &Path,
    manifest: &AgentpackManifest,
    catalog: &CapabilityCatalog,
    selected_mode: Option<&str>,
) -> Result<()> {
    // Probe the terminal background before we take over the screen — the query
    // writes/reads an escape sequence that must not race the alternate screen.
    let theme = Theme::detect();
    let mut app = TuiApp::new(project_root, manifest, catalog.clone(), selected_mode, theme)?;

    let mut terminal = enter_tui(project_root)?;
    let run_result = run_event_loop(&mut terminal, &mut app, project_root);
    leave_tui(&mut terminal);
    run_result
}

fn enter_tui(project_root: &Path) -> Result<Terminal<CrosstermBackend<io::Stdout>>> {
    enable_raw_mode().map_err(|error| AgentpackError::io(project_root, error))?;
    let mut stdout = io::stdout();
    // No mouse capture: the TUI is fully keyboard-driven, and capturing the
    // mouse would only disable the terminal's native text selection and scroll.
    if let Err(error) = execute!(stdout, EnterAlternateScreen) {
        disable_raw_mode().ok();
        return Err(AgentpackError::io(project_root, error));
    }
    Terminal::new(CrosstermBackend::new(stdout)).map_err(|error| {
        // Unwind the raw mode / alt-screen state so the user's terminal is usable.
        execute!(io::stdout(), LeaveAlternateScreen).ok();
        disable_raw_mode().ok();
        AgentpackError::io(project_root, error)
    })
}

fn leave_tui(terminal: &mut Terminal<CrosstermBackend<io::Stdout>>) {
    disable_raw_mode().ok();
    execute!(terminal.backend_mut(), LeaveAlternateScreen).ok();
    terminal.show_cursor().ok();
}

fn run_event_loop(
    terminal: &mut Terminal<CrosstermBackend<io::Stdout>>,
    app: &mut TuiApp,
    project_root: &Path,
) -> Result<()> {
    loop {
        terminal
            .draw(|frame| render(frame, app))
            .map_err(|error| AgentpackError::io(project_root, error))?;
        if app.quit {
            break;
        }
        let event = event::read().map_err(|error| AgentpackError::io(project_root, error))?;
        handle_event(app, event);
    }
    Ok(())
}
