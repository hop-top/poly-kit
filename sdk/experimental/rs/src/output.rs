//! Cross-runtime output module.
//!
//! Mirrors the surface shipped in kit-go / kit-py / kit-ts / kit-php:
//!
//! - [`Formatter`] trait + [`OptionSpec`]/[`OptionType`]/[`Options`]/[`ColumnSpec`]
//! - [`Registry`] with a `default_registry()` process-wide singleton
//! - JSON + YAML built-in formatters (gated on the `output` feature)
//! - [`parse_options`] for `--format-opt key=value` validation
//! - [`CliError`] structured-error envelope + [`render_error`] with the
//!   transience class (transient|permanent|unknown) and the cross-tool
//!   exit-code constants
//!
//! CLI integration (`register_output_flags` + `dispatch`) lives behind
//! the `cli` feature so consumers can take just the formatter machinery
//! without pulling in `clap`.
//!
//! See `sdk/experimental/php/src/Output` for the canonical implementation
//! that this mirrors method-for-method.

pub mod builtins;
mod column;
mod error;
mod formatter;
mod option;
mod registry;

pub use column::ColumnSpec;
pub use error::{
    exit_classes, exit_code_for_class, render_error, transience_for_code, CliError, CODE_CONFLICT,
    CODE_CONSENT_REFUSED, CODE_GENERIC, CODE_NOT_FOUND, CODE_OK, CODE_PREREQUISITE,
    CODE_PROVENANCE_MISSING, CODE_RATE_LIMITED, CODE_TRANSIENT, CODE_UNAUTHORIZED, CODE_USAGE,
    EXIT_CONFLICT, EXIT_CONSENT_REFUSED, EXIT_GENERIC, EXIT_NOT_FOUND, EXIT_OK, EXIT_PREREQUISITE,
    EXIT_PROVENANCE_MISSING, EXIT_RATE_LIMITED, EXIT_TRANSIENT, EXIT_UNAUTHORIZED, EXIT_USAGE,
    TRANSIENCE_PERMANENT, TRANSIENCE_TRANSIENT, TRANSIENCE_UNKNOWN,
};
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
