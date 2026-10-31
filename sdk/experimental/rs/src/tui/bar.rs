//! Themed progress bar (`tui.Progress` in Go).

use super::style::Styler;

/// Default bar width in cells, as in Go.
pub const DEFAULT_BAR_WIDTH: usize = 40;

const FULL: char = '█';
const EMPTY: char = '░';

/// A one-line progress bar. Value type: setters return a copy.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Bar {
    percent: f64,
    width: usize,
    styler: Styler,
}

impl Bar {
    pub fn new(styler: Styler) -> Bar {
        Bar {
            percent: 0.0,
            width: DEFAULT_BAR_WIDTH,
            styler,
        }
    }

    /// Clamped to `[0, 1]`; NaN reads as 0.
    pub fn set_percent(mut self, v: f64) -> Bar {
        self.percent = if v.is_nan() { 0.0 } else { v.clamp(0.0, 1.0) };
        self
    }

    /// Width in cells, at least 1.
    pub fn set_width(mut self, w: usize) -> Bar {
        self.width = w.max(1);
        self
    }

    pub fn percent(&self) -> f64 {
        self.percent
    }

    pub fn width(&self) -> usize {
        self.width
    }

    /// The bar: filled cells in the accent colour, the track muted. The
    /// filled count truncates, so 100% is the only full bar.
    pub fn view(&self) -> String {
        let filled = ((self.percent * self.width as f64) as usize).min(self.width);
        let full: String = FULL.to_string().repeat(filled);
        let empty: String = EMPTY.to_string().repeat(self.width - filled);
        self.styler.accent(&full) + &self.styler.muted(&empty)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tui::style::{ColorMode, Theme};

    fn bar() -> Bar {
        Bar::new(Styler::plain())
    }

    #[test]
    fn zero_percent_has_no_filled_cells() {
        let v = bar().view();
        assert!(!v.contains('█'));
        assert_eq!(v.chars().count(), DEFAULT_BAR_WIDTH);
    }

    #[test]
    fn set_percent() {
        assert!((bar().set_percent(0.5).percent() - 0.5).abs() < 1e-3);
    }

    #[test]
    fn clamp_percent() {
        assert_eq!(bar().set_percent(-1.0).percent(), 0.0);
        assert_eq!(bar().set_percent(2.0).percent(), 1.0);
        assert_eq!(bar().set_percent(f64::NAN).percent(), 0.0);
    }

    #[test]
    fn full_width_has_no_empty_cells() {
        assert!(!bar().set_percent(1.0).view().contains('░'));
    }

    #[test]
    fn truncates_filled_count() {
        let v = bar().set_width(10).set_percent(0.99).view();
        assert_eq!(v.chars().filter(|&c| c == '█').count(), 9);
    }

    #[test]
    fn width_floor_is_one() {
        assert_eq!(bar().set_width(0).width(), 1);
    }

    #[test]
    fn colour_only_when_enabled() {
        let on = Styler::resolve(Theme::default(), ColorMode::Always, false, false, false);
        assert!(Bar::new(on).set_percent(0.5).view().contains("\x1b["));
        assert!(!bar().set_percent(0.5).view().contains('\x1b'));
    }
}
