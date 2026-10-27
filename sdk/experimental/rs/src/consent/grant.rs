//! The grant record, its vocabulary and validation. Strings are the
//! cross-language contract (`contracts/consent-v1`).

use std::fmt;
use std::str::FromStr;
use std::time::SystemTime;

use super::error::{ErrorKind, GrantError};

/// The telemetry opt-in as a capability. It takes only an any-subject,
/// always grant; its record predates grants (`kit.telemetry.consent`).
pub const CAP_TELEMETRY: &str = "telemetry";

const MAX_CAPABILITY_LEN: usize = 128;
const MAX_SUBJECT_LEN: usize = 256;

macro_rules! vocabulary {
    ($(#[$m:meta])* $name:ident, $kind:ident, $want:literal { $($v:ident => $s:literal),+ $(,)? }) => {
        $(#[$m])*
        #[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
        pub enum $name { $($v),+ }

        impl $name {
            /// The wire string.
            pub fn as_str(self) -> &'static str {
                match self { $($name::$v => $s),+ }
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                f.write_str(self.as_str())
            }
        }

        impl FromStr for $name {
            type Err = GrantError;
            fn from_str(s: &str) -> Result<Self, GrantError> {
                match s {
                    $($s => Ok($name::$v),)+
                    _ => Err(GrantError::new(ErrorKind::$kind, format!("{s:?} (want {})", $want))),
                }
            }
        }
    };
}

vocabulary!(
    /// A grant's decision.
    Effect, InvalidDecision, "allow or deny" { Allow => "allow", Deny => "deny" }
);

vocabulary!(
    /// How long a decision holds. `Once` and `Session` stay in memory;
    /// `Until` and `Always` are persisted.
    Duration, InvalidDuration, "once, session, until or always" {
        Once => "once", Session => "session", Until => "until", Always => "always",
    }
);

impl Duration {
    /// Whether a grant of this duration reaches the store.
    pub fn persisted(self) -> bool {
        matches!(self, Duration::Until | Duration::Always)
    }
}

/// How a decision was reached: the telemetry-consent taxonomy.
/// `Unknown` carries a value read from a legacy telemetry record that
/// is outside it; it never validates.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum DecisionSource {
    Prompt,
    Flag,
    Env,
    Config,
    Unknown(String),
}

impl DecisionSource {
    /// The wire string.
    pub fn as_str(&self) -> &str {
        match self {
            DecisionSource::Prompt => "prompt",
            DecisionSource::Flag => "flag",
            DecisionSource::Env => "env",
            DecisionSource::Config => "config",
            DecisionSource::Unknown(s) => s,
        }
    }

    /// Maps any string, keeping one outside the taxonomy as `Unknown`.
    pub fn from_wire(s: &str) -> Self {
        match s {
            "prompt" => DecisionSource::Prompt,
            "flag" => DecisionSource::Flag,
            "env" => DecisionSource::Env,
            "config" => DecisionSource::Config,
            other => DecisionSource::Unknown(other.to_string()),
        }
    }

    pub(crate) fn validate(&self) -> Result<(), GrantError> {
        match self {
            DecisionSource::Unknown(s) => Err(GrantError::new(
                ErrorKind::InvalidSource,
                format!("{s:?} (want prompt, flag, env or config)"),
            )),
            _ => Ok(()),
        }
    }
}

impl fmt::Display for DecisionSource {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

impl FromStr for DecisionSource {
    type Err = GrantError;
    fn from_str(s: &str) -> Result<Self, GrantError> {
        let src = DecisionSource::from_wire(s);
        src.validate()?;
        Ok(src)
    }
}

/// One decision about one capability for one subject. An empty
/// `subject` means any subject; a grant for a named subject beats it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Grant {
    pub capability: String,
    pub subject: String,
    pub decision: Effect,
    pub duration: Duration,
    /// Stamped with the clock by `Grants::decide` when `None`.
    pub decided_at: Option<SystemTime>,
    /// Set for `Until` only.
    pub expires_at: Option<SystemTime>,
    pub decision_source: DecisionSource,
    pub prompt_version: i64,
}

impl Grant {
    /// A grant with no timestamps and prompt version 0.
    pub fn new(
        capability: impl Into<String>,
        subject: impl Into<String>,
        decision: Effect,
        duration: Duration,
        decision_source: DecisionSource,
    ) -> Self {
        Grant {
            capability: capability.into(),
            subject: subject.into(),
            decision,
            duration,
            decided_at: None,
            expires_at: None,
            decision_source,
            prompt_version: 0,
        }
    }

