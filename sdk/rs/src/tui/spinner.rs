//! Braille dot spinner frames (`tui.NewSpinner` in Go).

use super::parity::{SPINNER_FRAMES, SPINNER_INTERVAL_MS};
use super::style::Styler;
use std::time::Duration;

/// Renders spinner frames in the accent colour. The caller owns the
/// clock: advance the tick every [`Spinner::interval`].
#[derive(Debug, Clone, Copy)]
pub struct Spinner {
    styler: Styler,
}

impl Spinner {
    pub fn new(styler: Styler) -> Spinner {
        Spinner { styler }
    }

    /// Frame for `tick`, wrapping around the frame set.
    pub fn frame(&self, tick: usize) -> String {
        self.styler
            .accent(SPINNER_FRAMES[tick % SPINNER_FRAMES.len()])
    }

    pub fn interval(&self) -> Duration {
        Duration::from_millis(SPINNER_INTERVAL_MS)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn frames_wrap() {
        let s = Spinner::new(Styler::plain());
        assert_eq!(s.frame(0), "⠋");
        assert_eq!(s.frame(9), "⠏");
        assert_eq!(s.frame(10), "⠋");
    }

    #[test]
    fn interval_is_contract_value() {
        assert_eq!(
            Spinner::new(Styler::plain()).interval(),
            Duration::from_millis(80)
        );
    }
}
