//! What goes over a link before streams: the auth frame, the plain ack
//! and kit event frames.
//!
//! The dialer's first frame is exactly `{"auth":"<token>"}` and the
//! acceptor's reply exactly `{"type":"auth_ok"}`, the bytes a Go acceptor
//! sends a dialer that advertises no stream caps. Event frames use the
//! shared codec in [`crate::bus::cloudevents`]: kit frames out, either
//! format in, as Go's legacy links do.

use serde_json::{Map, Value};

use crate::bus::cloudevents::{decode_frame, encode_frame, FrameFormat};
use crate::bus::event::Event;

/// The acceptor's ack to a dialer without stream caps.
pub(crate) const AUTH_OK: &str = r#"{"type":"auth_ok"}"#;

/// The dialer's first frame.
pub(crate) fn auth_frame(token: &str) -> String {
    let mut m = Map::new();
    m.insert("auth".into(), Value::from(token));
    Value::Object(m).to_string()
}

/// The dialer's first frame as an acceptor reads it.
#[derive(Debug, PartialEq, Eq)]
pub(crate) struct Hello {
    pub(crate) token: String,
    /// The dialer's origin id; empty when absent (legacy dialers send
    /// none, stream-capable ones do).
    pub(crate) origin: String,
}

fn object(data: &[u8]) -> Option<Map<String, Value>> {
    match serde_json::from_slice(data).ok()? {
        Value::Object(m) => Some(m),
        _ => None,
    }
}

fn member_str<'a>(m: &'a Map<String, Value>, key: &str) -> Option<&'a str> {
    m.get(key).and_then(Value::as_str)
}

/// Reads the auth frame. `None` is Go's `invalid auth`: not a JSON
/// object, or `auth` not a non-empty string. Stream members (`caps`,
/// `session`) are ignored: this acceptor answers every dialer with the
/// plain ack, which a stream-capable dialer reads as a legacy link.
pub(crate) fn parse_auth_frame(data: &[u8]) -> Option<Hello> {
    let m = object(data)?;
    let token = member_str(&m, "auth").filter(|t| !t.is_empty())?;
    Some(Hello {
        token: token.to_string(),
        origin: member_str(&m, "origin").unwrap_or_default().to_string(),
    })
}

/// Whether the acceptor's reply grants the link: an object whose `type`
/// is the string `auth_ok`. Anything else is a refusal.
pub(crate) fn is_auth_ok(data: &[u8]) -> bool {
    object(data).is_some_and(|m| member_str(&m, "type") == Some("auth_ok"))
}

/// One outbound kit frame for `e`, stamped with `origin`.
pub(crate) fn encode_event(e: &Event, origin: &str) -> String {
    // A kit frame cannot fail to encode; only CloudEvents frames can.
    encode_frame(FrameFormat::Kit, e, origin).unwrap_or_default()
}

/// One inbound frame, kit or CloudEvents, with its wire origin. `None`
/// drops it, as Go does for any frame it cannot decode.
pub(crate) fn decode_event(data: &[u8]) -> Option<(Event, String)> {
    let f = decode_frame(data).ok()?;
    Some((f.event, f.origin))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn auth_frame_is_the_legacy_shape() {
        assert_eq!(auth_frame("s3cr\"t"), r#"{"auth":"s3cr\"t"}"#);
    }

    #[test]
    fn parse_auth_frame_follows_go() {
        let ok = |s: &str| parse_auth_frame(s.as_bytes());
        assert_eq!(
            ok(r#"{"auth":"t"}"#),
            Some(Hello {
                token: "t".into(),
                origin: String::new()
            })
        );
        assert_eq!(
            ok(r#"{"auth":"t","caps":{"streams":1},"origin":"o","session":{}}"#),
            Some(Hello {
                token: "t".into(),
                origin: "o".into()
            })
        );
        assert_eq!(ok(r#"{"auth":"t","origin":7}"#).unwrap().origin, "");
        for bad in [
            "",
            "null",
            "[]",
            r#""auth""#,
            "{}",
            r#"{"auth":""}"#,
            r#"{"auth":7}"#,
            r#"{"auth":null}"#,
            r#"{"Auth":"t"}"#,
        ] {
            assert_eq!(ok(bad), None, "{bad}");
        }
    }

    #[test]
    fn only_a_string_auth_ok_type_grants() {
        assert!(is_auth_ok(AUTH_OK.as_bytes()));
        assert!(is_auth_ok(br#"{"type":"auth_ok","caps":{"streams":1}}"#));
        for bad in [
            &b"{}"[..],
            b"null",
            b"not json",
            br#"{"type":"session_error","code":"bad_request"}"#,
            br#"{"type":"AUTH_OK"}"#,
            br#"{"Type":"auth_ok"}"#,
        ] {
            assert!(!is_auth_ok(bad), "{}", String::from_utf8_lossy(bad));
        }
    }
}
