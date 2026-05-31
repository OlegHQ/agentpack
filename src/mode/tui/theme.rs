//! Adaptive color palette for the mode TUI.
//!
//! Terminals do not tell ratatui whether the user runs a light or dark
//! background, so bright colors (`LightGreen`, `Yellow`, `Gray`) that read fine
//! on a dark terminal wash out to near-invisible on a white one. [`Theme`] keeps
//! the per-role colors in one place and [`Theme::detect`] picks the right
//! palette: an explicit `AGENTPACK_TUI_THEME` override wins, otherwise we ask
//! the terminal for its background luma, and fall back to the dark palette when
//! detection is unavailable (no TTY, query unsupported).

use std::io::IsTerminal;

use ratatui::style::Color;

/// Semantic colors used across the renderer. Foreground text that should follow
/// the terminal's own default color is left as `Style::default()` at the call
/// site and intentionally has no entry here; only roles that need an explicit
/// color (or a selection background) live on the theme.
#[derive(Clone, Copy, Debug)]
pub struct Theme {
    /// Focused pane titles and text-input echoes.
    pub accent: Color,
    /// Subtitles, hints, "(none)", "(read-only)" — secondary text.
    pub dim: Color,
    /// Modal / help titles.
    pub heading: Color,
    /// Reserved-mode bullet and other "heads up" marks.
    pub warn: Color,
    /// "enabled" / base=all glyphs.
    pub enabled: Color,
    /// "disabled" / base=none glyphs.
    pub disabled: Color,
    /// Neutral (inherited / non-selectable) glyphs.
    pub neutral: Color,
    /// Success status line.
    pub success: Color,
    /// Error status line.
    pub error: Color,
    /// Selection-row background while the pane is focused.
    pub sel_focused_bg: Color,
    /// Selection-row background while the pane is unfocused.
    pub sel_unfocused_bg: Color,
}

impl Theme {
    /// Pick a palette: explicit override → terminal background luma → dark.
    pub fn detect() -> Self {
        match std::env::var("AGENTPACK_TUI_THEME") {
            Ok(v) if v.eq_ignore_ascii_case("light") => return Self::light(),
            Ok(v) if v.eq_ignore_ascii_case("dark") => return Self::dark(),
            _ => {}
        }
        if !std::io::stdout().is_terminal() {
            return Self::dark();
        }
        // `luma` is 0.0 (black) .. 1.0 (white); the crate documents 0.6 as the
        // sensible "rather dark vs. rather light" pivot.
        match terminal_light::luma() {
            Ok(luma) if luma > 0.6 => Self::light(),
            _ => Self::dark(),
        }
    }

    pub fn dark() -> Self {
        Self {
            accent: Color::LightCyan,
            dim: Color::DarkGray,
            heading: Color::LightYellow,
            warn: Color::Yellow,
            enabled: Color::Green,
            disabled: Color::Red,
            neutral: Color::DarkGray,
            success: Color::LightGreen,
            error: Color::LightRed,
            sel_focused_bg: Color::Rgb(38, 70, 120),
            sel_unfocused_bg: Color::Rgb(48, 48, 48),
        }
    }

    pub fn light() -> Self {
        Self {
            accent: Color::Rgb(0, 95, 175),
            dim: Color::Rgb(110, 110, 110),
            heading: Color::Rgb(150, 90, 0),
            warn: Color::Rgb(165, 100, 0),
            enabled: Color::Rgb(0, 130, 0),
            disabled: Color::Rgb(185, 0, 0),
            neutral: Color::Rgb(130, 130, 130),
            success: Color::Rgb(0, 120, 0),
            error: Color::Rgb(185, 0, 0),
            sel_focused_bg: Color::Rgb(173, 210, 255),
            sel_unfocused_bg: Color::Rgb(220, 220, 220),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serial_test::serial;

    #[test]
    #[serial]
    fn env_override_forces_palette_regardless_of_terminal() {
        std::env::set_var("AGENTPACK_TUI_THEME", "light");
        assert_eq!(Theme::detect().accent, Theme::light().accent);
        std::env::set_var("AGENTPACK_TUI_THEME", "DARK");
        assert_eq!(Theme::detect().accent, Theme::dark().accent);
        std::env::remove_var("AGENTPACK_TUI_THEME");
    }
}