    /// Whether an `Until` grant no longer holds at `now`.
    pub fn expired(&self, now: SystemTime) -> bool {
        self.duration == Duration::Until && self.expires_at.is_some_and(|e| now >= e)
    }

    pub(crate) fn key(&self) -> Key {
        (self.capability.clone(), self.subject.clone())
    }

    /// Checks every field, in Go's order. `decided_at` must be set.
    pub fn validate(&self) -> Result<(), GrantError> {
        validate_key(&self.capability, &self.subject)?;
        match (self.duration, self.expires_at) {
            (Duration::Until, None) => {
                return Err(GrantError::new(
                    ErrorKind::InvalidExpiry,
                    "until needs expires_at",
                ))
            }
            (Duration::Until, Some(e)) if self.decided_at.is_some_and(|d| e <= d) => {
                return Err(GrantError::new(
                    ErrorKind::InvalidExpiry,
                    "expires_at must be after decided_at",
                ))
            }
            (Duration::Until, Some(_)) => {}
            (_, Some(_)) => {
                return Err(GrantError::new(
                    ErrorKind::InvalidExpiry,
                    "expires_at is only valid for until",
                ))
            }
            (_, None) => {}
        }
        self.decision_source.validate()?;
        if self.prompt_version < 0 {
            return Err(GrantError::new(
                ErrorKind::InvalidDecision,
                format!("prompt_version {} is negative", self.prompt_version),
            ));
        }
        if self.capability == CAP_TELEMETRY
            && (!self.subject.is_empty() || self.duration != Duration::Always)
        {
            return Err(GrantError::new(
                ErrorKind::UnsupportedScope,
                "telemetry takes only an any-subject always grant",
            ));
        }
        if self.decided_at.is_none() {
            return Err(GrantError::new(
                ErrorKind::InvalidDecision,
                "decided_at is required",
            ));
        }
        Ok(())
    }

    /// `validate`, and the duration must be one the store keeps.
    pub(crate) fn check_persistable(&self) -> Result<(), GrantError> {
        self.validate()?;
        if !self.duration.persisted() {
            return Err(GrantError::new(
                ErrorKind::UnsupportedScope,
                format!("{} grants are not persisted", self.duration),
            ));
        }
        Ok(())
    }
}

/// (capability, subject).
pub(crate) type Key = (String, String);

/// Checks `id` against the capability grammar: `telemetry`, or two or
/// more `^[a-z][a-z0-9_]*$` segments joined by `.`, at most 128 bytes.
pub fn validate_capability(id: &str) -> Result<(), GrantError> {
    if id == CAP_TELEMETRY {
        return Ok(());
    }
    let bad = |why: String| Err(GrantError::new(ErrorKind::InvalidCapability, why));
    if id.len() > MAX_CAPABILITY_LEN {
        return bad(format!("{id:?} longer than {MAX_CAPABILITY_LEN} bytes"));
    }
    let segs: Vec<&str> = id.split('.').collect();
    if segs.len() < 2 {
        return bad(format!(
            "{id:?} is not namespaced (want two or more dot-separated segments)"
        ));
    }
    for s in segs {
        if !valid_segment(s) {
            return bad(format!(
                "{id:?}: segment {s:?} must match ^[a-z][a-z0-9_]*$"
            ));
        }
    }
    Ok(())
}

fn valid_segment(s: &str) -> bool {
    let mut b = s.bytes();
    b.next().is_some_and(|c| c.is_ascii_lowercase())
        && b.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'_')
}

/// Checks a subject: empty (any subject), or 1-256 bytes with no
/// whitespace or control characters.
pub fn validate_subject(s: &str) -> Result<(), GrantError> {
    if s.len() > MAX_SUBJECT_LEN {
        return Err(GrantError::new(
            ErrorKind::InvalidSubject,
            format!("longer than {MAX_SUBJECT_LEN} bytes"),
        ));
    }
    if s.chars().any(|c| c.is_whitespace() || c.is_control()) {
        return Err(GrantError::new(
            ErrorKind::InvalidSubject,
            format!("{s:?} contains whitespace or a control character"),
        ));
    }
    Ok(())
}

pub(crate) fn validate_key(capability: &str, subject: &str) -> Result<(), GrantError> {
    validate_capability(capability)?;
    validate_subject(subject)
}

/// Sorts by capability, then subject (byte order).
pub(crate) fn sort_grants(gs: &mut [Grant]) {
    gs.sort_by(|a, b| {
        (a.capability.as_bytes(), a.subject.as_bytes())
            .cmp(&(b.capability.as_bytes(), b.subject.as_bytes()))
    });
}
