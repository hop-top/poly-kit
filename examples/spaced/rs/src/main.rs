//! spaced: satirical SpaceX CLI historian and parity test vehicle for
//! the kit CLI root, Rust implementation.
//!
//! Commands mirror the Go demo's observable contract: the same command
//! tree, descriptions, flags and the output the cross-language parity
//! harness asserts. Capabilities the Rust kit does not ship yet (daemon
//! stop, bus events, the launch wizard, `toolspec`, `config` display)
//! are left out; the harness skips their tests for this binary.

#![allow(clippy::result_large_err)]

mod data;

use std::io::Write;

use hop_top_kit::cli::{ArgSpec, CommandSpec, Config, FlagSpec, GlobalFlag, Invocation, Root};
use hop_top_kit::output::hint::{render_hints, Hint, HintStyle};
use hop_top_kit::output::CliError;
use serde_json::json;

use data::{find_daemon, find_mission, find_vehicle, Mission, DAEMONS, MISSIONS};

const DISCLAIMER: &str = "\
Not affiliated with, endorsed by, or in any way authorized by SpaceX,
Elon Musk, DOGE, NASA, the FAA, or the Starman mannequin currently past Mars.
We would, however, accept a sponsorship (https://github.com/sponsors/hop-top).
Cash, Starlink credits, or a ride on the next Crew Dragon all acceptable.";

type Out<'a> = &'a mut dyn Write;

fn root() -> Root {
    let mut cfg = Config::new(
        "spaced",
        "0.1.0",
        "satirical SpaceX CLI historian — every launch, every RUD, every daemon",
    );
    cfg.disclaimer = DISCLAIMER.into();
    // The demo declares no side-effect tiers, examples or next steps,
    // which boot-time validation asks of a shipped tool.
    cfg.disable_validate = true;
    // Accepted for contract parity with the other demos; nothing is
    // emitted from this binary.
    cfg.globals.push(GlobalFlag {
        name: "telemetry".into(),
        usage: "kit-telemetry emit mode (off|anon|full)".into(),
        default: Some("off".into()),
        ..GlobalFlag::default()
    });

    let mut root = Root::with_config(cfg);
    root.add_command(abort())
        .add_command(competitor())
        .add_command(countdown())
        .add_command(daemon())
        .add_command(elon())
        .add_command(fleet())
        .add_command(ipo())
        .add_command(launch())
        .add_command(mission())
        .add_command(starship())
        .add_command(telemetry());
    root
}

fn main() {
    let (mut out, mut err) = (std::io::stdout(), std::io::stderr());
    std::process::exit(root().run(std::env::args(), &mut out, &mut err));
}

fn mission_arg() -> ArgSpec {
    ArgSpec::new("mission").help("Mission name").required()
}

fn lookup(inv: &Invocation) -> Result<&'static Mission, CliError> {
    let name = inv.args.first().map_or("", String::as_str);
    find_mission(name).ok_or_else(|| CliError::generic(format!("mission not found: {name}")))
}

fn put(out: Out, text: &str) -> Result<(), CliError> {
    writeln!(out, "{text}").map_err(|e| CliError::generic(e.to_string()))
}

// ── mission ──────────────────────────────────────────────────────────

fn mission() -> CommandSpec {
    CommandSpec::new("mission")
        .about("Query mission history")
        .long_about("Browse and inspect the SpaceX mission archive.")
        .subcommand(CommandSpec::new("list").about("List all missions").handler(
            |inv, out, _err| {
                let rows: Vec<_> = MISSIONS
                    .iter()
                    .map(|m| {
                        json!({
                            "mission": m.name,
                            "vehicle": m.vehicle,
                            "date": m.date,
                            "outcome": m.outcome,
                        })
                    })
                    .collect();
                inv.render(out, &json!(rows))?;
                let hints = [Hint::new("Run `spaced mission inspect <name>` for detail.")];
                render_hints(
                    out,
                    &hints,
                    &inv.globals.format,
                    &inv.hint_options(),
                    &HintStyle::stdout(inv.globals.no_color),
                )
                .map_err(|e| CliError::generic(e.to_string()))
            },
        ))
        .subcommand(
            CommandSpec::new("inspect")
                .about("Inspect a mission in detail")
                .arg(ArgSpec::new("name").help("Mission name").required())
                .handler(|inv, out, _err| {
                    let m = lookup(inv)?;
                    put(out, &format!("  MISSION: {}", m.name))?;
                    for (label, value) in [
                        ("Vehicle", m.vehicle),
                        ("Date", m.date),
                        ("Orbit", m.orbit),
                        ("Payload", m.payload),
                        ("Outcome", m.outcome),
                        ("Note", m.note),
                    ] {
                        put(out, &format!("  {label:<10} {value}"))?;
                    }
                    Ok(())
                }),
        )
}

