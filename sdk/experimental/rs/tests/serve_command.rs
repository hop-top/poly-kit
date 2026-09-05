//! Conformance tests for the command half of the serve contract
//! (`docs/contracts/serve-lifecycle.md`, §"Cross-language parity"):
//! the `serve [SERVICE]` hierarchy as parsed commands, `--list` as a
//! flag, and the `--enable` / `--disable` / timeout flags with the
//! reference port's semantics.

#![cfg(feature = "serve-cli")]

use std::io::Write;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use clap::Command;
use hop_top_kit::output::{CODE_NOT_FOUND, CODE_UNAUTHORIZED, CODE_USAGE};
use hop_top_kit::serve::command::{
    apply_flags, mount, mount_for, operands, parse_failure_policy, run, run_list, serve_command,
    serve_command_for, ServeCommandOptions, SELECTOR_FLAGS_REFUSAL,
};
use hop_top_kit::serve::{
    resolve, CancelToken, FailurePolicy, LifecycleOutcome, PolicyGate, ReadySignal, ResolveRequest,
    RunResult, ServeFuture, Service, ServiceConfig, ServiceConfigs, ServiceRegistry,
    SupervisorConfig, Verdict, DEFAULT_READY_TIMEOUT, DEFAULT_SHUTDOWN_TIMEOUT,
    DEFAULT_STOP_TIMEOUT,
};

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

struct Fake(&'static str);

/// A service declaring a policy class, so the gate has something to
/// deny.
struct Classed(&'static str);

impl Service for Classed {
    fn name(&self) -> &str {
        self.0
    }
    fn start<'a>(
        &'a self,
        cancel: CancelToken,
        ready: ReadySignal,
    ) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move {
            ready.report();
            cancel.cancelled().await;
            Ok(())
        })
    }
    fn ready(&self) -> bool {
        false
    }
    fn stop<'a>(&'a self, _: CancelToken) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async { Ok(()) })
    }
    fn class(&self) -> Option<(String, String)> {
        Some(("write".to_string(), "egress".to_string()))
    }
}

struct DenyAll;

impl PolicyGate for DenyAll {
    fn allow(&self, _: &str, _: &str) -> Verdict {
        Verdict::deny("policy says no")
    }
}

impl Service for Fake {
    fn name(&self) -> &str {
        self.0
    }
    fn start<'a>(
        &'a self,
        cancel: CancelToken,
        ready: ReadySignal,
    ) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move {
            ready.report();
            cancel.cancelled().await;
            Ok(())
        })
    }
    fn ready(&self) -> bool {
        false
    }
    fn stop<'a>(&'a self, _: CancelToken) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async { Ok(()) })
    }
}

