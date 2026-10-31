//! Shared loading and shapes for the `contracts/consent-v1` runners.
#![allow(dead_code)]

use std::fs;
use std::path::{Path, PathBuf};
use std::time::SystemTime;

use hop_top_kit::consent::{
    validate_capability, validate_subject, DecisionSource, Duration, Effect, Grant, GrantError,
};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Deserializer};
use serde_json::Value;

/// Every corpus file; one no runner reads fails the run.
pub const FILES: &[&str] = &[
    "README.md",
    "capabilities.json",
    "records.json",
    "scenarios.json",
    "migration.json",
    "delivery.json",
];

/// The tool every runner uses for per-tool state.
pub const TOOL: &str = "mytool";

pub fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../contracts/consent-v1")
}

/// Decodes a corpus file strictly and checks `version` is `v1`.
pub fn load<T: DeserializeOwned + Versioned>(name: &str) -> T {
    assert!(FILES.contains(&name), "{name} not registered");
    let raw = fs::read_to_string(dir().join(name)).unwrap();
    let doc: T = serde_json::from_str(&raw).unwrap_or_else(|e| panic!("decode {name}: {e}"));
    assert_eq!(doc.version(), "v1", "{name}: version");
    doc
}

pub trait Versioned {
    fn version(&self) -> &str;
}

/// Absent is `None`; present (`null` included) is `Some`.
pub fn present<'de, D: Deserializer<'de>>(d: D) -> Result<Option<Value>, D::Error> {
    Value::deserialize(d).map(Some)
}

pub fn time(s: &str) -> Option<SystemTime> {
    (!s.is_empty()).then(|| parse_time(s).unwrap_or_else(|| panic!("corpus time {s:?}")))
}

fn parse_time(s: &str) -> Option<SystemTime> {
    // Corpus times are UTC whole seconds: YYYY-MM-DDTHH:MM:SSZ.
    let b = s.as_bytes();
    if b.len() != 20 || b[19] != b'Z' {
        return None;
    }
    let n = |r: std::ops::Range<usize>| s.get(r)?.parse::<i64>().ok();
    let (y, m, d) = (n(0..4)?, n(5..7)?, n(8..10)?);
    let (h, mi, sec) = (n(11..13)?, n(14..16)?, n(17..19)?);
    let y2 = if m <= 2 { y - 1 } else { y };
    let era = if y2 >= 0 { y2 } else { y2 - 399 } / 400;
    let yoe = y2 - era * 400;
    let doy = (153 * ((m + 9) % 12) + 2) / 5 + d - 1;
    let days = era * 146_097 + yoe * 365 + yoe / 4 - yoe / 100 + doy - 719_468;
    let secs = days * 86_400 + h * 3600 + mi * 60 + sec;
    let base = std::time::UNIX_EPOCH;
    Some(if secs >= 0 {
        base + std::time::Duration::from_secs(secs as u64)
    } else {
        base - std::time::Duration::from_secs(secs.unsigned_abs())
    })
}

pub fn format_time(t: SystemTime) -> String {
    let secs = match t.duration_since(std::time::UNIX_EPOCH) {
        Ok(d) => d.as_secs() as i64,
        Err(e) => -(e.duration().as_secs() as i64),
    };
    let (days, rem) = (secs.div_euclid(86_400), secs.rem_euclid(86_400));
    let z = days + 719_468;
    let era = if z >= 0 { z } else { z - 146_096 } / 146_097;
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + i64::from(m <= 2);
    format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z",
        rem / 3600,
        (rem % 3600) / 60,
        rem % 60
    )
}

/// The corpus shape of a grant.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GrantJson {
    pub capability: String,
    pub subject: String,
    pub decision: String,
    pub duration: String,
    #[serde(default)]
    pub decided_at: String,
    #[serde(default)]
    pub expires_at: String,
    pub decision_source: String,
    pub prompt_version: i64,
}

impl GrantJson {
    /// The grant, failing in Go's validation order: key, decision,
    /// duration; the library checks the rest.
    pub fn to_grant(&self) -> Result<Grant, GrantError> {
        validate_capability(&self.capability)?;
        validate_subject(&self.subject)?;
        let decision: Effect = self.decision.parse()?;
        let duration: Duration = self.duration.parse()?;
        Ok(Grant {
            decided_at: time(&self.decided_at),
            expires_at: time(&self.expires_at),
            prompt_version: self.prompt_version,
            ..Grant::new(
                &self.capability,
                &self.subject,
                decision,
                duration,
                DecisionSource::from_wire(&self.decision_source),
            )
        })
    }

    pub fn from_grant(g: &Grant) -> Self {
        GrantJson {
            capability: g.capability.clone(),
            subject: g.subject.clone(),
            decision: g.decision.as_str().into(),
            duration: g.duration.as_str().into(),
            decided_at: g.decided_at.map(format_time).unwrap_or_default(),
            expires_at: g.expires_at.map(format_time).unwrap_or_default(),
            decision_source: g.decision_source.as_str().into(),
            prompt_version: g.prompt_version,
        }
    }
}

pub fn assert_grants(what: &str, got: &[Grant], want: &[GrantJson]) {
    let got: Vec<GrantJson> = got.iter().map(GrantJson::from_grant).collect();
    assert_eq!(got, want, "{what}: grants");
}

/// Writes `body` at `p` (0600 in 0700 dirs), when given.
pub fn write(p: &Path, body: Option<&str>) {
    let Some(body) = body else { return };
    fs::create_dir_all(p.parent().unwrap()).unwrap();
    fs::write(p, body).unwrap();
}

/// A YAML file parsed and compared as a JSON value; `null` means the
/// file must not exist.
pub fn assert_file(what: &str, path: &Path, want: &Value) {
    let read = fs::read_to_string(path);
    if want.is_null() {
        assert!(
            read.is_err(),
            "{what}: want no file at {}, got {read:?}",
            path.display()
        );
        return;
    }
    let body = read.unwrap_or_else(|e| panic!("{what}: {e}"));
    let got: Value =
        serde_norway::from_str(&body).unwrap_or_else(|e| panic!("{what}: parse: {e}\n{body}"));
    assert_eq!(&got, want, "{what}:\n{body}");
}