// ── launch ───────────────────────────────────────────────────────────

/// Applies the `--tag` tokens in order: `x` adds, `-x` removes, `=a,b`
/// replaces the set, `=` clears it.
fn apply_tags(tokens: &[String]) -> Vec<String> {
    let mut tags: Vec<String> = Vec::new();
    for token in tokens {
        if let Some(rest) = token.strip_prefix('=') {
            tags = rest
                .split(',')
                .map(str::trim)
                .filter(|t| !t.is_empty())
                .map(String::from)
                .collect();
        } else if let Some(rest) = token.strip_prefix('-') {
            tags.retain(|t| t != rest);
        } else if !tags.contains(token) {
            tags.push(token.clone());
        }
    }
    tags
}

fn comma_list(values: &[String]) -> Vec<String> {
    values
        .iter()
        .flat_map(|v| v.split(','))
        .map(str::trim)
        .filter(|v| !v.is_empty())
        .map(String::from)
        .collect()
}

fn launch() -> CommandSpec {
    CommandSpec::new("launch")
        .about("Launch a mission")
        // Declares the tier so kit applies its global --dry-run to the handler.
        .side_effect("write")
        .long_about("Initiate launch sequence for a named mission. Use --dry-run to simulate.")
        .arg(mission_arg())
        .flag(
            FlagSpec::strings("payload").help("Payload manifest (comma-list: cargo,crew,starlink)"),
        )
        .flag(FlagSpec::string("orbit").help("Target orbit (leo|geo|lunar|helio|tbd)"))
        .flag(
            FlagSpec::string("output")
                .short('o')
                .help("Write JSON report to file"),
        )
        .flag(FlagSpec::strings("tag").help("Tags: add x, remove -x, replace =a,b, clear ="))
        .handler(|inv, out, _err| {
            let log = inv.logger();
            let name = inv.args.first().map_or("", String::as_str);
            log.info("resolving mission", &[("name", &name)]);
            let Some(m) = find_mission(name) else {
                log.error("mission not found", &[("name", &name)]);
                return Err(CliError::generic(format!("mission not found: {name}")));
            };

            let orbit = inv.flag("orbit").unwrap_or(m.orbit);
            let payload = comma_list(inv.flags.get("payload").map_or(&[], Vec::as_slice));
            let payload_text = if payload.is_empty() {
                m.payload.to_string()
            } else {
                payload.join(", ")
            };
            let tags = apply_tags(inv.flags.get("tag").map_or(&[], Vec::as_slice));
            let tags_text = if tags.is_empty() {
                "none".to_string()
            } else {
                tags.join(", ")
            };
            log.info(
                "launch parameters",
                &[
                    ("vehicle", &m.vehicle),
                    ("orbit", &orbit),
                    ("tags", &tags_text),
                ],
            );

            if inv.dry_run {
                log.warn("dry run mode — no actual launch", &[]);
                put(out, "")?;
                put(out, "  ── DRY RUN ────────────────────────────────────────")?;
                put(out, &format!("  Mission  : {}", m.name))?;
                put(out, &format!("  Vehicle  : {}", m.vehicle))?;
                put(out, &format!("  Orbit    : {orbit}"))?;
                put(out, &format!("  Payload  : {payload_text}"))?;
                put(out, &format!("  Tags     : {tags_text}"))?;
                put(
                    out,
                    "  Status   : Would have launched. Probably would have been fine.",
                )?;
                put(out, "")?;
                return put(out, "  Dry run complete. No actual rockets were harmed.");
            }

            put(out, "")?;
            put(out, &format!("  ▶ LAUNCH SEQUENCE INITIATED: {}", m.name))?;
            put(out, &format!("  Vehicle  : {}", m.vehicle))?;
            put(out, &format!("  Orbit    : {orbit}"))?;
            put(out, &format!("  Payload  : {payload_text}"))?;
            put(out, &format!("  Tags     : {tags_text}"))?;
            put(out, &format!("  Outcome  : {}", m.outcome))?;
            put(out, &format!("  Note     : {}", m.note))?;

            if let Some(path) = inv.flag("output") {
                let report = json!({
                    "mission": m.name,
                    "vehicle": m.vehicle,
                    "orbit": orbit,
                    "payload": payload,
                    "tags": tags,
                    "outcome": m.outcome,
                    "note": m.note,
                });
                let body = serde_json::to_string_pretty(&report)
                    .map_err(|e| CliError::generic(e.to_string()))?;
                std::fs::write(path, body)
                    .map_err(|e| CliError::generic(format!("write {path}: {e}")))?;
                put(out, &format!("  Report   : {path}"))?;
            }
            Ok(())
        })
}

// ── abort, countdown ─────────────────────────────────────────────────

