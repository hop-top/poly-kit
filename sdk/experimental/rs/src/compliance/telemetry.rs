//! Factor 13, Consenting Telemetry: the static check of the toolspec's
//! `telemetry:` block (Go `checkConsentingTelemetry`). A spec that does
//! not opt in skips it and keeps the denominator at 12.

use super::checks::{fail, pass, skip};
use super::spec::{Command, ToolSpec};
use super::{CheckResult, Factor};

/// The subcommands an opt-in tool must expose under its consent command.
const CANONICAL_SUBCOMMANDS: [&str; 5] = ["disable", "enable", "inspect", "reset", "status"];

pub(super) fn opted_in(spec: &ToolSpec) -> bool {
    spec.telemetry.as_ref().is_some_and(|t| t.enabled)
}

/// `^[A-Z][A-Z0-9_]*_TELEMETRY_MODE$`: `KIT_TELEMETRY_MODE` matches by
/// construction, as does an app-prefixed form like `SPACED_TELEMETRY_MODE`.
fn is_mode_env(e: &str) -> bool {
    let Some(prefix) = e.strip_suffix("_TELEMETRY_MODE") else {
        return false;
    };
    let mut chars = e.chars();
    chars.next().is_some_and(|c| c.is_ascii_uppercase())
        && !prefix.is_empty()
        && e.chars()
            .all(|c| c.is_ascii_uppercase() || c.is_ascii_digit() || c == '_')
}

fn path_exists(cmds: &[Command], path: &[String]) -> bool {
    let Some((head, rest)) = path.split_first() else {
        return false;
    };
    match cmds.iter().find(|c| &c.name == head) {
        Some(_) if rest.is_empty() => true,
        Some(c) => path_exists(&c.children, rest),
        None => false,
    }
}

pub(super) fn check(spec: &ToolSpec) -> CheckResult {
    let f = Factor::ConsentingTelemetry;
    let Some(t) = spec.telemetry.as_ref().filter(|t| t.enabled) else {
        return skip(f, "binary does not opt into telemetry");
    };
    let mut failures: Vec<String> = Vec::new();

    if t.categories.is_empty() {
        failures.push("telemetry.categories is empty".into());
    }
    let missing: Vec<&str> = CANONICAL_SUBCOMMANDS
        .iter()
        .copied()
        .filter(|req| !t.consent_subcommands.iter().any(|s| s == req))
        .collect();
    if !missing.is_empty() {
        failures.push(format!(
            "telemetry.consent_subcommands missing required entries: {}",
            missing.join(", ")
        ));
    }
    if !t.kill_switch_envs.iter().any(|e| e == "DO_NOT_TRACK") {
        failures.push("telemetry.kill_switch_envs missing DO_NOT_TRACK".into());
    }
    if !t.kill_switch_envs.iter().any(|e| is_mode_env(e)) {
        failures.push(
            "telemetry.kill_switch_envs missing a <APP>_TELEMETRY_MODE entry \
             (e.g. KIT_TELEMETRY_MODE or SPACED_TELEMETRY_MODE)"
                .into(),
        );
    }
    if t.prompt_version.trim().is_empty() {
        failures.push(
            "telemetry.prompt_version is empty (canonical field name is \
             `prompt_version`; aliases like `consent_version` are not accepted)"
                .into(),
        );
    }
    if t.redact_rules.trim().is_empty() {
        failures.push("telemetry.redact_rules is empty".into());
    }

    // The consent command is `<bin> telemetry [...]`: the leading token
    // is the binary, not a command, so only the tail is the parent path.
    let tokens: Vec<&str> = t.consent_command.split_whitespace().collect();
    let parent: Vec<String> = if tokens.len() > 1 {
        tokens[1..].iter().map(|s| s.to_string()).collect()
    } else {
        vec!["telemetry".into()]
    };
    let unmapped: Vec<String> = t
        .consent_subcommands
        .iter()
        .filter_map(|sub| {
            let mut full = parent.clone();
            full.push(sub.clone());
            (!path_exists(&spec.commands, &full)).then(|| full.join(" "))
        })
        .collect();
    if !unmapped.is_empty() {
        failures.push(format!(
            "telemetry.consent_subcommands declared but not in commands tree: {}",
            unmapped.join(", ")
        ));
    }

    if failures.is_empty() {
        return pass(
            f,
            "telemetry block well-formed; all consent subcommands declared",
        );
    }
    fail(
        f,
        failures.join("; "),
        "Fix the telemetry block: ensure categories, consent_subcommands {disable, \
         enable, inspect, reset, status}, kill_switch_envs [DO_NOT_TRACK, \
         <APP>_TELEMETRY_MODE], prompt_version, redact_rules are set, and that each \
         consent_subcommand maps to a command in the commands tree.",
    )
}