fn registry(names: &[&'static str]) -> ServiceRegistry {
    let mut r = ServiceRegistry::new();
    for n in names {
        r.register(Arc::new(Fake(n))).unwrap();
    }
    r
}

/// A `Write` the test can read back after handing it over as a
/// `Box<dyn Write>`.
#[derive(Clone, Default)]
struct Sink(Arc<Mutex<Vec<u8>>>);

impl Sink {
    fn text(&self) -> String {
        String::from_utf8(self.0.lock().unwrap().clone()).unwrap()
    }
}

impl Write for Sink {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        self.0.lock().unwrap().extend_from_slice(buf);
        Ok(buf.len())
    }
    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

fn parse(argv: &[&str]) -> clap::ArgMatches {
    serve_command()
        .try_get_matches_from(argv)
        .unwrap_or_else(|e| panic!("{argv:?}: {e}"))
}

fn root_with_serve() -> Command {
    mount(Command::new("tool").subcommand(Command::new("other")))
}

// ---------------------------------------------------------------------------
// The command and its flags
// ---------------------------------------------------------------------------

#[test]
fn every_flag_parses_with_its_contract_name() {
    let m = parse(&[
        "serve",
        "--list",
        "--enable",
        "a",
        "--enable",
        "b",
        "--disable",
        "c",
        "--ready-timeout",
        "5s",
        "--stop-timeout",
        "1m30s",
        "--shutdown-timeout",
        "2h",
        "api",
    ]);
    assert!(m.get_flag("list"));
    let enable: Vec<&String> = m.get_many::<String>("enable").unwrap().collect();
    assert_eq!(enable, ["a", "b"]);
    let disable: Vec<&String> = m.get_many::<String>("disable").unwrap().collect();
    assert_eq!(disable, ["c"]);
    assert_eq!(
        m.get_one::<Duration>("ready-timeout"),
        Some(&Duration::from_secs(5))
    );
    assert_eq!(
        m.get_one::<Duration>("stop-timeout"),
        Some(&Duration::from_secs(90))
    );
    assert_eq!(
        m.get_one::<Duration>("shutdown-timeout"),
        Some(&Duration::from_secs(7200))
    );
    assert_eq!(operands(&m), ["api"]);
}

#[test]
fn a_bare_invocation_has_no_operands_and_no_flags() {
    let m = parse(&["serve"]);
    assert!(!m.get_flag("list"));
    assert!(operands(&m).is_empty());
    assert!(m.get_one::<Duration>("ready-timeout").is_none());
}

#[test]
fn the_help_is_the_contract_sentence() {
    let cmd = serve_command();
    assert_eq!(cmd.get_name(), "serve");
    assert_eq!(
        cmd.get_about().map(|s| s.to_string()).as_deref(),
        Some("Run configured services under one lifecycle")
    );
}

#[test]
fn a_malformed_duration_is_refused_by_the_parser_naming_the_token() {
    let err = serve_command()
        .try_get_matches_from(["serve", "--ready-timeout", "30"])
        .unwrap_err();
    let text = err.to_string();
    assert!(text.contains("missing unit"), "{text}");
    assert!(text.contains("\"30\""), "{text}");

    let err = serve_command()
        .try_get_matches_from(["serve", "--shutdown-timeout", "5d"])
        .unwrap_err();
    assert!(err.to_string().contains("unknown unit"), "{err}");
}

#[test]
fn the_help_addendum_lists_registered_names_only_when_there_are_any() {
    let with = serve_command_for(&registry(&["api", "web"]));
    let mut buf = Vec::new();
    with.clone().write_long_help(&mut buf).unwrap();
    let help = String::from_utf8(buf).unwrap();
    assert!(help.contains("Services: api, web"), "{help}");

    let without = serve_command_for(&ServiceRegistry::new());
    let mut buf = Vec::new();
    without.clone().write_long_help(&mut buf).unwrap();
    assert!(!String::from_utf8(buf).unwrap().contains("Services:"));

    // Help text only: an unregistered name still parses, because the
    // NOT_FOUND refusal belongs to the registration gate.
    with.try_get_matches_from(["serve", "nope"]).unwrap();
}

// ---------------------------------------------------------------------------
// Hierarchy: arity is owned by resolve, not by the parser
// ---------------------------------------------------------------------------

#[test]
fn two_operands_parse_and_resolve_refuses_them_at_usage_2() {
    let m = parse(&["serve", "a", "b"]);
    assert_eq!(operands(&m), ["a", "b"]);

    let out = resolve(
        &registry(&["a", "b"]),
        &ResolveRequest {
            args: operands(&m),
            ..ResolveRequest::default()
        },
    );
    let err = out.error.expect("two operands are a usage error");
    assert_eq!(err.code, CODE_USAGE);
    assert_eq!(err.exit_code, 2);
    assert!(out.selected.is_empty());
}

#[test]
fn one_operand_is_the_selector_form_and_none_is_the_supervisor_form() {
    let reg = registry(&["a", "b"]);
    let mut configs = ServiceConfigs::new();
    configs.insert("b".to_string(), ServiceConfig::enabled());

    let m = parse(&["serve", "a"]);
    let out = resolve(
        &reg,
        &ResolveRequest {
            args: operands(&m),
            configs: Some(&configs),
            policy: None,
        },
    );
    assert!(out.explicit);
    assert_eq!(out.selected, ["a"], "selector overrides enablement");

    let m = parse(&["serve"]);
    let out = resolve(
        &reg,
        &ResolveRequest {
            args: operands(&m),
            configs: Some(&configs),
            policy: None,
        },
    );
    assert!(!out.explicit);
    assert_eq!(out.selected, ["b"]);
}

// ---------------------------------------------------------------------------
// --list
// ---------------------------------------------------------------------------

#[test]
fn list_prints_registration_order_with_configured_and_enabled_state() {
    let reg = registry(&["zeta", "alpha", "mid"]);
    let mut configs = ServiceConfigs::new();
    configs.insert("alpha".to_string(), ServiceConfig::enabled());
    configs.insert("mid".to_string(), ServiceConfig::disabled());

    let mut out = Vec::new();
    run_list(&reg, Some(&configs), &mut out).unwrap();
    let text = String::from_utf8(out).unwrap();
    let lines: Vec<&str> = text.lines().collect();
    assert_eq!(lines.len(), 4, "{text}");
    assert!(lines[0].starts_with("SERVICE"), "{text}");
    assert!(lines[0].contains("CONFIGURED") && lines[0].contains("ENABLED"));
    assert!(lines[0].contains("READY"));

    let rows: Vec<Vec<&str>> = lines[1..]
        .iter()
        .map(|l| l.split_whitespace().collect())
        .collect();
    assert_eq!(rows[0], ["zeta", "false", "false", "false"]);
    assert_eq!(rows[1], ["alpha", "true", "true", "false"]);
    assert_eq!(rows[2], ["mid", "true", "false", "false"]);
}

#[test]
fn list_with_no_configs_marks_nothing_configured() {
    let mut out = Vec::new();
    run_list(&registry(&["a"]), None, &mut out).unwrap();
    let text = String::from_utf8(out).unwrap();
    assert!(text.lines().nth(1).unwrap().contains("false"), "{text}");
}

#[test]
fn the_options_stdout_sink_is_where_list_output_goes() {
    // ServeCommandOptions carries the sink so a caller can redirect
    // the listing; the sink type is what the run path writes to.
    let sink = Sink::default();
    let mut opts = ServeCommandOptions::new(registry(&["a"]));
    opts.stdout = Box::new(sink.clone());
    run_list(&opts.registry, opts.configs.as_ref(), opts.stdout.as_mut()).unwrap();
    assert!(sink.text().starts_with("SERVICE"));
    assert_eq!(opts.config, SupervisorConfig::default());
}

// ---------------------------------------------------------------------------
// --enable / --disable
// ---------------------------------------------------------------------------

#[test]
fn enable_makes_an_unconfigured_service_configured_and_enabled() {
    let reg = registry(&["a", "b"]);
    let mut configs = ServiceConfigs::new();
    let mut sup = SupervisorConfig::default();
    let m = parse(&["serve", "--enable", "b"]);

    apply_flags(&m, &mut configs, &mut sup, false).unwrap();
    let b = configs.get("b").expect("--enable implies configured");
    assert!(b.enabled);
    assert_eq!(b.ready_timeout, DEFAULT_READY_TIMEOUT);
    assert_eq!(b.stop_timeout, DEFAULT_STOP_TIMEOUT);
    assert!(!configs.contains_key("a"));

    let out = resolve(
        &reg,
        &ResolveRequest {
            args: operands(&m),
            configs: Some(&configs),
            policy: None,
        },
    );
    assert_eq!(out.selected, ["b"]);
    assert!(out.error.is_none());
}

#[test]
fn enable_on_a_disabled_service_keeps_its_budgets() {
    let mut configs = ServiceConfigs::new();
    configs.insert(
        "a".to_string(),
        ServiceConfig::disabled().with_ready_timeout(Duration::from_secs(3)),
    );
    let m = parse(&["serve", "--enable", "a"]);
    apply_flags(&m, &mut configs, &mut SupervisorConfig::default(), false).unwrap();
    assert!(configs["a"].enabled);
    assert_eq!(configs["a"].ready_timeout, Duration::from_secs(3));
}

#[test]
fn disable_clears_enablement_and_the_supervisor_form_skips_it_silently() {
    let reg = registry(&["a", "b"]);
    let mut configs = ServiceConfigs::new();
    configs.insert("a".to_string(), ServiceConfig::enabled());
    configs.insert("b".to_string(), ServiceConfig::enabled());
    let m = parse(&["serve", "--disable", "a"]);

    apply_flags(&m, &mut configs, &mut SupervisorConfig::default(), false).unwrap();
    assert!(!configs["a"].enabled);
    assert!(configs.contains_key("a"), "--disable does not unconfigure");

    let out = resolve(
        &reg,
        &ResolveRequest {
            args: operands(&m),
            configs: Some(&configs),
            policy: None,
        },
    );
    assert_eq!(out.selected, ["b"]);
    assert_eq!(out.skipped, ["a"]);
    assert!(out.error.is_none(), "skipping is not an error");
}

#[test]
fn disable_on_an_unconfigured_service_is_a_no_op() {
    let mut configs = ServiceConfigs::new();
    let m = parse(&["serve", "--disable", "ghost"]);
    apply_flags(&m, &mut configs, &mut SupervisorConfig::default(), false).unwrap();
    assert!(configs.is_empty());
}

#[test]
fn enable_wins_over_disable_for_the_same_name() {
    let mut configs = ServiceConfigs::new();
    let m = parse(&["serve", "--disable", "a", "--enable", "a"]);
    apply_flags(&m, &mut configs, &mut SupervisorConfig::default(), false).unwrap();
    assert!(configs["a"].enabled);

    let mut configs = ServiceConfigs::new();
    configs.insert("a".to_string(), ServiceConfig::enabled());
    let m = parse(&["serve", "--enable", "a", "--disable", "a"]);
    apply_flags(&m, &mut configs, &mut SupervisorConfig::default(), false).unwrap();
    assert!(
        configs["a"].enabled,
        "order on the command line is irrelevant"
    );
}

#[test]
fn enable_or_disable_under_the_selector_form_is_refused_at_usage_2() {
    for argv in [
        ["serve", "api", "--enable", "api"],
        ["serve", "api", "--disable", "other"],
    ] {
        let m = parse(&argv);
        let mut configs = ServiceConfigs::new();
        let err = apply_flags(
            &m,
            &mut configs,
            &mut SupervisorConfig::default(),
            operands(&m).len() == 1,
        )
        .unwrap_err();
        assert_eq!(err.code, CODE_USAGE, "{argv:?}");
        assert_eq!(err.exit_code, 2, "{argv:?}");
        assert_eq!(err.message, SELECTOR_FLAGS_REFUSAL, "{argv:?}");
        assert!(configs.is_empty(), "a refused invocation mutates nothing");
    }
}

#[test]
fn the_selector_form_without_the_flags_passes() {
    let m = parse(&["serve", "api", "--ready-timeout", "1s"]);
    let mut configs = ServiceConfigs::new();
    configs.insert("api".to_string(), ServiceConfig::disabled());
    apply_flags(&m, &mut configs, &mut SupervisorConfig::default(), true).unwrap();
    assert_eq!(configs["api"].ready_timeout, Duration::from_secs(1));
}

// ---------------------------------------------------------------------------
// Timeout flags
// ---------------------------------------------------------------------------

#[test]
fn ready_and_stop_timeouts_land_on_every_resolved_service() {
    let mut configs = ServiceConfigs::new();
    configs.insert("a".to_string(), ServiceConfig::enabled());
    configs.insert(
        "b".to_string(),
        ServiceConfig::disabled().with_stop_timeout(Duration::from_secs(9)),
    );
    let mut sup = SupervisorConfig::default();
    let m = parse(&["serve", "--ready-timeout", "2s", "--stop-timeout", "3s"]);

    apply_flags(&m, &mut configs, &mut sup, false).unwrap();
    for name in ["a", "b"] {
        assert_eq!(
            configs[name].ready_timeout,
            Duration::from_secs(2),
            "{name}"
        );
        assert_eq!(configs[name].stop_timeout, Duration::from_secs(3), "{name}");
    }
    assert!(!configs["b"].enabled, "a budget flag does not enable");
    assert_eq!(sup.shutdown_timeout, DEFAULT_SHUTDOWN_TIMEOUT);
}

#[test]
fn shutdown_timeout_lands_on_the_supervisor_only() {
    let mut configs = ServiceConfigs::new();
    configs.insert("a".to_string(), ServiceConfig::enabled());
    let mut sup = SupervisorConfig::default();
    let m = parse(&["serve", "--shutdown-timeout", "4s"]);

    apply_flags(&m, &mut configs, &mut sup, false).unwrap();
    assert_eq!(sup.shutdown_timeout, Duration::from_secs(4));
    assert_eq!(configs["a"].ready_timeout, DEFAULT_READY_TIMEOUT);
    assert_eq!(configs["a"].stop_timeout, DEFAULT_STOP_TIMEOUT);
}

#[test]
fn only_one_budget_flag_leaves_the_other_alone() {
    let mut configs = ServiceConfigs::new();
    configs.insert(
        "a".to_string(),
        ServiceConfig::enabled().with_stop_timeout(Duration::from_secs(7)),
    );
    let m = parse(&["serve", "--ready-timeout", "2s"]);
    apply_flags(&m, &mut configs, &mut SupervisorConfig::default(), false).unwrap();
    assert_eq!(configs["a"].ready_timeout, Duration::from_secs(2));
    assert_eq!(configs["a"].stop_timeout, Duration::from_secs(7));
}

#[test]
fn zero_and_absent_timeouts_leave_configured_values_alone() {
    let mut configs = ServiceConfigs::new();
    configs.insert(
        "a".to_string(),
        ServiceConfig::enabled()
            .with_ready_timeout(Duration::from_secs(5))
            .with_stop_timeout(Duration::from_secs(6)),
    );
    let mut sup = SupervisorConfig {
        shutdown_timeout: Duration::from_secs(7),
        ..SupervisorConfig::default()
    };
    let m = parse(&[
        "serve",
        "--ready-timeout",
        "0s",
        "--stop-timeout",
        "0ms",
        "--shutdown-timeout",
        "0h",
    ]);
    apply_flags(&m, &mut configs, &mut sup, false).unwrap();
    assert_eq!(configs["a"].ready_timeout, Duration::from_secs(5));
    assert_eq!(configs["a"].stop_timeout, Duration::from_secs(6));
    assert_eq!(sup.shutdown_timeout, Duration::from_secs(7));

    let m = parse(&["serve"]);
    apply_flags(&m, &mut configs, &mut sup, false).unwrap();
    assert_eq!(configs["a"].ready_timeout, Duration::from_secs(5));
    assert_eq!(sup.shutdown_timeout, Duration::from_secs(7));
}

// ---------------------------------------------------------------------------
// Mounting: exactly one owner of the word
// ---------------------------------------------------------------------------

fn serve_count(root: &Command) -> usize {
    root.get_subcommands()
        .filter(|c| c.get_name() == "serve")
        .count()
}

#[test]
fn mount_adds_serve_beside_the_roots_own_commands() {
    let root = root_with_serve();
    assert_eq!(serve_count(&root), 1);
    assert!(root.find_subcommand("other").is_some());

    let m = root
        .try_get_matches_from(["tool", "serve", "--list"])
        .unwrap();
    let (name, sub) = m.subcommand().unwrap();
    assert_eq!(name, "serve");
    assert!(sub.get_flag("list"));
}

#[test]
fn mount_replaces_a_serve_the_root_already_owned() {
    let root = Command::new("tool")
        .subcommand(Command::new("serve").about("a leaf from elsewhere"))
        .subcommand(Command::new("other"));
    let root = mount(root);
    assert_eq!(serve_count(&root), 1, "never two owners of the word");
    let serve = root.find_subcommand("serve").unwrap();
    assert_eq!(
        serve.get_about().map(|s| s.to_string()).as_deref(),
        Some("Run configured services under one lifecycle"),
        "the kit-owned command wins"
    );
    assert!(serve.get_arguments().any(|a| a.get_id() == "list"));
    assert!(root.find_subcommand("other").is_some());
}

#[test]
fn mounting_twice_still_leaves_one() {
    let root = mount(mount(Command::new("tool")));
    assert_eq!(serve_count(&root), 1);
}

#[test]
fn mount_for_carries_the_help_addendum() {
    let root = mount_for(Command::new("tool"), &registry(&["api"]));
    let serve = root.find_subcommand("serve").unwrap();
    assert!(serve
        .get_after_help()
        .map(|s| s.to_string())
        .unwrap()
        .contains("api"));
}

// ---------------------------------------------------------------------------
// The run path: matches in, exit code out
// ---------------------------------------------------------------------------

/// A `#[tokio::test]` has no per-test timeout; a defect that never
/// drains would hang the suite rather than fail one test.
async fn bounded(fut: impl std::future::Future<Output = RunResult>) -> RunResult {
    match tokio::time::timeout(Duration::from_secs(20), fut).await {
        Ok(res) => res,
        Err(_) => panic!("run did not finish within its bound"),
    }
}

fn opts_with(reg: ServiceRegistry, configs: ServiceConfigs) -> ServeCommandOptions {
    let mut opts = ServeCommandOptions::new(reg);
    opts.configs = Some(configs);
    opts
}

fn enabled(names: &[&str]) -> ServiceConfigs {
    names
        .iter()
        .map(|n| (n.to_string(), ServiceConfig::enabled()))
        .collect()
}

#[tokio::test]
async fn run_refuses_two_operands_at_usage_2_without_starting() {
    let m = parse(&["serve", "a", "b"]);
    let res = bounded(run(
        &m,
        opts_with(registry(&["a", "b"]), enabled(&["a", "b"])),
    ))
    .await;
    assert_eq!(res.exit_code, 2);
    assert_eq!(res.outcome, LifecycleOutcome::InvalidSelection);
    assert_eq!(res.error.as_ref().unwrap().code, CODE_USAGE);
    assert!(res.started.is_empty());
}

#[tokio::test]
async fn run_refuses_an_unknown_service_at_not_found_3() {
    let m = parse(&["serve", "nope"]);
    let res = bounded(run(&m, opts_with(registry(&["api"]), enabled(&["api"])))).await;
    assert_eq!(res.exit_code, 3);
    assert_eq!(res.outcome, LifecycleOutcome::UnknownService);
    assert_eq!(res.error.as_ref().unwrap().code, CODE_NOT_FOUND);
}

#[tokio::test]
async fn run_refuses_a_policy_denied_service_at_unauthorized_5() {
    let mut reg = ServiceRegistry::new();
    reg.register(Arc::new(Classed("api"))).unwrap();
    let m = parse(&["serve", "api"]);
    let mut opts = opts_with(reg, ServiceConfigs::new());
    opts.policy = Some(Arc::new(DenyAll));
    let res = bounded(run(&m, opts)).await;
    assert_eq!(res.exit_code, 5);
    assert_eq!(res.outcome, LifecycleOutcome::PolicyDenied);
    assert_eq!(res.error.as_ref().unwrap().code, CODE_UNAUTHORIZED);
}

#[tokio::test]
async fn run_refuses_a_supervisor_form_resolving_to_nothing_at_usage_2() {
    let m = parse(&["serve"]);
    let res = bounded(run(
        &m,
        opts_with(registry(&["api"]), ServiceConfigs::new()),
    ))
    .await;
    assert_eq!(res.exit_code, 2);
    assert_eq!(res.outcome, LifecycleOutcome::NoServices);
}

#[tokio::test]
async fn run_list_short_circuits_before_resolution() {
    // Nothing configured: a bare `serve` is USAGE/2, but `--list` is
    // an inspection and exits 0 with the table.
    let sink = Sink::default();
    let m = parse(&["serve", "--list"]);
    let mut opts = opts_with(registry(&["api"]), ServiceConfigs::new());
    opts.stdout = Box::new(sink.clone());
    let res = bounded(run(&m, opts)).await;
    assert_eq!(res.exit_code, 0);
    assert_eq!(res.outcome, LifecycleOutcome::CleanStop);
    assert!(res.error.is_none());
    assert!(res.started.is_empty(), "--list starts nothing");
    let text = sink.text();
    assert!(text.starts_with("SERVICE"), "{text}");
    assert!(text.contains("api"), "{text}");
}

#[tokio::test]
async fn run_refuses_enable_under_the_selector_form_before_resolving() {
    // `nope` is unregistered; the flag refusal (USAGE/2) must come
    // before the registration gate (NOT_FOUND/3).
    let m = parse(&["serve", "nope", "--enable", "api"]);
    let res = bounded(run(&m, opts_with(registry(&["api"]), enabled(&["api"])))).await;
    assert_eq!(res.exit_code, 2);
    assert_eq!(res.outcome, LifecycleOutcome::InvalidSelection);
    assert_eq!(res.error.as_ref().unwrap().message, SELECTOR_FLAGS_REFUSAL);
}

#[tokio::test]
async fn run_starts_the_resolved_set_and_drains_to_exit_0_on_shutdown() {
    let m = parse(&["serve", "--disable", "a"]);
    let shutdown = CancelToken::new();
    let mut opts = opts_with(registry(&["a", "b"]), enabled(&["a", "b"]));
    opts.shutdown = Some(shutdown.clone());

    let trigger = shutdown.clone();
    tokio::spawn(async move {
        tokio::time::sleep(Duration::from_millis(150)).await;
        trigger.cancel();
    });

    let res = bounded(run(&m, opts)).await;
    assert_eq!(res.exit_code, 0, "{:?}", res.error);
    assert_eq!(res.outcome, LifecycleOutcome::CleanStop);
    assert_eq!(
        res.started,
        ["b"],
        "the disabled service is skipped silently"
    );
    assert_eq!(res.ready, ["b"]);
    assert!(res.failed.is_empty());
}

#[tokio::test]
async fn run_enables_an_unconfigured_service_for_the_supervisor_form() {
    let m = parse(&["serve", "--enable", "b", "--shutdown-timeout", "5s"]);
    let shutdown = CancelToken::new();
    let mut opts = opts_with(registry(&["a", "b"]), ServiceConfigs::new());
    opts.shutdown = Some(shutdown.clone());
    tokio::spawn(async move {
        tokio::time::sleep(Duration::from_millis(150)).await;
        shutdown.cancel();
    });
    let res = bounded(run(&m, opts)).await;
    assert_eq!(res.exit_code, 0, "{:?}", res.error);
    assert_eq!(res.started, ["b"]);
}

#[tokio::test]
async fn run_maps_a_start_failure_onto_generic_1() {
    struct Broken;
    impl Service for Broken {
        fn name(&self) -> &str {
            "broken"
        }
        fn start<'a>(
            &'a self,
            _: CancelToken,
            _: ReadySignal,
        ) -> ServeFuture<'a, Result<(), String>> {
            Box::pin(async { Err("bind: address in use".to_string()) })
        }
        fn ready(&self) -> bool {
            false
        }
        fn stop<'a>(&'a self, _: CancelToken) -> ServeFuture<'a, Result<(), String>> {
            Box::pin(async { Ok(()) })
        }
    }
    let mut reg = ServiceRegistry::new();
    reg.register(Arc::new(Broken)).unwrap();
    let m = parse(&["serve", "broken"]);
    let res = bounded(run(&m, opts_with(reg, ServiceConfigs::new()))).await;
    assert_eq!(res.exit_code, 1);
    assert_eq!(res.outcome, LifecycleOutcome::StartFailed);
    assert!(res.failed.contains_key("broken"));
}