fn abort() -> CommandSpec {
    CommandSpec::new("abort")
        .about("Abort a mission")
        .long_about("Abort a named mission. Specify --reason for the record.")
        .arg(mission_arg())
        .flag(FlagSpec::string("reason").help("Reason for abort (optional but appreciated)"))
        .handler(|inv, out, _err| {
            let m = lookup(inv)?;
            let reason = inv
                .flag("reason")
                .unwrap_or("Unspecified. SpaceX calls this a 'hold for anomaly investigation'.");
            put(out, &format!("  ✗ ABORT INITIATED: {}", m.name))?;
            put(out, &format!("  Vehicle : {}", m.vehicle))?;
            put(out, &format!("  Reason  : {reason}"))?;
            put(out, "  Mission aborted. The FAA has been notified.")
        })
}

fn countdown() -> CommandSpec {
    CommandSpec::new("countdown")
        .about("Show countdown status for a mission")
        .arg(mission_arg())
        .handler(|inv, out, _err| {
            let m = lookup(inv)?;
            put(out, &format!("  COUNTDOWN: {}", m.name))?;
            put(out, &format!("  Vehicle    : {}", m.vehicle))?;
            put(out, &format!("  T-0        : {} 00:00:00 UTC", m.date))?;
            put(out, &format!("  Outcome    : {}", m.outcome))?;
            put(
                out,
                "  Status     : COMPLETE — we can't rewind time, but we can query it",
            )
        })
}

// ── telemetry ────────────────────────────────────────────────────────

fn telemetry() -> CommandSpec {
    CommandSpec::new("telemetry")
        .about("Mission telemetry streams")
        .subcommand(
            CommandSpec::new("get")
                .about("Get telemetry for a mission")
                .arg(mission_arg())
                .handler(|inv, out, _err| {
                    let m = lookup(inv)?;
                    let engine = if m.outcome == "RUD" { "RUD*" } else { "NOMINAL" };
                    let rows = json!([
                        {"channel": "mission", "value": m.name, "unit": "—", "status": "NOMINAL"},
                        {"channel": "altitude", "value": "420", "unit": "km", "status": "NOMINAL"},
                        {"channel": "velocity", "value": "7660", "unit": "m/s", "status": "NOMINAL"},
                        {"channel": "engine-cluster", "value": "all", "unit": "—", "status": engine},
                        {"channel": "comms", "value": "established", "unit": "—", "status": "NOMINAL"},
                    ]);
                    inv.render(out, &rows)
                }),
        )
}

// ── fleet ────────────────────────────────────────────────────────────

fn fleet() -> CommandSpec {
    CommandSpec::new("fleet")
        .about("Inspect the SpaceX vehicle fleet")
        .subcommand(
            CommandSpec::new("vehicle")
                .about("Vehicle subcommands")
                .subcommand(
                    CommandSpec::new("inspect")
                        .about("Inspect a vehicle in detail")
                        .arg(ArgSpec::new("name").help("Vehicle name").required())
                        .flag(FlagSpec::strings("systems").help("Systems to inspect (comma-list)"))
                        .handler(|inv, out, _err| {
                            let name = inv.args.first().map_or("", String::as_str);
                            let v = find_vehicle(name).ok_or_else(|| {
                                CliError::generic(format!("vehicle not found: {name}"))
                            })?;
                            put(out, &format!("  VEHICLE: {}", v.name))?;
                            put(out, &format!("  Type         {}", v.kind))?;
                            put(out, &format!("  Status       {}", v.status))?;
                            put(out, &format!("  First Flight {}", v.first_flight))?;
                            let wanted =
                                comma_list(inv.flags.get("systems").map_or(&[], Vec::as_slice));
                            put(out, "")?;
                            put(out, "  SYSTEMS")?;
                            for want in &wanted {
                                match v.systems.iter().find(|s| s.name.eq_ignore_ascii_case(want)) {
                                    Some(s) => put(
                                        out,
                                        &format!(
                                            "  {:<12} [{}] {}",
                                            s.name, s.status, s.description
                                        ),
                                    )?,
                                    None => put(out, &format!("  {want:<12} (unknown system)"))?,
                                }
                            }
                            if wanted.is_empty() {
                                for s in &v.systems {
                                    put(
                                        out,
                                        &format!(
                                            "  {:<12} [{}] {}",
                                            s.name, s.status, s.description
                                        ),
                                    )?;
                                }
                            }
                            Ok(())
                        }),
                ),
        )
}

// ── daemon ───────────────────────────────────────────────────────────

