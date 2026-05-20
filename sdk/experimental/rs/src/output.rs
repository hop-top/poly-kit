//! Cross-runtime output module.
//!
//! Mirrors the surface shipped in kit-go / kit-py / kit-ts / kit-php:
//!
//! - [`Formatter`] trait + [`OptionSpec`]/[`OptionType`]/[`Options`]/[`ColumnSpec`]
//! - [`Registry`] with a `default_registry()` process-wide singleton
//! - JSON + YAML built-in formatters (gated on the `output` feature)
//! - [`parse_options`] for `--format-opt key=value` validation
//!
//! CLI integration (`register_output_flags` + `dispatch`) lives behind
//! the `cli` feature so consumers can take just the formatter machinery
//! without pulling in `clap`.
//!
//! See `sdk/experimental/php/src/Output` for the canonical implementation
//! that this mirrors method-for-method.

pub mod builtins;
mod column;
mod formatter;
mod option;
mod registry;

pub use column::ColumnSpec;
pub use formatter::Formatter;
pub use option::{parse_options, OptionSpec, OptionType, OptionValue, Options, ParseError};
pub use registry::{default_registry, Registry};

#[cfg(feature = "cli")]
mod dispatch;
#[cfg(feature = "cli")]
mod flags;

#[cfg(feature = "cli")]
pub use dispatch::{dispatch, DispatchError, DispatchOptions};
#[cfg(feature = "cli")]
pub use flags::{register_output_flags, RegisterOutputFlagsOptions};
