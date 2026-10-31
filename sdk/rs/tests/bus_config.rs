//! Enforcement mode from string, env and config. Ports
//! `go/runtime/bus/config_test.go`.
//!
//! Go sets the process environment per test. Rust tests run in
//! parallel, so everything here reads a fake environment through the
//! `_with` variants; the real reader is tested in `bus_config_env.rs`.

#![cfg(feature = "bus")]

use std::collections::HashMap;

use hop_top_kit::bus::{
    mode_from_config_with, mode_from_env_with, Bus, ConfigGetter, Event, Mode, CONFIG_KEY_ENFORCE,
    ENV_ENFORCE,
};

fn env_of(val: Option<&'static str>) -> impl Fn(&str) -> Option<String> {
    move |k| {
        assert_eq!(k, ENV_ENFORCE, "reads only the documented variable");
        val.map(String::from)
    }
}

fn cfg(pairs: &[(&str, &str)]) -> HashMap<String, String> {
    pairs
        .iter()
        .map(|(k, v)| (k.to_string(), v.to_string()))
        .collect()
}

#[test]
fn constants() {
    assert_eq!(ENV_ENFORCE, "KIT_BUS_ENFORCE");
    assert_eq!(CONFIG_KEY_ENFORCE, "kit.bus.enforce");
}

#[test]
fn mode_from_string() {
    let cases: &[(&str, Mode, bool)] = &[
        ("", Mode::Warn, false),
        ("   ", Mode::Warn, false),
        ("off", Mode::Off, false),
        ("OFF", Mode::Off, false),
        ("  Warn  ", Mode::Warn, false),
        ("strict", Mode::Strict, false),
        ("Strict", Mode::Strict, false),
        ("loud", Mode::Warn, true),
        ("verbose", Mode::Warn, true),
    ];
    for (input, want, want_err) in cases {
        let got = input.parse::<Mode>();
        assert_eq!(got.is_err(), *want_err, "{input:?}: {got:?}");
        assert_eq!(got.unwrap_or_default(), *want, "{input:?}");
    }
}

#[test]
fn mode_from_string_error_text() {
    let e = " Loud ".parse::<Mode>().unwrap_err();
    assert_eq!(
        e.to_string(),
        "bus: unknown enforce mode \" Loud \" (want off|warn|strict)"
    );
}

#[test]
fn mode_from_env_table() {
    let cases: &[(&str, Option<&str>, Mode)] = &[
        ("unset", None, Mode::Warn),
        ("empty", Some(""), Mode::Warn),
        ("off", Some("off"), Mode::Off),
        ("warn", Some("warn"), Mode::Warn),
        ("strict", Some("strict"), Mode::Strict),
        ("strict mixed case", Some("Strict"), Mode::Strict),
        ("invalid falls back", Some("yelling"), Mode::Warn),
    ];
    for (name, val, want) in cases {
        assert_eq!(mode_from_env_with(env_of(*val)), *want, "{name}");
    }
}

#[test]
fn mode_from_config_precedence() {
    struct Case {
        name: &'static str,
        getter: Option<HashMap<String, String>>,
        env: Option<&'static str>,
        want: Mode,
    }
    let cases = [
        Case {
            name: "config overrides env",
            getter: Some(cfg(&[(CONFIG_KEY_ENFORCE, "strict")])),
            env: Some("off"),
            want: Mode::Strict,
        },
        Case {
            name: "env used when no config key",
            getter: Some(cfg(&[])),
            env: Some("off"),
            want: Mode::Off,
        },
        Case {
            name: "default when neither set",
            getter: Some(cfg(&[])),
            env: None,
            want: Mode::Warn,
        },
        Case {
            name: "nil getter falls through to env",
            getter: None,
            env: Some("strict"),
            want: Mode::Strict,
        },
        Case {
            name: "invalid config value falls through to env",
            getter: Some(cfg(&[(CONFIG_KEY_ENFORCE, "loud")])),
            env: Some("off"),
            want: Mode::Off,
        },
        Case {
            name: "all unparseable returns default",
            getter: Some(cfg(&[(CONFIG_KEY_ENFORCE, "nope")])),
            env: Some("alsono"),
            want: Mode::Warn,
        },
    ];
    for c in cases {
        let getter = c.getter.as_ref().map(|g| g as &dyn ConfigGetter);
        assert_eq!(
            mode_from_config_with(getter, env_of(c.env)),
            c.want,
            "{}",
            c.name
        );
    }
}

fn bad_topic_publish(bus: &mut Bus) -> Result<(), hop_top_kit::bus::BusError> {
    bus.publish(&Event::new("bad.topic", "src", serde_json::Value::Null))
}

#[test]
fn with_enforce_from_env() {
    let mut bus = Bus::builder()
        .enforce_from_env_with(env_of(Some("strict")))
        .build();
    let err = bad_topic_publish(&mut bus).unwrap_err();
    assert!(err.is_invalid_topic(), "{err}");
}

#[test]
fn with_enforce_overrides_env() {
    // Env says strict, code says off; the later explicit call wins.
    let mut bus = Bus::builder()
        .enforce_from_env_with(env_of(Some("strict")))
        .enforce(Mode::Off)
        .build();
    bad_topic_publish(&mut bus).expect("explicit enforce(Off) wins");
}

#[test]
fn with_enforce_from_config() {
    let g = cfg(&[(CONFIG_KEY_ENFORCE, "strict")]);
    let mut bus = Bus::builder()
        .enforce_from_config_with(Some(&g), env_of(Some("off")))
        .build();
    assert!(bad_topic_publish(&mut bus).unwrap_err().is_invalid_topic());
}
