//! The `serve` command: `serve [SERVICE]` with `--list`, `--enable`,
//! `--disable` and the three timeout flags, mounted on any clap root.
//!
//! Mirrors the TypeScript port's `registerServe(program, opts)` rather
//! than Go's root option: an adopter hands over whatever `clap::Command`
//! it already has and gets it back with `serve` mounted. The kit-owned
//! root factory, when it lands, mounts the same command through its own
//! option; nothing here depends on it.
//!
//! Two things the parser is deliberately *not* asked to do:
//!
//! - Arity. `serve a b` parses; [`run`] hands the operands to
//!   [`resolve`], which refuses two or more at `USAGE`/2. Leaving the
//!   refusal to clap would produce clap's own error and exit code, and
//!   the contract's arity refusal belongs to the shared taxonomy.
//! - Name validation. An unknown service is `NOT_FOUND`/3 from the
//!   registration gate, not a parse error, so the positional carries
//!   no possible-values list.
//!
//! Process exit stays with the adopter's `main`: this is a library
//! crate, and [`run`] returns the exit code rather than calling
//! `std::process::exit` on the caller's behalf.
//!
//! # Example
//!
//! ```no_run
//! use std::sync::Arc;
//! use hop_top_kit::serve::command::{mount, ServeCommandOptions};
//! use hop_top_kit::serve::{ServiceConfig, ServiceConfigs, ServiceRegistry};
//! # use hop_top_kit::serve::{CancelToken, ReadySignal, ServeFuture, Service};
//! # struct Api;
//! # impl Service for Api {
//! #     fn name(&self) -> &str { "api" }
//! #     fn start<'a>(&'a self, cancel: CancelToken, ready: ReadySignal)
//! #         -> ServeFuture<'a, Result<(), String>> {
//! #         Box::pin(async move { ready.report(); cancel.cancelled().await; Ok(()) })
//! #     }
//! #     fn ready(&self) -> bool { true }
//! #     fn stop<'a>(&'a self, _: CancelToken) -> ServeFuture<'a, Result<(), String>> {
//! #         Box::pin(async { Ok(()) })
//! #     }
//! # }
//!
//! #[tokio::main]
//! async fn main() {
//!     let mut registry = ServiceRegistry::new();
//!     registry.register(Arc::new(Api)).expect("wiring");
//!
//!     let mut configs = ServiceConfigs::new();
//!     configs.insert("api".to_string(), ServiceConfig::enabled());
//!
//!     let root = mount(clap::Command::new("tool"));
//!     let matches = root.get_matches();
//!     if let Some(sub) = matches.subcommand_matches("serve") {
//!         let mut opts = ServeCommandOptions::new(registry);
//!         opts.configs = Some(configs);
//!         let _ = (sub, opts);
//!     }
//! }
//! ```

use std::io::Write;
use std::sync::Arc;
use std::time::Duration;

use clap::{Arg, ArgAction, ArgMatches, Command};

use crate::output::CliError;

use super::config::{parse_duration, ServiceConfigs, SupervisorConfig};
use super::events::{Publisher, ServeLogger};
use super::registry::{PolicyGate, ServiceRegistry};
use super::resolve::list_services;

/// The command word. `serve` is the parent for both forms.
pub const COMMAND_NAME: &str = "serve";
/// The positional: zero for the supervisor form, one for the selector.
pub const ARG_SERVICE: &str = "service";
/// `--list`: the inspection form. A flag, not a child, because `list`
/// is reserved selector vocabulary.
pub const FLAG_LIST: &str = "list";
/// `--enable <NAME>`: enable a service for this run (supervisor form).
pub const FLAG_ENABLE: &str = "enable";
/// `--disable <NAME>`: disable a service for this run (supervisor form).
pub const FLAG_DISABLE: &str = "disable";
/// `--ready-timeout <DURATION>`: per-service start-to-ready budget.
pub const FLAG_READY_TIMEOUT: &str = "ready-timeout";
/// `--stop-timeout <DURATION>`: per-service stop budget.
pub const FLAG_STOP_TIMEOUT: &str = "stop-timeout";
/// `--shutdown-timeout <DURATION>`: total shutdown budget.
pub const FLAG_SHUTDOWN_TIMEOUT: &str = "shutdown-timeout";