#[test]
fn failure_policy_strings_parse_or_refuse_with_the_fix_spelled_out() {
    assert_eq!(parse_failure_policy("").unwrap(), FailurePolicy::FailFast);
    assert_eq!(
        parse_failure_policy("fail-fast").unwrap(),
        FailurePolicy::FailFast
    );
    assert_eq!(
        parse_failure_policy("isolate").unwrap(),
        FailurePolicy::Isolate
    );

    let err = parse_failure_policy("retry").unwrap_err();
    assert_eq!(err.code, CODE_USAGE);
    assert_eq!(err.exit_code, 2);
    assert!(err.message.contains("\"retry\""), "{}", err.message);
    assert_eq!(err.suggested_fix, "use \"fail-fast\" or \"isolate\"");
}

// ---------------------------------------------------------------------------
// Serving in a real process
// ---------------------------------------------------------------------------

#[cfg(unix)]
mod real_process {
    use std::path::PathBuf;
    use std::process::Stdio;
    use std::time::Duration;

    use tokio::io::{AsyncBufReadExt, BufReader};
    use tokio::process::Command;
    use tokio::sync::OnceCell;

    static EXAMPLE: OnceCell<PathBuf> = OnceCell::const_new();

    /// Builds `examples/serve.rs` once per test binary. Cargo sets no
    /// `CARGO_BIN_EXE_*` for examples, so the test builds it itself,
    /// bounded so a wedged build fails these tests rather than CI.
    async fn example() -> PathBuf {
        EXAMPLE
            .get_or_init(|| async {
                let cargo = std::env::var("CARGO").unwrap_or_else(|_| "cargo".to_string());
                let manifest = env!("CARGO_MANIFEST_DIR");
                let status = tokio::time::timeout(
                    Duration::from_secs(600),
                    Command::new(cargo)
                        .current_dir(manifest)
                        .args(["build", "--example", "serve", "--features", "serve-cli"])
                        .stdout(Stdio::null())
                        .stderr(Stdio::inherit())
                        .status(),
                )
                .await
                .expect("building the serve example did not finish within its bound")
                .expect("spawn cargo");
                assert!(status.success(), "cargo build --example serve: {status}");

                let mut path = PathBuf::from(
                    std::env::var("CARGO_TARGET_DIR")
                        .unwrap_or_else(|_| format!("{manifest}/target")),
                );
                path.push("debug");
                path.push("examples");
                path.push("serve");
                assert!(path.exists(), "{}", path.display());
                path
            })
            .await
            .clone()
    }

