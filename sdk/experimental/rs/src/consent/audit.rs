//! Audit records, auditors and the bus seam.

use std::fmt;
use std::str::FromStr;
use std::sync::Arc;
use std::time::SystemTime;

use serde::{Deserialize, Serialize};

use super::grant::{DecisionSource, Duration, Effect, Grant};
use super::time::{format_rfc3339, parse_rfc3339};
use crate::bus::{Bus, BusError, DynBus, Event, SharedBus};

macro_rules! wire_enum {
    ($(#[$m:meta])* $name:ident { $($v:ident => $s:literal),+ $(,)? }) => {
        $(#[$m])*
        #[derive(Clone, Debug, PartialEq, Eq, Hash)]
        pub enum $name {
            $($v,)+
            /// A value this version does not know, kept as read (a newer
            /// writer's).
            Other(String),
        }

        impl $name {
            pub fn as_str(&self) -> &str {
                match self { $($name::$v => $s,)+ $name::Other(s) => s }
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                f.write_str(self.as_str())
            }
        }

        impl FromStr for $name {
            type Err = std::convert::Infallible;
            /// Known values map to their variant; any other is `Other`.
            fn from_str(s: &str) -> Result<Self, Self::Err> {
                Ok(match s {
                    $($s => $name::$v,)+
                    other => $name::Other(other.to_string()),
                })
            }
        }
    };
}

wire_enum!(
    /// The audited moment; doubles as the last topic segment.
    AuditKind {
        Granted => "granted", Denied => "denied", Revoked => "revoked",
        Used => "used", Refused => "refused",
    }
);

wire_enum!(
    /// Why a refusal was not a decision.
    RefusalCause {
        NoPrompter => "no-prompter", PrompterError => "prompter-error",
        StoreUnreadable => "store-unreadable", AuditFailed => "audit-failed",
        StandingDeny => "standing-deny", Expired => "expired",
    }
);

/// `kit.core.consent.<event>`.
fn event_topic(event: &str) -> String {
    format!("kit.core.consent.{event}")
}

/// A newer writer's event is accepted only when its topic passes the bus
/// grammar: `validate` (four `^[a-z][a-z0-9_]*$` segments, <= 128 bytes)
/// and `validate_topic` (a past-tense action), as Go checks with
/// `bus.Validate` and `bus.ValidateTopic`. Otherwise the record is
/// malformed, so a hostile line cannot invent a topic.
fn check_event_name(k: &AuditKind) -> Result<(), String> {
    let AuditKind::Other(name) = k else {
        return Ok(());
    };
    let t = crate::bus::Topic::from(event_topic(name));
    crate::bus::validate(&t).map_err(|e| format!("audit record event {name:?}: {e}"))?;
    crate::bus::validate_topic(&t).map_err(|e| format!("audit record event {name:?}: {e}"))
}

/// One audit record. `log` and `seq` are stamped by a durable auditor
/// (`FileAuditor`); `<log>:<seq>` is the idempotency key. `revoked`
/// carries only `decision_source` (who revoked); `refused` carries
/// `decision_source` (of the governing grant, `config` when none) and
/// `reason`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AuditEvent {
    pub log: String,
    pub seq: u64,
    pub event: AuditKind,
    pub at: SystemTime,
    pub capability: String,
    pub subject: String,
    pub decision: Option<Effect>,
    pub duration: Option<Duration>,
    pub expires_at: Option<SystemTime>,
    pub decision_source: Option<DecisionSource>,
    pub prompt_version: i64,
    pub reason: Option<RefusalCause>,
    /// The line as read from the log: a record read back is written as
    /// read, so fields and values a newer writer added reach the bus.
    raw: Option<String>,
}

impl AuditEvent {
    /// A record with only the event, time and key set.
    pub fn new(event: AuditKind, at: SystemTime, capability: &str, subject: &str) -> Self {
        AuditEvent {
            log: String::new(),
            seq: 0,
            event,
            at,
            capability: capability.to_string(),
            subject: subject.to_string(),
            decision: None,
            duration: None,
            expires_at: None,
            decision_source: None,
            prompt_version: 0,
            reason: None,
            raw: None,
        }
    }

    pub(crate) fn from_grant(event: AuditKind, g: &Grant, at: SystemTime) -> Self {
        AuditEvent {
            decision: Some(g.decision),
            duration: Some(g.duration),
            expires_at: g.expires_at,
            decision_source: Some(g.decision_source.clone()),
            prompt_version: g.prompt_version,
            ..AuditEvent::new(event, at, &g.capability, &g.subject)
        }
    }

    /// `<log>:<seq>`, or `None` for a record no durable auditor stamped.
    pub fn key(&self) -> Option<String> {
        (!self.log.is_empty() && self.seq != 0).then(|| format!("{}:{}", self.log, self.seq))
    }

    fn wire(&self) -> Wire {
        let carries_version = !matches!(self.event, AuditKind::Revoked | AuditKind::Refused);
        Wire {
            v: AUDIT_VERSION,
            log: self.log.clone(),
            seq: self.seq,
            event: self.event.as_str().to_string(),
            at: format_rfc3339(self.at),
            capability: self.capability.clone(),
            subject: self.subject.clone(),
            decision: self
                .decision
                .map(|d| d.as_str().to_string())
                .unwrap_or_default(),
            duration: self
                .duration
                .map(|d| d.as_str().to_string())
                .unwrap_or_default(),
            expires_at: self.expires_at.map(format_rfc3339).unwrap_or_default(),
            decision_source: self
                .decision_source
                .as_ref()
                .map(|s| s.as_str().to_string())
                .unwrap_or_default(),
            prompt_version: carries_version.then_some(self.prompt_version),
            reason: self
                .reason
                .as_ref()
                .map(|r| r.as_str().to_string())
                .unwrap_or_default(),
        }
    }

    /// The record as one JSON line (no newline), the
    /// `contracts/consent-v1` audit shape.
    pub fn to_json(&self) -> String {
        match &self.raw {
            Some(raw) => raw.clone(),
            None => serde_json::to_string(&self.wire()).expect("audit record serializes"),
        }
    }

    /// The record as a JSON value (the bus payload).
    pub fn to_value(&self) -> serde_json::Value {
        match &self.raw {
            Some(raw) => serde_json::from_str(raw).expect("raw record was parsed"),
            None => serde_json::to_value(self.wire()).expect("audit record serializes"),
        }
    }

    /// Reads one record back.
    pub fn from_json(line: &str) -> Result<Self, String> {
        let w: Wire = serde_json::from_str(line).map_err(|e| e.to_string())?;
        if w.v != AUDIT_VERSION {
            return Err(format!(
                "audit record version {} (want {AUDIT_VERSION})",
                w.v
            ));
        }
        let at = parse_rfc3339(&w.at).ok_or_else(|| format!("audit record at {:?}", w.at))?;
        let opt = |s: &str| (!s.is_empty()).then(|| s.to_string());
        let expires_at = match opt(&w.expires_at) {
            Some(s) => Some(parse_rfc3339(&s).ok_or_else(|| format!("expires_at {s:?}"))?),
            None => None,
        };
        let event: AuditKind = w.event.parse().unwrap_or_else(|e| match e {});
        check_event_name(&event)?;
        Ok(AuditEvent {
            log: w.log,
            seq: w.seq,
            event,
            at,
            capability: w.capability,
            subject: w.subject,
            // Go keeps any string here; a value this version cannot
            // type stays in `raw`, which is what gets published.
            decision: w.decision.parse().ok(),
            duration: w.duration.parse().ok(),
            expires_at,
            decision_source: opt(&w.decision_source).map(|s| DecisionSource::from_wire(&s)),
            prompt_version: w.prompt_version.unwrap_or(0),
            reason: opt(&w.reason).map(|s| s.parse().unwrap_or_else(|e| match e {})),
            raw: Some(line.to_string()),
        })
    }
}

const AUDIT_VERSION: i64 = 1;

fn is_zero(n: &u64) -> bool {
    *n == 0
}

#[derive(Serialize, Deserialize)]
struct Wire {
    v: i64,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    log: String,
    #[serde(default, skip_serializing_if = "is_zero")]
    seq: u64,
    event: String,
    at: String,
    #[serde(default)]
    capability: String,
    #[serde(default)]
    subject: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    decision: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    duration: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    expires_at: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    decision_source: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    prompt_version: Option<i64>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    reason: String,
}

/// Records audit events. An error on a use refuses the use: no audit
/// entry, no tap.
pub trait Auditor: Send + Sync {
    fn record(&self, event: &AuditEvent) -> Result<(), String>;
}

impl<F> Auditor for F
where
    F: Fn(&AuditEvent) -> Result<(), String> + Send + Sync,
{
    fn record(&self, event: &AuditEvent) -> Result<(), String> {
        self(event)
    }
}

/// Records to each auditor in order and stops at the first error.
pub struct MultiAuditor(pub Vec<Arc<dyn Auditor>>);

impl Auditor for MultiAuditor {
    fn record(&self, event: &AuditEvent) -> Result<(), String> {
        self.0.iter().try_for_each(|a| a.record(event))
    }
}

/// The source of every consent event.
pub const EVENT_SOURCE: &str = "core.consent";

/// Bus topics per audit kind.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Topics {
    pub granted: String,
    pub denied: String,
    pub revoked: String,
    pub used: String,
    pub refused: String,
}

