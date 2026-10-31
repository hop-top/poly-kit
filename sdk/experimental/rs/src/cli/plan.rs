//! The structured dry-run output of a write or destructive command.
//! Ports Go `cli.Plan` / `cli.RenderPlan`: a handler that sees
//! [`Invocation::dry_run`] builds a [`Plan`] of what it would change
//! and renders it instead of acting.

use std::collections::BTreeMap;
use std::io::Write;
use std::time::{SystemTime, UNIX_EPOCH};

use serde::Serialize;

use crate::output::CliError;

use super::run::Invocation;

/// One declared state change a [`Plan`] would apply.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct Effect {
    /// A short verb: `create`, `update`, `delete`.
    pub kind: String,
    /// The addressable resource (`alias:foo`, `/etc/hosts`).
    pub target: String,
    /// Whether the inverse command restores prior state losslessly.
    pub reversible: bool,
    /// A free-form one-line note.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub detail: String,
}

/// What a command would do if re-issued without `--dry-run`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct Plan {
    /// The full command path; defaults to the invocation's.
    pub command: String,
    /// The resolved arguments.
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub args: BTreeMap<String, String>,
    /// The ordered changes.
    pub effects: Vec<Effect>,
    /// Pre-flight checks that passed while planning.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub prerequisites_checked: Vec<String>,
    /// Non-fatal advisories for the operator.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub warnings: Vec<String>,
    /// RFC 3339 UTC time of assembly; stamped when empty.
    pub generated_at: String,
}

impl Invocation {
    /// Whether this run is a dry run: kit applied `--dry-run`, or the
    /// flag was given (a read command accepts it and still runs).
    pub fn is_dry_run(&self) -> bool {
        self.dry_run || self.globals.dry_run
    }

    /// Writes `plan` as the command's dry-run output in the active
    /// `--format`: json and yaml serialize it, anything else prints the
    /// plan table. An empty command defaults to `tool_path` and a zero
    /// time to now.
    pub fn render_plan(
        &self,
        tool_path: &str,
        mut plan: Plan,
        out: &mut dyn Write,
    ) -> Result<(), CliError> {
        if plan.command.is_empty() {
            plan.command = tool_path.to_string();
        }
        if plan.generated_at.is_empty() {
            plan.generated_at = rfc3339_now();
        }
        let io = |e: std::io::Error| CliError::generic(e.to_string());
        match self.globals.format.as_str() {
            "json" => {
                let s = serde_json::to_string_pretty(&plan)
                    .map_err(|e| CliError::generic(e.to_string()))?;
                writeln!(out, "{s}").map_err(io)
            }
            "yaml" => {
                let s =
                    serde_norway::to_string(&plan).map_err(|e| CliError::generic(e.to_string()))?;
                out.write_all(s.as_bytes()).map_err(io)
            }
            _ => {
                writeln!(out, "COMMAND   {}", plan.command).map_err(io)?;
                writeln!(out, "GENERATED {}", plan.generated_at).map_err(io)?;
                for w in &plan.warnings {
                    writeln!(out, "WARNING   {w}").map_err(io)?;
                }
                let rows = serde_json::to_value(&plan.effects)
                    .map_err(|e| CliError::generic(e.to_string()))?;
                self.render(out, &rows)
            }
        }
    }
}

/// `YYYY-MM-DDTHH:MM:SSZ` for now, from the Unix epoch (civil date
/// algorithm, no time dependency).
pub(super) fn rfc3339_now() -> String {
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |d| d.as_secs() as i64);
    let (days, rem) = (secs.div_euclid(86_400), secs.rem_euclid(86_400));
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1_460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + i64::from(m <= 2);
    format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z",
        rem / 3600,
        rem % 3600 / 60,
        rem % 60
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn timestamp_shape_and_epoch_math() {
        let t = rfc3339_now();
        assert_eq!(t.len(), 20, "{t}");
        assert!(t.ends_with('Z') && &t[10..11] == "T");
    }

    fn inv(format: &str) -> Invocation {
        let mut i = Invocation::default();
        i.globals.format = format.into();
        i.dry_run = true;
        i
    }

    fn plan() -> Plan {
        Plan {
            args: BTreeMap::from([("name".into(), "gg".into())]),
            effects: vec![Effect {
                kind: "create".into(),
                target: "alias:gg".into(),
                reversible: true,
                detail: "dispatch to ls".into(),
            }],
            ..Plan::default()
        }
    }

    #[test]
    fn json_plan_carries_every_field_and_defaults() {
        let mut out = Vec::new();
        inv("json")
            .render_plan("t alias add", plan(), &mut out)
            .unwrap();
        let v: serde_json::Value = serde_json::from_slice(&out).unwrap();
        assert_eq!(v["command"], "t alias add");
        assert_eq!(v["effects"][0]["kind"], "create");
        assert_eq!(v["effects"][0]["reversible"], true);
        assert_eq!(v["args"]["name"], "gg");
        assert!(v["generated_at"].as_str().unwrap().ends_with('Z'));
        assert!(v.get("warnings").is_none());
    }

    #[test]
    fn table_plan_has_a_header_block_and_effect_rows() {
        let mut out = Vec::new();
        inv("table")
            .render_plan("t alias add", plan(), &mut out)
            .unwrap();
        let s = String::from_utf8(out).unwrap();
        assert!(s.starts_with("COMMAND   t alias add\nGENERATED 20"), "{s}");
        assert!(s.contains("alias:gg"), "{s}");
    }
}
