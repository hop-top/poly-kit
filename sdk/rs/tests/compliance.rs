//! The 12-factor compliance check over a toolspec (Go
//! `core/compliance` `static.go`, `compliance.go`, `format.go`; the
//! TypeScript and Python `compliance` modules). Static pass only: the
//! runtime pass executes a binary and is not ported.

#![cfg(feature = "compliance")]

use hop_top_kit::compliance::{
    format_report, load_spec, run_static, CheckResult, Factor, Report, Status,
};

fn report(yaml: &str) -> Report {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("spec.yaml");
    std::fs::write(&path, yaml).unwrap();
    run_static(&path).unwrap()
}

fn status(r: &Report, f: Factor) -> Status {
    r.results.iter().find(|c| c.factor == f).unwrap().status
}

fn result(r: &Report, f: Factor) -> &CheckResult {
    r.results.iter().find(|c| c.factor == f).unwrap()
}

const FULL: &str = r#"
name: probe
schema_version: "1"
state_introspection:
  config_commands: ["config show"]
commands:
  - name: list
    contract: {idempotent: true}
    output_schema: {format: json}
  - name: apply
    contract: {idempotent: false, side_effects: ["writes files"]}
    preview_modes: ["--dry-run"]
"#;

#[test]
fn a_complete_spec_passes_every_static_factor() {
    let r = report(FULL);
    assert_eq!(r.total, 12);
    // Runtime-only factors 3, 9, 10 skip; 12 skips without auth_commands.
    for (f, want) in [
        (Factor::SelfDescribing, Status::Pass),
        (Factor::StructuredIo, Status::Pass),
        (Factor::StreamDiscipline, Status::Skip),
        (Factor::ContractsErrors, Status::Pass),
        (Factor::Preview, Status::Pass),
        (Factor::Idempotency, Status::Pass),
        (Factor::StateTransparency, Status::Pass),
        (Factor::SafeDelegation, Status::Pass),
        (Factor::ObservableOps, Status::Skip),
        (Factor::Provenance, Status::Skip),
        (Factor::Evolution, Status::Pass),
        (Factor::AuthLifecycle, Status::Skip),
    ] {
        assert_eq!(status(&r, f), want, "{f:?}");
    }
    assert_eq!(r.score, 8);
    // F13 skips on a spec that does not opt in, and is out of the total.
    assert_eq!(status(&r, Factor::ConsentingTelemetry), Status::Skip);
}

#[test]
fn an_empty_spec_fails_what_it_can() {
    let r = report("name: x\n");
    assert_eq!(status(&r, Factor::SelfDescribing), Status::Fail);
    assert_eq!(
        result(&r, Factor::SelfDescribing).details,
        "no commands defined"
    );
    assert_eq!(status(&r, Factor::StructuredIo), Status::Fail);
    assert_eq!(status(&r, Factor::ContractsErrors), Status::Fail);
    assert_eq!(status(&r, Factor::Idempotency), Status::Fail);
    assert_eq!(status(&r, Factor::StateTransparency), Status::Fail);
    assert_eq!(status(&r, Factor::Evolution), Status::Fail);
    // Nothing mutating, nothing dangerous.
    assert_eq!(status(&r, Factor::Preview), Status::Pass);
    assert_eq!(status(&r, Factor::SafeDelegation), Status::Pass);
    assert_eq!(r.score, 2);
}

#[test]
fn an_unnamed_command_fails_self_description() {
    let r = report("commands:\n  - children: []\n");
    assert_eq!(
        result(&r, Factor::SelfDescribing).details,
        "command missing name"
    );
}

#[test]
fn nested_commands_count() {
    let r = report(
        r#"
commands:
  - name: group
    children:
      - name: deep
        output_schema: {format: json}
        contract: {idempotent: true, side_effects: [net]}
        preview_modes: [--dry-run]
"#,
    );
    assert_eq!(status(&r, Factor::StructuredIo), Status::Pass);
    assert_eq!(
        result(&r, Factor::StructuredIo).details,
        "output_schema found on deep"
    );
    assert_eq!(status(&r, Factor::Preview), Status::Pass);
    assert_eq!(status(&r, Factor::Idempotency), Status::Pass);
}

#[test]
fn contracts_need_every_mutating_command_covered() {
    // Contracts declared on a non-mutating command only.
    let r = report("commands:\n  - name: a\n    contract: {idempotent: true}\n");
    assert_eq!(status(&r, Factor::ContractsErrors), Status::Pass);
    assert_eq!(
        result(&r, Factor::ContractsErrors).details,
        "contracts found"
    );
    let r = report(
        "commands:\n  - name: a\n    contract: {side_effects: [x]}\n  - name: b\n    contract: {side_effects: [y]}\n",
    );
    assert_eq!(
        result(&r, Factor::ContractsErrors).details,
        "all mutating commands have contracts"
    );
}