impl Default for Topics {
    /// `kit.core.consent.{granted,denied,revoked,used,refused}`.
    fn default() -> Self {
        Topics {
            granted: "kit.core.consent.granted".into(),
            denied: "kit.core.consent.denied".into(),
            revoked: "kit.core.consent.revoked".into(),
            used: "kit.core.consent.used".into(),
            refused: "kit.core.consent.refused".into(),
        }
    }
}

impl Topics {
    /// The topic of `kind`; a newer writer's event (checked when read)
    /// has its own, `kit.core.consent.<event>`.
    pub fn topic(&self, kind: &AuditKind) -> String {
        match kind {
            AuditKind::Granted => self.granted.clone(),
            AuditKind::Denied => self.denied.clone(),
            AuditKind::Revoked => self.revoked.clone(),
            AuditKind::Used => self.used.clone(),
            AuditKind::Refused => self.refused.clone(),
            AuditKind::Other(name) => event_topic(name),
        }
    }

    /// `self` with empty fields filled from the default.
    pub fn or_default(mut self) -> Self {
        let def = Topics::default();
        for (dst, src) in [
            (&mut self.granted, def.granted),
            (&mut self.denied, def.denied),
            (&mut self.revoked, def.revoked),
            (&mut self.used, def.used),
            (&mut self.refused, def.refused),
        ] {
            if dst.is_empty() {
                *dst = src;
            }
        }
        self
    }
}