/// The refusal for `--enable`/`--disable` under the selector form,
/// verbatim from the Go port.
pub const SELECTOR_FLAGS_REFUSAL: &str =
    "--enable/--disable apply to the supervisor form; drop the service name or drop the flags";

/// What [`run`] needs beyond the parsed matches. Mirrors the TypeScript
/// `RegisterServeOptions`.
pub struct ServeCommandOptions {
    /// The seam services were registered into.
    pub registry: ServiceRegistry,
    /// Resolved `services.<name>` blocks. `None` is the same as empty:
    /// nothing configured, so the supervisor form resolves to zero
    /// services unless `--enable` names some.
    pub configs: Option<ServiceConfigs>,
    /// The supervisor-scoped half of the `services` block.
    pub config: SupervisorConfig,
    /// The third validation gate.
    pub policy: Option<Arc<dyn PolicyGate>>,
    pub publisher: Option<Arc<dyn Publisher>>,
    /// Defaults to the stderr logger.
    pub logger: Option<Arc<dyn ServeLogger>>,
    /// Where `--list` writes. Defaults to the process stdout.
    pub stdout: Box<dyn Write>,
}

impl ServeCommandOptions {
    /// Options with every sink at its default.
    pub fn new(registry: ServiceRegistry) -> Self {
        ServeCommandOptions {
            registry,
            configs: None,
            config: SupervisorConfig::default(),
            policy: None,
            publisher: None,
            logger: None,
            stdout: Box::new(std::io::stdout()),
        }
    }
}

/// Builds the `serve [SERVICE]...` command with its flags.
///
/// The positional accepts any number of operands on purpose; see the
/// module docs for why arity is refused downstream.
pub fn serve_command() -> Command {
    Command::new(COMMAND_NAME)
        .about("Run configured services under one lifecycle")
        .long_about(
            "Run every configured and enabled service under one lifecycle,\n\
             or exactly the named service.\n\n\
             Naming a service starts it even when it is not enabled, provided\n\
             its configuration and policy validate.",
        )
        .arg(
            Arg::new(ARG_SERVICE)
                .value_name("SERVICE")
                .num_args(0..)
                .help("Service to run (at most one; omit to run every enabled service)"),
        )
        .arg(
            Arg::new(FLAG_LIST)
                .long(FLAG_LIST)
                .action(ArgAction::SetTrue)
                .help("List registered services and their state"),
        )
        .arg(
            Arg::new(FLAG_ENABLE)
                .long(FLAG_ENABLE)
                .value_name("NAME")
                .action(ArgAction::Append)
                .help("Enable a service for this run (repeatable, supervisor form only)"),
        )
        .arg(
            Arg::new(FLAG_DISABLE)
                .long(FLAG_DISABLE)
                .value_name("NAME")
                .action(ArgAction::Append)
                .help("Disable a service for this run (repeatable, supervisor form only)"),
        )
        .arg(duration_flag(
            FLAG_READY_TIMEOUT,
            "Per-service budget from start to ready (default 30s)",
        ))
        .arg(duration_flag(
            FLAG_STOP_TIMEOUT,
            "Per-service budget for one stop (default 30s)",
        ))
        .arg(duration_flag(
            FLAG_SHUTDOWN_TIMEOUT,
            "Total shutdown budget across all services (default 60s)",
        ))
}

/// [`serve_command`] with the registered names appended to the help,
/// so `serve --help` answers "which services can I name?" — the Go
/// port's help addendum. The names are help text only; an unknown
/// operand is still the registration gate's `NOT_FOUND`.
pub fn serve_command_for(registry: &ServiceRegistry) -> Command {
    let names = registry.names();
    if names.is_empty() {
        return serve_command();
    }
    serve_command().after_help(format!("Services: {}", names.join(", ")))
}

fn duration_flag(name: &'static str, help: &'static str) -> Arg {
    Arg::new(name)
        .long(name)
        .value_name("DURATION")
        .value_parser(parse_duration)
        .help(help)
}

/// Returns `root` with `serve` mounted.
///
/// Exactly one command owns the word: a `serve` already on `root` (a
/// leaf some other option mounted earlier) is replaced, never kept
/// beside this one.
pub fn mount(root: Command) -> Command {
    mount_command(root, serve_command())
}