    async fn output_of(args: &[&str]) -> (i32, String, String) {
        let out = tokio::time::timeout(
            Duration::from_secs(20),
            Command::new(example().await).args(args).output(),
        )
        .await
        .expect("the example did not exit within its bound")
        .expect("spawn example");
        (
            out.status.code().unwrap_or(-1),
            String::from_utf8_lossy(&out.stdout).to_string(),
            String::from_utf8_lossy(&out.stderr).to_string(),
        )
    }

    #[tokio::test]
    async fn the_example_binds_reports_ready_and_stops_cleanly_on_sigint() {
        let mut child = Command::new(example().await)
            .arg("serve")
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
            .expect("spawn example");
        let pid = child.id().expect("child pid");
        let mut lines = BufReader::new(child.stderr.take().unwrap()).lines();

        // Wait for readiness, and read the address out of the event
        // rather than trusting the log: the contract puts it in the
        // payload, and a real port must actually be listening there.
        let ready_line = tokio::time::timeout(Duration::from_secs(20), async {
            while let Ok(Some(line)) = lines.next_line().await {
                if line.contains("kit.serve.service.ready_reported") {
                    return line;
                }
            }
            panic!("stderr ended before ready_reported")
        })
        .await
        .expect("the service did not report ready within its bound");
        let addr = ready_line
            .split("\"address\":\"")
            .nth(1)
            .and_then(|rest| rest.split('"').next())
            .unwrap_or_else(|| panic!("no address in {ready_line}"))
            .to_string();
        std::net::TcpStream::connect(&addr).unwrap_or_else(|e| panic!("connect {addr}: {e}"));

        let killed = Command::new("kill")
            .args(["-INT", &pid.to_string()])
            .status()
            .await
            .expect("kill");
        assert!(killed.success());

        let rest = tokio::time::timeout(Duration::from_secs(20), async {
            let mut acc = String::new();
            while let Ok(Some(line)) = lines.next_line().await {
                acc.push_str(&line);
                acc.push('\n');
            }
            acc
        })
        .await
        .expect("stderr did not close within its bound");
        let status = tokio::time::timeout(Duration::from_secs(20), child.wait())
            .await
            .expect("the example did not exit within its bound")
            .expect("wait");

        assert_eq!(status.code(), Some(0), "a signal stop exits 0:\n{rest}");
        assert!(
            rest.contains("kit.serve.supervisor.stopped"),
            "no supervisor stopped event:\n{rest}"
        );
        assert!(
            rest.contains("\"reason\":\"clean-stop\""),
            "stopped reason:\n{rest}"
        );
        assert!(
            rest.contains("kit.serve.service.stopped"),
            "no service stopped event:\n{rest}"
        );
        assert!(
            std::net::TcpStream::connect(&addr).is_err(),
            "the listener outlived the process"
        );
    }

    #[tokio::test]
    async fn the_example_lists_and_refuses_with_the_contract_exit_codes() {
        let (code, stdout, _) = output_of(&["serve", "--list"]).await;
        assert_eq!(code, 0);
        assert!(stdout.starts_with("SERVICE"), "{stdout}");
        assert!(stdout.contains("echo"), "{stdout}");

        let (code, _, stderr) = output_of(&["serve", "a", "b"]).await;
        assert_eq!(code, 2, "{stderr}");
        assert!(stderr.contains("USAGE"), "{stderr}");

        let (code, _, stderr) = output_of(&["serve", "nope"]).await;
        assert_eq!(code, 3, "{stderr}");
        assert!(stderr.contains("NOT_FOUND"), "{stderr}");

        let (code, _, stderr) = output_of(&["serve", "echo", "--enable", "echo"]).await;
        assert_eq!(code, 2, "{stderr}");
        assert!(stderr.contains("supervisor form"), "{stderr}");

        let (code, _, stderr) = output_of(&["serve", "--ready-timeout", "30"]).await;
        assert_eq!(code, 2, "clap's own usage error is also 2:\n{stderr}");
        assert!(stderr.contains("missing unit"), "{stderr}");
    }
}