#[test]
fn preview_reports_partial_coverage_as_a_pass() {
    let r = report(
        r#"
commands:
  - name: a
    contract: {side_effects: [x]}
    preview_modes: [--dry-run]
  - name: b
    contract: {side_effects: [y]}
"#,
    );
    let p = result(&r, Factor::Preview);
    assert_eq!(p.status, Status::Pass);
    assert_eq!(p.details, "1/2 mutating commands have preview_modes");
    let r = report("commands:\n  - name: a\n    contract: {side_effects: [x]}\n");
    let p = result(&r, Factor::Preview);
    assert_eq!(p.status, Status::Fail);
    assert_eq!(p.details, "no mutating command has preview_modes");
    assert!(p.suggestion.contains("--dry-run"));
}

#[test]
fn idempotency_needs_an_explicit_declaration() {
    let r = report("commands:\n  - name: a\n    contract: {side_effects: [x]}\n");
    assert_eq!(status(&r, Factor::Idempotency), Status::Fail);
    let r = report("commands:\n  - name: a\n    contract: {idempotent: false}\n");
    assert_eq!(status(&r, Factor::Idempotency), Status::Pass);
}

#[test]
fn dangerous_commands_need_a_safety_block() {
    let r = report(
        "commands:\n  - name: rm\n    safety: {level: dangerous, requires_confirmation: true}\n",
    );
    assert_eq!(status(&r, Factor::SafeDelegation), Status::Pass);
    assert_eq!(
        result(&r, Factor::SafeDelegation).details,
        "all dangerous commands have safety metadata"
    );
    let r = report("commands:\n  - name: a\n    safety: {level: safe}\n");
    assert_eq!(
        result(&r, Factor::SafeDelegation).details,
        "no dangerous commands"
    );
}

#[test]
fn evolution_names_the_schema_version() {
    let r = report("schema_version: \"2\"\ncommands: [{name: a}]\n");
    assert_eq!(result(&r, Factor::Evolution).details, "schema_version: 2");
    // An unquoted number is the same string, as YAML-to-string decoders do.
    let r = report("schema_version: 2\ncommands: [{name: a}]\n");
    assert_eq!(result(&r, Factor::Evolution).details, "schema_version: 2");
}

#[test]
fn auth_lifecycle_passes_when_declared() {
    let r = report("state_introspection:\n  auth_commands: [login]\n");
    assert_eq!(status(&r, Factor::AuthLifecycle), Status::Pass);
}

// --- F13 ---

const TELEMETRY: &str = r#"
commands:
  - name: telemetry
    children:
      - {name: status}
      - {name: enable}
      - {name: disable}
      - {name: reset}
      - {name: inspect}
telemetry:
  enabled: true
  categories: [invocation]
  sinks: [bus]
  consent_command: "probe telemetry"
  consent_subcommands: [disable, enable, inspect, reset, status]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
"#;

#[test]
fn an_opt_in_spec_widens_the_denominator_and_passes_f13() {
    let r = report(TELEMETRY);
    assert_eq!(r.total, 13);
    let f = result(&r, Factor::ConsentingTelemetry);
    assert_eq!(f.status, Status::Pass, "{f:?}");
    assert_eq!(
        f.details,
        "telemetry block well-formed; all consent subcommands declared"
    );
}

#[test]
fn f13_aggregates_every_broken_sub_condition() {
    let broken = TELEMETRY
        .replace("categories: [invocation]", "categories: []")
        .replace(
            "consent_subcommands: [disable, enable, inspect, reset, status]",
            "consent_subcommands: [status, bogus]",
        )
        .replace("[DO_NOT_TRACK, PROBE_TELEMETRY_MODE]", "[PROBE_MODE]")
        .replace("prompt_version: \"v1\"", "consent_version: \"v1\"")
        .replace("redact_rules: kit-default", "redact_rules: \"  \"");
    let r = report(&broken);
    let f = result(&r, Factor::ConsentingTelemetry);
    assert_eq!(f.status, Status::Fail);
    for want in [
        "telemetry.categories is empty",
        "telemetry.consent_subcommands missing required entries: disable, enable, inspect, reset",
        "telemetry.kill_switch_envs missing DO_NOT_TRACK",
        "telemetry.kill_switch_envs missing a <APP>_TELEMETRY_MODE entry",
        "telemetry.prompt_version is empty",
        "telemetry.redact_rules is empty",
        "telemetry.consent_subcommands declared but not in commands tree: telemetry bogus",
    ] {
        assert!(f.details.contains(want), "{want:?} not in {:?}", f.details);
    }
    assert!(f.suggestion.contains("DO_NOT_TRACK"));
    // Failures are one row joined by "; ".
    assert_eq!(
        f.details.matches("; telemetry.").count(),
        6,
        "{}",
        f.details
    );
}

#[test]
fn the_mode_env_shape_is_app_prefixed_upper_snake() {
    for (env, ok) in [
        ("KIT_TELEMETRY_MODE", true),
        ("SPACED_TELEMETRY_MODE", true),
        ("A1_B_TELEMETRY_MODE", true),
        ("_TELEMETRY_MODE", false),
        ("1A_TELEMETRY_MODE", false),
        ("spaced_telemetry_mode", false),
        ("TELEMETRY_MODE", false),
    ] {
        let spec = TELEMETRY.replace("PROBE_TELEMETRY_MODE", env);
        let f = report(&spec);
        let st = status(&f, Factor::ConsentingTelemetry);
        assert_eq!(st == Status::Pass, ok, "{env}");
    }
}

