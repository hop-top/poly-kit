//! Prefixed status lines (`tui.InfoMsg` rendering in Go).

use super::parity::{SYMBOL_ERROR, SYMBOL_INFO, SYMBOL_SUCCESS, SYMBOL_WARN};
use super::sanitize::clean;
use super::style::Styler;

/// Classifies a status line.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum InfoType {
    Info,
    Error,
    Warn,
    Success,
}

impl InfoType {
    /// The contract symbol for this kind.
    pub fn symbol(self) -> &'static str {
        match self {
            InfoType::Info => SYMBOL_INFO,
            InfoType::Error => SYMBOL_ERROR,
            InfoType::Warn => SYMBOL_WARN,
            InfoType::Success => SYMBOL_SUCCESS,
        }
    }
}

/// `<symbol> <text>`, symbol and text coloured by kind: info accent,
/// error red, warn secondary, success green.
pub fn status_line(styler: &Styler, kind: InfoType, text: &str) -> String {
    let paint = |t: &str| match kind {
        InfoType::Info => styler.accent(t),
        InfoType::Error => styler.error(t),
        InfoType::Warn => styler.secondary(t),
        InfoType::Success => styler.success(t),
    };
    format!("{} {}", paint(kind.symbol()), paint(&clean(text)))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tui::style::{ColorMode, Theme};

    #[test]
    fn plain_line_has_symbol_and_text() {
        let s = Styler::plain();
        assert_eq!(status_line(&s, InfoType::Success, "done"), "✓ done");
        assert_eq!(status_line(&s, InfoType::Error, "boom"), "● boom");
        assert_eq!(status_line(&s, InfoType::Warn, "hmm"), "▲ hmm");
        assert_eq!(status_line(&s, InfoType::Info, "fyi"), "ℹ fyi");
    }

    #[test]
    fn text_is_stripped_of_controls() {
        use crate::tui::sanitize::{assert_inert, HOSTILE};
        let s = Styler::resolve(Theme::default(), ColorMode::Always, false, false, false);
        for h in HOSTILE {
            assert_inert(&status_line(&Styler::plain(), InfoType::Error, h));
            let out = status_line(&s, InfoType::Error, h);
            assert!(
                !out.contains(h) && !out.contains('\r') && !out.contains('\u{9b}'),
                "{out:?}"
            );
        }
    }

    #[test]
    fn every_kind_renders_message() {
        let s = Styler::resolve(Theme::default(), ColorMode::Always, false, false, false);
        for k in [
            InfoType::Info,
            InfoType::Error,
            InfoType::Warn,
            InfoType::Success,
        ] {
            assert!(status_line(&s, k, "test").contains("test"));
        }
    }

    #[test]
    fn kinds_use_distinct_colours() {
        let s = Styler::resolve(Theme::default(), ColorMode::Always, false, false, false);
        let a = status_line(&s, InfoType::Error, "x");
        let b = status_line(&s, InfoType::Success, "x");
        assert_ne!(a, b);
    }
}