fn daemon() -> CommandSpec {
    CommandSpec::new("daemon")
        .about("Manage background controversy processes")
        .subcommand(
            CommandSpec::new("list")
                .about("List all active daemons")
                .handler(|_inv, out, _err| {
                    put(out, "  ACTIVE DAEMONS")?;
                    put(
                        out,
                        &format!("  {:<4} {:<28} {:<10} SINCE", "ID", "DAEMON", "STATUS"),
                    )?;
                    for (i, d) in DAEMONS.iter().enumerate() {
                        put(
                            out,
                            &format!(
                                "  {:03}  {:<28} {:<10} {}",
                                i + 1,
                                d.name,
                                d.status,
                                d.since
                            ),
                        )?;
                    }
                    put(out, "")?;
                    put(
                        out,
                        "  Use 'spaced daemon status <id>' for media references.",
                    )
                }),
        )
        .subcommand(
            CommandSpec::new("status")
                .about("Show daemon status and media references")
                .arg(ArgSpec::new("id").help("Daemon name").required())
                .handler(|inv, out, _err| {
                    let id = inv.args.first().map_or("", String::as_str);
                    let d = find_daemon(id)
                        .ok_or_else(|| CliError::generic(format!("daemon not found: {id}")))?;
                    put(out, &format!("  DAEMON: {}", d.name))?;
                    put(out, &format!("  Status : {}", d.status))?;
                    put(out, &format!("  Since  : {}", d.since))?;
                    put(out, &format!("  {}", d.summary))?;
                    put(out, "")?;
                    put(out, "  REFERENCES")?;
                    for r in d.references {
                        put(out, &format!("  {r}"))?;
                    }
                    Ok(())
                }),
        )
}

// ── static status commands ───────────────────────────────────────────

fn status_group(
    name: &str,
    about: &str,
    leaf_about: &str,
    lines: &'static [&'static str],
) -> CommandSpec {
    CommandSpec::new(name).about(about).subcommand(
        CommandSpec::new("status")
            .about(leaf_about)
            .handler(move |_inv, out, _err| {
                for line in lines {
                    put(out, line)?;
                }
                Ok(())
            }),
    )
}

fn elon() -> CommandSpec {
    status_group(
        "elon",
        "Elon Musk current status",
        "Display Elon's current operational status",
        &[
            "  Elon Musk status",
            "  Primary role : CEO, SpaceX (among others)",
            "  Currently    : Tweeting about SpaceX timelines. Optimistically.",
        ],
    )
}

fn ipo() -> CommandSpec {
    status_group(
        "ipo",
        "Spacex IPO status tracker",
        "Current SpaceX IPO status",
        &[
            "  IPO STATUS",
            "  Valuation : Whatever the last tender offer said",
            "  Status    : Not public. ETA: heat death of the universe.",
        ],
    )
}

fn starship() -> CommandSpec {
    status_group(
        "starship",
        "Starship program status and history",
        "Current Starship program status",
        &[
            "  STARSHIP STATUS",
            "  Program : Starship, integrated flight tests",
            "  Status  : Next flight pending FAA review. Allegedly.",
        ],
    )
}

fn competitor() -> CommandSpec {
    CommandSpec::new("competitor")
        .about("Compare SpaceX against its competitors")
        .subcommand(
            CommandSpec::new("compare")
                .about("Compare SpaceX vs a named competitor")
                .arg(ArgSpec::new("name").help("Competitor name").required())
                .handler(|inv, out, _err| {
                    let name = inv.args.first().map_or("", String::as_str);
                    let rival = match name.to_ascii_lowercase().as_str() {
                        "boeing" => "Boeing",
                        "blue-origin" | "blue origin" => "Blue Origin",
                        _ => {
                            return Err(CliError::generic(format!("competitor not found: {name}")))
                        }
                    };
                    put(out, &format!("  SpaceX vs {rival}"))?;
                    put(
                        out,
                        "  Verdict : SpaceX flies more often; the rival files more paperwork.",
                    )
                }),
        )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn s(v: &[&str]) -> Vec<String> {
        v.iter().map(|x| x.to_string()).collect()
    }

    #[test]
    fn tags_add_remove_replace_clear() {
        assert_eq!(apply_tags(&s(&["crew", "cargo"])), ["crew", "cargo"]);
        assert_eq!(apply_tags(&s(&["crew", "cargo", "-crew"])), ["cargo"]);
        assert_eq!(apply_tags(&s(&["old", "=new1,new2"])), ["new1", "new2"]);
        assert!(apply_tags(&s(&["x", "="])).is_empty());
    }

    #[test]
    fn comma_lists_flatten_and_trim() {
        assert_eq!(comma_list(&s(&["a, b", "c", ""])), ["a", "b", "c"]);
    }

    #[test]
    fn root_carries_the_eleven_commands() {
        let help = root().render_help();
        for c in [
            "abort",
            "competitor",
            "countdown",
            "daemon",
            "elon",
            "fleet",
            "ipo",
            "launch",
            "mission",
            "starship",
            "telemetry",
        ] {
            assert!(help.contains(&format!("\n  {c} ")), "{c} missing:\n{help}");
        }
        assert!(help.contains("Not affiliated"));
    }
}