#[test]
fn a_single_token_consent_command_defaults_to_the_telemetry_group() {
    let spec = TELEMETRY.replace(
        "consent_command: \"probe telemetry\"",
        "consent_command: probe",
    );
    assert_eq!(
        status(&report(&spec), Factor::ConsentingTelemetry),
        Status::Pass
    );
    let nested = TELEMETRY.replace(
        "consent_command: \"probe telemetry\"",
        "consent_command: \"probe privacy telemetry\"",
    );
    let r = report(&nested);
    assert_eq!(status(&r, Factor::ConsentingTelemetry), Status::Fail);
    assert!(result(&r, Factor::ConsentingTelemetry)
        .details
        .contains("privacy telemetry status"));
}

#[test]
fn enabled_false_is_not_an_opt_in() {
    let spec = TELEMETRY.replace("enabled: true", "enabled: false");
    let r = report(&spec);
    assert_eq!(r.total, 12);
    assert_eq!(status(&r, Factor::ConsentingTelemetry), Status::Skip);
}

// --- loading and formatting ---

#[test]
fn load_errors_name_the_step() {
    let e = load_spec(std::path::Path::new("/nonexistent/spec.yaml")).unwrap_err();
    assert!(e.to_string().starts_with("read toolspec: "), "{e}");
    let dir = tempfile::tempdir().unwrap();
    let p = dir.path().join("bad.yaml");
    std::fs::write(&p, "commands: [unterminated\n").unwrap();
    let e = load_spec(&p).unwrap_err();
    assert!(e.to_string().starts_with("parse toolspec: "), "{e}");
}

#[test]
fn the_loader_reads_the_whole_toolspec_shape() {
    let dir = tempfile::tempdir().unwrap();
    let p = dir.path().join("full.yaml");
    std::fs::write(
        &p,
        r#"
name: t
schema_version: "1"
commands:
  - name: deploy
    short: Ship it
    aliases: [d]
    flags:
      - {name: env, short: e, type: string, description: Target, required: true, default: prod, choices: [prod, dev]}
    args:
      - {name: target, required: true}
    exit_codes: [USAGE]
    safety: {level: dangerous, requires_confirmation: true, permissions: [net]}
    contract: {idempotent: true, side_effects: [net], retryable: true, pre_conditions: [auth]}
    deprecated: true
    replaced_by: ship
error_patterns:
  - {pattern: "no such", fix: "run init", cause: missing}
workflows:
  - {name: release, steps: [build, deploy]}
"#,
    )
    .unwrap();
    let s = load_spec(&p).unwrap();
    let c = &s.commands[0];
    assert_eq!(c.short, "Ship it");
    assert_eq!(c.flags[0].choices, ["prod", "dev"]);
    assert!(c.flags[0].required);
    assert_eq!(c.args[0].name, "target");
    assert_eq!(c.safety.as_ref().unwrap().level, "dangerous");
    assert_eq!(c.contract.as_ref().unwrap().idempotent, Some(true));
    assert_eq!(s.error_patterns[0].fix, "run init");
    assert_eq!(s.workflows[0].steps, ["build", "deploy"]);
}

#[test]
fn text_report_matches_go_layout() {
    let r = report(FULL);
    let text = format_report(&r, "text");
    assert!(
        text.starts_with("\n  12-Factor AI CLI Compliance Report\n"),
        "{text}"
    );
    assert!(text.contains("  Toolspec : "), "{text}");
    assert!(
        text.contains("  PASS  F1  Self-Describing        commands array non-empty, all named\n"),
        "{text}"
    );
    assert!(
        text.contains("  SKIP  F3  Stream Discipline      runtime check only\n"),
        "{text}"
    );
    assert!(
        text.ends_with("  Score: 8/12 factors passing\n\n"),
        "{text}"
    );
    let bad = report("name: x\n");
    assert!(format_report(&bad, "text")
        .contains("       └─ Add a commands array with at least one named command\n"));
}

#[test]
fn json_report_matches_go_shape() {
    let r = report(FULL);
    let v: serde_json::Value = serde_json::from_str(&format_report(&r, "json")).unwrap();
    assert_eq!(v["score"], 8);
    assert_eq!(v["total"], 12);
    assert_eq!(v["results"][0]["factor"], 1);
    assert_eq!(v["results"][0]["name"], "Self-Describing");
    assert_eq!(v["results"][0]["status"], "pass");
    // Empty suggestion stays off the wire.
    assert!(v["results"][0].get("suggestion").is_none());
    assert_eq!(v["binary"], "");
}

#[test]
fn factor_names_are_the_contract_spelling() {
    let names: Vec<_> = (1..=13u8)
        .map(|n| Factor::from_number(n).unwrap().name())
        .collect();
    assert_eq!(names[0], "Self-Describing");
    assert_eq!(names[1], "Structured I/O");
    assert_eq!(names[3], "Contracts & Errors");
    assert_eq!(names[12], "Consenting Telemetry");
    assert!(Factor::from_number(14).is_none());
}
