//! The [`CloudEvent`] struct API: validation and the structured-mode JSON
//! codec, from `go/runtime/bus/cloudevents_test.go`. The shared vectors
//! run in `bus_cloudevents_conformance.rs`.

#![cfg(feature = "bus")]

use std::collections::BTreeMap;

use hop_top_kit::bus::cloudevents::{
    from_cloud_event, unmarshal_cloud_event, CloudEvent, Data, ExtensionValue, SPEC_VERSION,
};

const ID: &str = "evt_01jg000000e008000000000000";
const CE_TIME: &str = "2026-10-25T12:00:00.5Z";

fn valid_ce() -> CloudEvent {
    CloudEvent::new(ID, "kit.test", "kit.test.order.created")
}

fn ext(pairs: &[(&str, ExtensionValue)]) -> BTreeMap<String, ExtensionValue> {
    pairs
        .iter()
        .map(|(k, v)| (k.to_string(), v.clone()))
        .collect()
}

#[test]
fn cloud_event_validate() {
    type Mut = fn(&mut CloudEvent);
    let cases: Vec<(&str, Mut, &str)> = vec![
        ("valid", |_| {}, ""),
        (
            "missing specversion",
            |c| c.spec_version.clear(),
            "cloudevents: specversion: required",
        ),
        (
            "wrong specversion",
            |c| c.spec_version = "0.3".into(),
            r#"cloudevents: specversion: unsupported value "0.3""#,
        ),
        (
            "specversion quoted like Go %q",
            |c| c.spec_version = "a\"b\n".into(),
            r#"cloudevents: specversion: unsupported value "a\"b\n""#,
        ),
        ("missing id", |c| c.id.clear(), "cloudevents: id: required"),
        (
            "missing source",
            |c| c.source.clear(),
            "cloudevents: source: required",
        ),
        (
            "missing type",
            |c| c.event_type.clear(),
            "cloudevents: type: required",
        ),
        (
            "first error wins",
            |c| {
                c.id.clear();
                c.event_type.clear()
            },
            "cloudevents: id: required",
        ),
        (
            "data not JSON",
            |c| c.data = Some(Data::Json("{".into())),
            "cloudevents: data: invalid JSON",
        ),
        (
            "uppercase extension",
            |c| c.extensions = ext(&[("Tenant", "x".into())]),
            "cloudevents: Tenant: invalid extension name",
        ),
        (
            "underscore extension",
            |c| c.extensions = ext(&[("workspace_id", "x".into())]),
            "cloudevents: workspace_id: invalid extension name",
        ),
        (
            "empty extension name",
            |c| c.extensions = ext(&[("", "x".into())]),
            r#"cloudevents: "": invalid extension name"#,
        ),
        (
            "reserved extension",
            |c| c.extensions = ext(&[("subject", "x".into())]),
            "cloudevents: subject: reserved attribute name",
        ),
        (
            "int over int32",
            |c| c.extensions = ext(&[("big", (1i64 << 31).into())]),
            "cloudevents: big: integer out of range",
        ),
        (
            "extensions sorted, first wins",
            |c| c.extensions = ext(&[("b_", 1i64.into()), ("a_", 1i64.into())]),
            "cloudevents: a_: invalid extension name",
        ),
        (
            "long name accepted",
            |c| c.extensions = ext(&[("abcdefghijklmnopqrstuvwxyz", "ok".into())]),
            "",
        ),
        (
            "year 10000",
            |c| c.time = "10000-01-01T00:00:00Z".into(),
            "cloudevents: time: invalid RFC 3339 timestamp",
        ),
        (
            "time not RFC 3339",
            |c| c.time = "2026-10-25".into(),
            "cloudevents: time: invalid RFC 3339 timestamp",
        ),
        (
            "int32 bounds accepted",
            |c| {
                c.extensions = ext(&[
                    ("lo", i64::from(i32::MIN).into()),
                    ("hi", i64::from(i32::MAX).into()),
                ])
            },
            "",
        ),
    ];
    for (name, mutate, want) in cases {
        let mut c = valid_ce();
        mutate(&mut c);
        match c.validate() {
            Ok(()) => assert_eq!(want, "", "{name}: want error"),
            Err(e) => assert_eq!(e.to_string(), want, "{name}"),
        }
    }
}

