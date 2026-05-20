//! Formatter trait.
//!
//! Mirrors py `Formatter` Protocol, ts `Formatter<T>` interface, go
//! `Formatter` interface, and php `Formatter` interface.

use std::io::Write;

use serde_json::Value;

use super::option::{OptionSpec, Options};

/// A Formatter encodes structured data to a writable stream.
///
/// Implementations declare their key, file extensions, and the option
/// keys they accept. The Dispatcher validates `--format-opt` input
/// against `options()` before invoking `render()`, so `render()` may
/// trust `opts` to contain only declared keys with values coerced to
/// declared types.
pub trait Formatter: Send + Sync {
    /// Unique format identifier exposed via `--format <key>`.
    fn key(&self) -> &'static str;

    /// File extensions (with leading dot, e.g. `".csv"`) that map to
    /// this formatter for `--output` extension inference. May be empty.
    fn extensions(&self) -> &'static [&'static str];

    /// Option specs accepted by this formatter via `--format-opt key=value`.
    fn options(&self) -> &'static [OptionSpec];

    /// Render `data` to `out`.
    ///
    /// `opts` contains only validated option values keyed by
    /// [`OptionSpec::name`]; missing keys mean the caller did not set
    /// them (render should fall back to spec defaults — which the
    /// Dispatcher already merges in via `parse_options`).
    ///
    /// `cols` is the user-requested column projection; an empty slice
    /// means "all default columns".
    fn render(
        &self,
        out: &mut dyn Write,
        data: &Value,
        opts: &Options,
        cols: &[String],
    ) -> std::io::Result<()>;
}
