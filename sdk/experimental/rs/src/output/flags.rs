//! clap integration: `register_output_flags` adds the standard output
//! flag suite (--format, --format-opt, --format-help, --cols, --columns,
//! --template, -o/--output) to a clap `Command`. Mirrors
//! py `register_output_flags`, ts `registerOutputFlags`, php `Flags::register`.

use std::sync::Arc;

use clap::{Arg, ArgAction, Command};

use super::registry::{default_registry, Registry};

/// Options for [`register_output_flags`].
#[derive(Default)]
pub struct RegisterOutputFlagsOptions {
    /// Custom registry (default: process-wide [`default_registry`]).
    pub registry: Option<Arc<Registry>>,
    pub disable_format: bool,
    pub disable_format_opt: bool,
    pub disable_format_help: bool,
    pub disable_cols: bool,
    pub disable_template: bool,
    pub disable_output: bool,
}

/// Adds the output flag suite to `cmd` as global args, so every
/// subcommand inherits them automatically.
///
/// The active registry is stored on the returned [`OutputFlagsContext`]
/// — pass it (or call [`default_registry`]) when invoking
/// [`crate::output::dispatch`].
pub fn register_output_flags(
    mut cmd: Command,
    opts: RegisterOutputFlagsOptions,
) -> (Command, OutputFlagsContext) {
    let registry = opts.registry.unwrap_or_else(default_registry);

    if !opts.disable_format {
        cmd = cmd.arg(
            Arg::new("format")
                .long("format")
                .help(format!(
                    "Output format ({})",
                    registry.keys().join(", ")
                ))
                .default_value("table")
                .global(true),
        );
    }
    if !opts.disable_format_opt {
        cmd = cmd.arg(
            Arg::new("format-opt")
                .long("format-opt")
                .help("Per-format option as key=value (repeatable; bool keys may omit =value)")
                .action(ArgAction::Append)
                .global(true)
                .hide(true),
        );
    }
    if !opts.disable_format_help {
        cmd = cmd.arg(
            Arg::new("format-help")
                .long("format-help")
                .help("Show available formats and their options")
                .num_args(0..=1)
                .require_equals(false)
                .global(true)
                .hide(true),
        );
    }
    if !opts.disable_cols {
        cmd = cmd.arg(
            Arg::new("cols")
                .long("cols")
                .help("Restrict columns to this comma-separated list (repeatable)")
                .action(ArgAction::Append)
                .global(true)
                .hide(true),
        );
        cmd = cmd.arg(
            Arg::new("columns")
                .long("columns")
                .help("Alias for --cols")
                .action(ArgAction::Append)
                .global(true)
                .hide(true),
        );
    }
    if !opts.disable_template {
        cmd = cmd.arg(
            Arg::new("template")
                .long("template")
                .help("Template applied to results (mutually exclusive with --cols)")
                .global(true)
                .hide(true),
        );
    }
    if !opts.disable_output {
        cmd = cmd.arg(
            Arg::new("output")
                .long("output")
                .short('o')
                .help("Write output to path (use - or empty for stdout)")
                .default_value("")
                .global(true)
                .hide(true),
        );
    }

    (cmd, OutputFlagsContext { registry })
}

/// Returned by [`register_output_flags`]; pass to [`crate::output::dispatch`]
/// when you want to use a registry other than the process-wide default.
pub struct OutputFlagsContext {
    pub registry: Arc<Registry>,
}