#[test]
fn cloud_event_error_parts() {
    let mut c = valid_ce();
    c.extensions = ext(&[("", "x".into())]);
    let e = c.validate().unwrap_err();
    assert_eq!(e.attribute(), "\"\"");
    assert_eq!(e.reason(), "invalid extension name");
    let e = CloudEvent::from_json(b"[1]").unwrap_err();
    assert_eq!(e.attribute(), "");
    assert_eq!(e.to_string(), "cloudevents: not a JSON object");
}

#[test]
fn cloud_event_to_json() {
    let mut c = valid_ce();
    c.subject = "a<b".into();
    c.time = CE_TIME.into();
    c.data_content_type = "application/json".into();
    c.data = Some(Data::Json(r#"{ "k" : "v" }"#.into()));
    c.extensions = ext(&[
        ("zeta", true.into()),
        ("alpha", (-3i64).into()),
        ("tenantid", "t1".into()),
    ]);
    let want = concat!(
        r#"{"specversion":"1.0","id":"evt_01jg000000e008000000000000","source":"kit.test","#,
        r#""type":"kit.test.order.created","subject":"a<b","time":"2026-10-25T12:00:00.5Z","#,
        r#""datacontenttype":"application/json","alpha":-3,"tenantid":"t1","zeta":true,"data":{"k":"v"}}"#
    );
    assert_eq!(c.to_json().unwrap(), want);

    c.data = Some(Data::Binary(b"hi".to_vec()));
    c.extensions.clear();
    assert!(c.to_json().unwrap().ends_with(r#""data_base64":"aGk="}"#));

    let err = CloudEvent::default().to_json().unwrap_err();
    assert_eq!(err.to_string(), "cloudevents: specversion: required");
}

#[test]
fn cloud_event_time_normalised_to_utc() {
    let mut c = valid_ce();
    c.time = "2026-10-25T08:00:00-04:00".into();
    assert!(c
        .to_json()
        .unwrap()
        .contains(r#""time":"2026-10-25T12:00:00Z""#));
    // The zero instant is unset, as Go's time.Time.IsZero.
    c.time = "0001-01-01T01:00:00+01:00".into();
    assert!(!c.to_json().unwrap().contains("time"));
}

#[test]
fn cloud_event_from_json() {
    let input = concat!(
        r#"{"specversion":"1.0","id":"x1","source":"/acme/core","type":"acme.core.stream.reset","#,
        r#""time":"2026-10-25T08:00:00.5-04:00","subject":null,"datacontenttype":"application/json","#,
        r#""data":{"n":1},"tenantid":"t1","seqoffset":42,"live":false,"gone":null}"#
    );
    let c = CloudEvent::from_json(input.as_bytes()).unwrap();
    let want = CloudEvent {
        spec_version: "1.0".into(),
        id: "x1".into(),
        source: "/acme/core".into(),
        event_type: "acme.core.stream.reset".into(),
        time: "2026-10-25T08:00:00.5-04:00".into(),
        data_content_type: "application/json".into(),
        data: Some(Data::Json(r#"{"n":1}"#.into())),
        extensions: ext(&[
            ("tenantid", "t1".into()),
            ("seqoffset", 42i64.into()),
            ("live", false.into()),
        ]),
        ..CloudEvent::default()
    };
    assert_eq!(c, want);
}

#[test]
fn cloud_event_from_json_data_null_vs_absent() {
    let head = r#"{"specversion":"1.0","id":"x","source":"s","type":"t""#;
    for input in [
        format!("{head}}}"),
        format!(r#"{head},"data":null}}"#),
        format!(r#"{head},"data_base64":null}}"#),
    ] {
        let c = CloudEvent::from_json(input.as_bytes()).unwrap();
        assert_eq!(c.data, None, "{input}");
    }
    let c = CloudEvent::from_json(format!(r#"{head},"data":"null"}}"#).as_bytes()).unwrap();
    assert_eq!(c.data, Some(Data::Json(r#""null""#.into())));
}

#[test]
fn cloud_event_from_json_keeps_data_text() {
    // Raw data text is kept, compacted: member order, number spelling and
    // escapes survive, as Go's json.RawMessage.
    let input = r#"{"specversion":"1.0","id":"x","source":"s","type":"t","data":{ "z" : 1.0, "a" : [1e3, "é \" x"] }}"#;
    let c = CloudEvent::from_json(input.as_bytes()).unwrap();
    assert_eq!(
        c.data,
        Some(Data::Json(r#"{"z":1.0,"a":[1e3,"é \" x"]}"#.into()))
    );
}

#[test]
fn cloud_event_from_json_escaped_member_names() {
    // Member names are compared after unescaping, as Go does.
    let input = r#"{"specversion":"1.0","id":"x","source":"s","type":"t","data":1}"#;
    let c = CloudEvent::from_json(input.as_bytes()).unwrap();
    assert_eq!(c.data, Some(Data::Json("1".into())));
}

#[test]
fn cloud_event_from_json_errors() {
    let head = r#"{"specversion":"1.0","id":"x","source":"s","type":"t""#;
    let cases = [
        ("not JSON", "{".to_string(), "cloudevents: invalid JSON"),
        (
            "trailing garbage",
            format!("{head}}} x"),
            "cloudevents: invalid JSON",
        ),
        (
            "not an object",
            "[1]".into(),
            "cloudevents: not a JSON object",
        ),
        (
            "bad base64",
            format!(r#"{head},"data_base64":"!!"}}"#),
            "cloudevents: data_base64: invalid base64",
        ),
        (
            "base64 bad padding",
            format!(r#"{head},"data_base64":"A==="}}"#),
            "cloudevents: data_base64: invalid base64",
        ),
        (
            "base64 inner padding",
            format!(r#"{head},"data_base64":"AQ==AQ=="}}"#),
            "cloudevents: data_base64: invalid base64",
        ),
        (
            "base64 non-canonical 3",
            format!(r#"{head},"data_base64":"AAB="}}"#),
            "cloudevents: data_base64: invalid base64",
        ),
        (
            "int out of range",
            format!(r#"{head},"offset":4294967296}}"#),
            "cloudevents: offset: integer out of range",
        ),
        (
            "leading zero",
            format!(r#"{head},"n":01}}"#),
            "cloudevents: invalid JSON",
        ),
        (
            "shape before value",
            r#"{"specversion":"0.3","id":7}"#.into(),
            "cloudevents: id: must be a string",
        ),
    ];
    for (name, input, want) in cases {
        let err = CloudEvent::from_json(input.as_bytes()).expect_err(name);
        assert_eq!(err.to_string(), want, "{name}");
    }
}

#[test]
fn base64_round_trip() {
    for len in 0..=7u8 {
        let bytes: Vec<u8> = (0..len)
            .map(|i| i.wrapping_mul(97).wrapping_add(200))
            .collect();
        let mut c = valid_ce();
        c.data = Some(Data::Binary(bytes.clone()));
        let back = CloudEvent::from_json(c.to_json().unwrap().as_bytes()).unwrap();
        assert_eq!(back.data, Some(Data::Binary(bytes)), "len {len}");
    }
}

#[test]
fn spec_version_constant() {
    assert_eq!(SPEC_VERSION, "1.0");
    assert_eq!(valid_ce().spec_version, SPEC_VERSION);
}

/// A safety divergence from Go, pinned: `serde_json` builds a payload
/// only up to 128 levels of nesting (Go allows 10000). The document itself
/// is checked without building values, so `data` text of any depth decodes;
/// mapping it to an [`Event`](hop_top_kit::bus::Event) payload fails.
#[test]
fn data_nested_128_deep_cannot_become_a_payload() {
    let doc = |depth: usize| {
        format!(
            r#"{{"specversion":"1.0","id":"x","source":"s","type":"t","data":{}{}}}"#,
            "[".repeat(depth),
            "]".repeat(depth)
        )
    };
    assert!(unmarshal_cloud_event(doc(127).as_bytes()).is_ok());
    for depth in [128, 100_000] {
        let c = CloudEvent::from_json(doc(depth).as_bytes()).expect("document decodes");
        let err = from_cloud_event(c).unwrap_err();
        assert_eq!(
            err.to_string(),
            "cloudevents: data: invalid JSON",
            "depth {depth}"
        );
    }
}
