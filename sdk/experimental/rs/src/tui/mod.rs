//! Terminal UI primitives for kit CLIs: gated behind the `tui` feature.
//!
//! Std only, plain ANSI: no raw mode, no widget framework, no
//! dependencies. Every renderer returns a `String` and every prompt takes
//! its reader and writer, so nothing here needs a real terminal. See the
//! module README for the ratatui-versus-ANSI decision and the parity
//! table against `go/console/tui` and `go/console/progress`.
//!
//! - [`progress`]: per-phase progress events as JSONL or human lines
//!   (`go/console/progress`)
//! - [`Bar`], [`Spinner`], [`status_line`]: themed one-line renderers
//! - [`Confirm`]: yes/no prompt, line based, with a non-interactive
//!   fallback
//! - [`AppShell`]: header / main / footer frame and canonical keymap, as a
//!   pure state machine the caller's event loop drives

mod appshell;
mod bar;
mod confirm;
mod parity;
pub mod progress;
mod spinner;
mod status;
mod style;

pub use appshell::{AppRenderer, AppShell, Cmd, KeyMap, Msg};
pub use bar::{Bar, DEFAULT_BAR_WIDTH};
pub use confirm::{Confirm, ConfirmState};
pub use parity::{SPINNER_FRAMES, SPINNER_INTERVAL_MS};
pub use spinner::Spinner;
pub use status::{status_line, InfoType};
pub use style::{ColorMode, Rgb, Styler, Theme};
