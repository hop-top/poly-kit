//! Helpers for the CloudEvents vector runner: value comparison as the
//! corpus README defines it, and kit events in vector form.

use std::collections::BTreeMap;

use hop_top_kit::bus::cloudevents::DecodedEvent;
use hop_top_kit::bus::event::canonical_timestamp;
use hop_top_kit::bus::{Event, Topic};
use serde::Deserialize;
use serde_json::Value;

// --- comparison helpers ------------------------------------------------

/// JSON value equality with numbers compared as `f64`, as Go's
/// `encoding/json` decodes them into `any`.
pub fn json_eq(a: &Value, b: &Value) -> bool {
    match (a, b) {
        (Value::Number(x), Value::Number(y)) => x.as_f64() == y.as_f64(),
        (Value::Array(x), Value::Array(y)) => {
            x.len() == y.len() && x.iter().zip(y).all(|(x, y)| json_eq(x, y))
        }
        (Value::Object(x), Value::Object(y)) => {
            x.len() == y.len()
                && x.iter()
                    .all(|(k, v)| y.get(k).is_some_and(|w| json_eq(v, w)))
        }
        _ => a == b,
    }
}

pub fn parse_json(s: &str) -> Option<Value> {
    serde_json::from_str(s).ok()
}

/// Days from 0000-03-01-based civil date (Howard Hinnant).
pub fn days_from_civil(y: i64, m: i64, d: i64) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let mp = (m + 9) % 12;
    let doy = (153 * mp + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146_097 + doe - 719_468
}

/// A canonical timestamp as (UTC seconds, nanos): an instant.
pub fn instant(ts: &str) -> (i64, u32) {
    let c = canonical_timestamp(ts).unwrap_or_else(|e| panic!("timestamp {ts:?}: {e}"));
    let n = |a: usize, b: usize| c[a..b].parse::<i64>().unwrap();
    let days = days_from_civil(n(0, 4), n(5, 7), n(8, 10));
    let mut secs = days * 86_400 + n(11, 13) * 3600 + n(14, 16) * 60 + n(17, 19);
    let mut rest = &c[19..];
    let mut nanos = 0u32;
    if let Some(frac) = rest.strip_prefix('.') {
        let digits: String = frac.chars().take_while(char::is_ascii_digit).collect();
        nanos = format!("{digits:0<9}").parse().unwrap();
        rest = &frac[digits.len()..];
    }
    if rest != "Z" {
        let sign = if rest.starts_with('-') { -1 } else { 1 };
        let off =
            rest[1..3].parse::<i64>().unwrap() * 3600 + rest[4..6].parse::<i64>().unwrap() * 60;
        secs -= sign * off;
    }
    (secs, nanos)
}

/// Strict standard base64 for vector inputs (not the code under test).
pub fn b64(s: &str) -> Vec<u8> {
    const A: &[u8] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = vec![];
    let mut acc = 0u32;
    let mut bits = 0;
    for c in s.bytes().filter(|&c| c != b'=') {
        let v = A.iter().position(|&a| a == c).expect("vector base64") as u32;
        acc = (acc << 6) | v;
        bits += 6;
        if bits >= 8 {
            bits -= 8;
            out.push((acc >> bits) as u8);
        }
    }
    out
}

/// A TypeID with prefix `evt`: `evt_` and 26 Crockford base32 characters,
/// the first at most `7`.
pub fn is_evt_typeid(id: &str) -> bool {
    let Some(suffix) = id.strip_prefix("evt_") else {
        return false;
    };
    suffix.len() == 26
        && suffix.as_bytes()[0] <= b'7'
        && suffix
            .bytes()
            .all(|c| b"0123456789abcdefghjkmnpqrstvwxyz".contains(&c))
}

// --- vector events -----------------------------------------------------

/// A kit Event in vector form.
#[derive(Deserialize, Default)]
#[serde(deny_unknown_fields, default)]
pub struct VEvent {
    id: String,
    topic: String,
    source: String,
    timestamp: String,
    subject: String,
    datacontenttype: String,
    dataschema: String,
    payload: Value,
    payload_base64: Option<String>,
    workspace_id: String,
    extensions: Option<BTreeMap<String, Value>>,
}

impl VEvent {
    pub fn event(&self) -> Event {
        Event {
            topic: Topic::from(self.topic.as_str()),
            source: self.source.clone(),
            timestamp: if self.timestamp.is_empty() {
                "0001-01-01T00:00:00Z".to_string()
            } else {
                canonical_timestamp(&self.timestamp).expect("vector timestamp")
            },
            payload: self.payload.clone(),
            workspace_id: self.workspace_id.clone(),
            id: self.id.clone(),
            subject: self.subject.clone(),
            data_content_type: self.datacontenttype.clone(),
            data_schema: self.dataschema.clone(),
            extensions: self.extensions.clone().unwrap_or_default(),
        }
    }

    pub fn binary(&self) -> Option<Vec<u8>> {
        self.payload_base64.as_deref().map(b64)
    }

    /// Compares `got` with the vector; timestamps as instants.
    pub fn diff(&self, got: &DecodedEvent) -> Vec<String> {
        let want = self.event();
        let e = &got.event;
        let mut d = vec![];
        let mut field = |name: &str, g: &str, w: &str| {
            if g != w {
                d.push(format!("{name} = {g:?}, want {w:?}"));
            }
        };
        field("id", &e.id, &want.id);
        field("topic", e.topic.as_str(), want.topic.as_str());
        field("source", &e.source, &want.source);
        field("subject", &e.subject, &want.subject);
        field(
            "datacontenttype",
            &e.data_content_type,
            &want.data_content_type,
        );
        field("dataschema", &e.data_schema, &want.data_schema);
        field("workspace_id", &e.workspace_id, &want.workspace_id);
        if instant(&e.timestamp) != instant(&want.timestamp) {
            d.push(format!(
                "timestamp = {}, want {}",
                e.timestamp, want.timestamp
            ));
        }
        if !json_eq(&e.payload, &want.payload) {
            d.push(format!("payload = {}, want {}", e.payload, want.payload));
        }
        if got.binary != self.binary() {
            d.push(format!(
                "binary = {:?}, want {:?}",
                got.binary,
                self.binary()
            ));
        }
        let ext = |m: &BTreeMap<String, Value>| Value::Object(m.clone().into_iter().collect());
        if !json_eq(&ext(&e.extensions), &ext(&want.extensions)) {
            d.push(format!(
                "extensions = {}, want {}",
                ext(&e.extensions),
                ext(&want.extensions)
            ));
        }
        d
    }
}