/// The part of a bus consent needs. `Ok` is the ack: on an in-process
/// bus every sync subscriber returned `Ok`. Async subscribers and
/// network peers are not covered.
pub trait Publisher: Send + Sync {
    fn publish(&self, event: &Event) -> Result<(), BusError>;
}

impl Publisher for Bus {
    fn publish(&self, event: &Event) -> Result<(), BusError> {
        Bus::publish(self, event)
    }
}

impl Publisher for SharedBus {
    fn publish(&self, event: &Event) -> Result<(), BusError> {
        SharedBus::publish(self, event)
    }
}

impl Publisher for DynBus {
    fn publish(&self, event: &Event) -> Result<(), BusError> {
        DynBus::publish(self, event)
    }
}

impl<P: Publisher + ?Sized> Publisher for Arc<P> {
    fn publish(&self, event: &Event) -> Result<(), BusError> {
        (**self).publish(event)
    }
}

/// Publishes each record inline with the record as payload: delivery is
/// confirmed by the publisher's return, but nothing is kept for
/// redelivery. A publisher error (a veto included) refuses the use. For
/// durable at-least-once delivery, record to a `FileAuditor` and run a
/// `Relay` instead.
pub struct BusAuditor<P> {
    publisher: P,
    topics: Topics,
}

impl<P: Publisher> BusAuditor<P> {
    pub fn new(publisher: P, topics: Topics) -> Self {
        BusAuditor {
            publisher,
            topics: topics.or_default(),
        }
    }
}

impl<P: Publisher> Auditor for BusAuditor<P> {
    fn record(&self, e: &AuditEvent) -> Result<(), String> {
        let ev = Event::new_at(
            self.topics.topic(&e.event),
            EVENT_SOURCE,
            e.to_value(),
            e.at,
        );
        self.publisher.publish(&ev).map_err(|err| err.to_string())
    }
}

/// What `Grants` tells a delivery relay: a record landed, or a refusal
/// no auditor could record must go out unrecorded.
pub trait Delivery: Send + Sync {
    /// A record was written; flush soon. Never blocks.
    fn notify(&self);
    /// Best effort: no key, no redelivery, errors ignored.
    fn publish_unrecorded(&self, event: &AuditEvent);
}