/// [`mount`] with [`serve_command_for`]'s help addendum.
pub fn mount_for(root: Command, registry: &ServiceRegistry) -> Command {
    mount_command(root, serve_command_for(registry))
}

fn mount_command(root: Command, cmd: Command) -> Command {
    if root.find_subcommand(COMMAND_NAME).is_some() {
        return root.mut_subcommand(COMMAND_NAME, |_| cmd);
    }
    root.subcommand(cmd)
}

/// The positional operands, in order.
pub fn operands(matches: &ArgMatches) -> Vec<String> {
    matches
        .get_many::<String>(ARG_SERVICE)
        .map(|v| v.cloned().collect())
        .unwrap_or_default()
}

/// Applies the flag overrides onto the resolved configuration, with
/// the Go port's semantics.
///
/// - `--enable NAME` marks the service configured *and* enabled: an
///   unconfigured service becomes configured the moment an operator
///   names it, the aggregate equivalent of the selector's override.
/// - `--disable NAME` clears enablement on a configured service and is
///   a no-op on an unconfigured one. `--enable` wins when both name
///   the same service: the affirmative act is the more specific one.
/// - Either flag under the selector form (`selector == true`) is
///   refused at `USAGE`/2: the override rule already decides
///   enablement there, and one invocation must not say two things.
/// - `--ready-timeout` / `--stop-timeout` set the budget on every
///   resolved service; `--shutdown-timeout` sets the supervisor's.
///   Absent or zero leaves the configured value alone.
// CliError is the crate's own envelope and the supervisor's RunResult
// already carries one inline; boxing it here alone would give this one
// function a different shape from every other refusal path.
#[allow(clippy::result_large_err)]
pub fn apply_flags(
    matches: &ArgMatches,
    configs: &mut ServiceConfigs,
    supervisor: &mut SupervisorConfig,
    selector: bool,
) -> Result<(), CliError> {
    let enable: Vec<String> = repeated(matches, FLAG_ENABLE);
    let disable: Vec<String> = repeated(matches, FLAG_DISABLE);

    if selector && !(enable.is_empty() && disable.is_empty()) {
        return Err(CliError::usage(SELECTOR_FLAGS_REFUSAL));
    }

    for name in &disable {
        if let Some(cfg) = configs.get_mut(name) {
            cfg.enabled = false;
        }
    }
    for name in &enable {
        configs.entry(name.clone()).or_default().enabled = true;
    }

    let ready = positive(matches, FLAG_READY_TIMEOUT);
    let stop = positive(matches, FLAG_STOP_TIMEOUT);
    if ready.is_some() || stop.is_some() {
        for cfg in configs.values_mut() {
            if let Some(d) = ready {
                cfg.ready_timeout = d;
            }
            if let Some(d) = stop {
                cfg.stop_timeout = d;
            }
        }
    }
    if let Some(d) = positive(matches, FLAG_SHUTDOWN_TIMEOUT) {
        supervisor.shutdown_timeout = d;
    }
    Ok(())
}

fn repeated(matches: &ArgMatches, flag: &str) -> Vec<String> {
    matches
        .get_many::<String>(flag)
        .map(|v| v.cloned().collect())
        .unwrap_or_default()
}

/// A duration flag's value when present and non-zero. Zero is "leave
/// the configured value alone", as in the Go port.
fn positive(matches: &ArgMatches, flag: &str) -> Option<Duration> {
    matches
        .get_one::<Duration>(flag)
        .copied()
        .filter(|d| !d.is_zero())
}

/// Writes the `--list` table: every registered service with its
/// configured, enabled and ready state, in registration order.
///
/// The ordering is contract; the columns are not, and reuse the
/// Go/TypeScript header so an operator moving between tools written in
/// different languages reads the same table.
pub fn run_list(
    registry: &ServiceRegistry,
    configs: Option<&ServiceConfigs>,
    w: &mut dyn Write,
) -> std::io::Result<()> {
    writeln!(
        w,
        "{:<20} {:<11} {:<8} READY",
        "SERVICE", "CONFIGURED", "ENABLED"
    )?;
    for row in list_services(registry, configs) {
        writeln!(
            w,
            "{:<20} {:<11} {:<8} {}",
            row.name, row.configured, row.enabled, row.ready
        )?;
    }
    Ok(())
}
