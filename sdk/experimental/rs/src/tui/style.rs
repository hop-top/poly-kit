//! Theme and ANSI styling with the NO_COLOR / TTY gate.

use std::io::IsTerminal;

/// An sRGB colour.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Rgb(pub u8, pub u8, pub u8);

impl Rgb {
    /// Parse `#RRGGBB` or `RRGGBB`. `None` on anything else.
    pub fn from_hex(s: &str) -> Option<Rgb> {
        let h = s.strip_prefix('#').unwrap_or(s);
        if h.len() != 6 || !h.is_ascii() {
            return None;
        }
        let p = |i: usize| u8::from_str_radix(&h[i..i + 2], 16).ok();
        Some(Rgb(p(0)?, p(2)?, p(4)?))
    }
}

/// The palette the components draw from. Every renderer takes a
/// [`Styler`] built from one; there is no global default theme in use.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Theme {
    pub accent: Rgb,
    pub secondary: Rgb,
    pub muted: Rgb,
    pub error: Rgb,
    pub success: Rgb,
}

impl Default for Theme {
    /// The NEON palette the Python and TypeScript ports document.
    fn default() -> Self {
        Theme {
            accent: Rgb(0x7E, 0xD9, 0x57),
            secondary: Rgb(0xFF, 0x00, 0xFF),
            muted: Rgb(0x85, 0x81, 0x83),
            error: Rgb(0xED, 0x4A, 0x5E),
            success: Rgb(0x52, 0xCF, 0x84),
        }
    }
}

/// When to emit colour.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ColorMode {
    /// On for a terminal, unless `NO_COLOR` is set or `TERM=dumb`.
    Auto,
    Always,
    Never,
}

/// Applies a [`Theme`] as ANSI SGR sequences, or returns text untouched
/// when colour is off.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Styler {
    theme: Theme,
    enabled: bool,
}

impl Styler {
    /// Plain text, whatever the theme: what a pipe or a test gets.
    pub fn plain() -> Styler {
        Styler {
            theme: Theme::default(),
            enabled: false,
        }
    }

    /// Colour decision from explicit facts. `NO_COLOR` (set and non-empty,
    /// per no-color.org) beats `Always`; `Auto` also needs a terminal and
    /// a `TERM` other than `dumb`.
    pub fn resolve(
        theme: Theme,
        mode: ColorMode,
        is_tty: bool,
        no_color: bool,
        term_dumb: bool,
    ) -> Styler {
        let enabled = !no_color
            && match mode {
                ColorMode::Never => false,
                ColorMode::Always => true,
                ColorMode::Auto => is_tty && !term_dumb,
            };
        Styler { theme, enabled }
    }

    /// [`resolve`](Self::resolve) with the environment and `stream`'s
    /// terminal-ness read for you. Pass stderr for progress and prompts.
    pub fn detect<S: IsTerminal>(theme: Theme, mode: ColorMode, stream: &S) -> Styler {
        let no_color = std::env::var_os("NO_COLOR").is_some_and(|v| !v.is_empty());
        let dumb = std::env::var("TERM").is_ok_and(|t| t == "dumb");
        Styler::resolve(theme, mode, stream.is_terminal(), no_color, dumb)
    }

    pub fn theme(&self) -> &Theme {
        &self.theme
    }

    pub fn enabled(&self) -> bool {
        self.enabled
    }

    pub(crate) fn paint(&self, c: Rgb, bold: bool, text: &str) -> String {
        if !self.enabled || text.is_empty() {
            return text.to_string();
        }
        let b = if bold { "1;" } else { "" };
        format!("\x1b[{b}38;2;{};{};{}m{text}\x1b[0m", c.0, c.1, c.2)
    }

    pub(crate) fn accent(&self, t: &str) -> String {
        self.paint(self.theme.accent, false, t)
    }
    pub(crate) fn accent_bold(&self, t: &str) -> String {
        self.paint(self.theme.accent, true, t)
    }
    pub(crate) fn muted(&self, t: &str) -> String {
        self.paint(self.theme.muted, false, t)
    }
    pub(crate) fn error(&self, t: &str) -> String {
        self.paint(self.theme.error, false, t)
    }
    pub(crate) fn success(&self, t: &str) -> String {
        self.paint(self.theme.success, false, t)
    }
    pub(crate) fn secondary(&self, t: &str) -> String {
        self.paint(self.theme.secondary, false, t)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn r(mode: ColorMode, tty: bool, no_color: bool, dumb: bool) -> bool {
        Styler::resolve(Theme::default(), mode, tty, no_color, dumb).enabled()
    }

    #[test]
    fn auto_needs_tty() {
        assert!(r(ColorMode::Auto, true, false, false));
        assert!(!r(ColorMode::Auto, false, false, false));
        assert!(!r(ColorMode::Auto, true, false, true));
    }

    #[test]
    fn no_color_beats_always() {
        assert!(r(ColorMode::Always, false, false, false));
        assert!(!r(ColorMode::Always, true, true, false));
        assert!(!r(ColorMode::Never, true, false, false));
    }

    #[test]
    fn paint_plain_is_identity() {
        assert_eq!(Styler::plain().accent("x"), "x");
    }

    #[test]
    fn paint_emits_truecolor_sgr() {
        let s = Styler::resolve(Theme::default(), ColorMode::Always, false, false, false);
        assert_eq!(s.accent("x"), "\x1b[38;2;126;217;87mx\x1b[0m");
        assert_eq!(s.accent_bold("x"), "\x1b[1;38;2;126;217;87mx\x1b[0m");
    }

    #[test]
    fn hex_parse() {
        assert_eq!(Rgb::from_hex("#7ED957"), Some(Rgb(0x7E, 0xD9, 0x57)));
        assert_eq!(Rgb::from_hex("7ed957"), Some(Rgb(0x7E, 0xD9, 0x57)));
        assert_eq!(Rgb::from_hex("#7ED95"), None);
        assert_eq!(Rgb::from_hex("#GGGGGG"), None);
        assert_eq!(Rgb::from_hex("#é1234"), None);
    }
}
