//! Ports setter_test.go, unset_test.go and parse_scalar_test.go's
//! SetValue cases from go/core/config. Byte-exact output is not
//! asserted where only Go's re-emission would produce it; values,
//! kept comments and key order are.
#![cfg(feature = "config")]

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};

use hop_top_kit::config::{
    get, parse_scalar, set, set_value, unset, ErrorKind, Options, Scope, Value,
};
use serde::{Deserialize, Serialize};

fn opts(path: &Path) -> Options {
    Options {
        project_config_path: path.to_path_buf(),
        ..Options::default()
    }
}

fn fresh() -> (tempfile::TempDir, PathBuf) {
    let d = tempfile::tempdir().unwrap();
    let p = d.path().join("config.yaml");
    (d, p)
}

fn read(p: &Path) -> String {
    std::fs::read_to_string(p).unwrap()
}

/// Decodes the file as `any` would in Go.
fn decode(p: &Path) -> Value {
    let o = opts(p);
    let mut out = BTreeMap::new();
    for e in hop_top_kit::config::list(&o).unwrap() {
        out.insert(e.key.clone(), get(&e.key, &o).unwrap());
    }
    Value::Map(out)
}

#[test]
fn set_new_file_update_deep_overwrite() {
    let (_d, p) = fresh();
    set("name", "alice", Scope::Project, &opts(&p)).unwrap();
    assert!(read(&p).contains("name: alice"));

    let (_d, p) = fresh();
    std::fs::write(&p, "name: alice\nport: \"8080\"\n").unwrap();
    set("debug", "true", Scope::Project, &opts(&p)).unwrap();
    let c = read(&p);
    assert!(c.contains("name: alice") && c.contains("port:") && c.contains("debug: \"true\""));

    let (_d, p) = fresh();
    set("a.b.c", "deep", Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "a:\n  b:\n    c: deep\n");

    let (_d, p) = fresh();
    std::fs::write(&p, "name: alice\n").unwrap();
    set("name", "bob", Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "name: bob\n");
}

#[test]
fn set_scope_user_and_empty_scope_and_dirs() {
    let (u, p) = (tempfile::tempdir().unwrap(), tempfile::tempdir().unwrap());
    let (up, pp) = (u.path().join("config.yaml"), p.path().join("config.yaml"));
    let o = Options {
        user_config_path: up.clone(),
        project_config_path: pp.clone(),
        ..Options::default()
    };
    set("key", "val", Scope::User, &o).unwrap();
    assert!(read(&up).contains("key: val"));
    assert!(!pp.exists());

    let err = set("key", "val", Scope::User, &Options::default()).unwrap_err();
    assert!(err.is(ErrorKind::EmptyScope));
    let err = set_value("key", &1, Scope::User, &Options::default()).unwrap_err();
    assert!(err.is(ErrorKind::EmptyScope));

    let d = tempfile::tempdir().unwrap();
    let deep = d.path().join("a/b/config.yaml");
    set("k", "v", Scope::Project, &opts(&deep)).unwrap();
    assert!(read(&deep).contains("k: v"));
}

#[test]
fn set_comment_preservation() {
    let (_d, p) = fresh();
    std::fs::write(&p, "name: alice # the user name\n").unwrap();
    set("port", "9090", Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "name: alice # the user name\nport: \"9090\"\n");

    let (_d, p) = fresh();
    std::fs::write(&p, "# block comment about name\nname: alice\n").unwrap();
    set("name", "bob", Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "# block comment about name\nname: bob\n");

    let (_d, p) = fresh();
    std::fs::write(
        &p,
        "# first section\nalpha: one\n# second section\nbeta: two\n",
    )
    .unwrap();
    set("gamma", "three", Scope::Project, &opts(&p)).unwrap();
    assert_eq!(
        read(&p),
        "# first section\nalpha: one\n# second section\nbeta: two\ngamma: three\n"
    );
}

#[test]
fn set_empty_and_whitespace_files() {
    for body in ["", "  \n\t\n  "] {
        let (_d, p) = fresh();
        std::fs::write(&p, body).unwrap();
        set("key", "val", Scope::Project, &opts(&p)).unwrap();
        assert_eq!(read(&p), "key: val\n");
    }
}

#[derive(Debug, Default, Serialize, Deserialize)]
#[serde(default)]
struct Threshold {
    keyword_threshold: f64,
}

#[test]
fn set_value_float_round_trips_into_float_field() {
    let (_d, p) = fresh();
    set_value("keyword_threshold", &0.9, Scope::Project, &opts(&p)).unwrap();
    let c = read(&p);
    assert!(c.contains("keyword_threshold: 0.9"));
    assert!(!c.contains("\"0.9\""));
    let mut cfg = Threshold::default();
    hop_top_kit::config::load(
        &mut cfg,
        &Options {
            user_config_path: p,
            ..Options::default()
        },
    )
    .unwrap();
    assert!((cfg.keyword_threshold - 0.9).abs() < 1e-9);
}

#[test]
fn set_float_still_quoted() {
    for (v, want) in [
        ("0.9", "keyword_threshold: \"0.9\""),
        ("123", "keyword_threshold: \"123\""),
        ("true", "keyword_threshold: \"true\""),
        ("null", "keyword_threshold: \"null\""),
        ("abc", "keyword_threshold: abc"),
    ] {
        let (_d, p) = fresh();
        set("keyword_threshold", v, Scope::Project, &opts(&p)).unwrap();
        assert!(read(&p).contains(want), "{v}: {}", read(&p));
    }
}

#[test]
fn set_value_typed_scalars() {
    let cases: Vec<(Value, &str)> = vec![
        (Value::Float(0.9), "k: 0.9"),
        (Value::Int(3), "k: 3"),
        (Value::Bool(true), "k: true"),
        (Value::Bool(false), "k: false"),
        (Value::Null, "k: null"),
        ("abc".into(), "k: abc"),
        ("0.9".into(), "k: \"0.9\""),
    ];
    for (v, want) in cases {
        let (_d, p) = fresh();
        set_value("k", &v, Scope::Project, &opts(&p)).unwrap();
        assert!(read(&p).contains(want), "{v:?}: {}", read(&p));
    }
    let (_d, p) = fresh();
    set_value("k", &None::<i32>, Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "k: null\n");
}

#[test]
fn yaml11_lookalikes() {
    for value in ["yes", "no", "on", "off"] {
        let (_d, p) = fresh();
        set("k", value, Scope::Project, &opts(&p)).unwrap();
        assert_eq!(read(&p), format!("k: {value}\n"));
        assert_eq!(get("k", &opts(&p)).unwrap(), Value::from(value));
        let (_d, p) = fresh();
        set_value("k", value, Scope::Project, &opts(&p)).unwrap();
        assert_eq!(read(&p), format!("k: \"{value}\"\n"));
        assert_eq!(get("k", &opts(&p)).unwrap(), Value::from(value));
    }
}

#[test]
fn set_value_append_and_update_branches() {
    let (_d, p) = fresh();
    std::fs::write(&p, "name: alice\n").unwrap();
    set_value("threshold", &0.9, Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "name: alice\nthreshold: 0.9\n");

    let (_d, p) = fresh();
    std::fs::write(&p, "threshold: \"0.5\"\n").unwrap();
    set_value("threshold", &0.9, Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "threshold: 0.9\n");
}

#[test]
fn write_does_not_corrupt_neighbors() {
    let original = "threshold: 0.9\nretries: 3\nenabled: true\nmissing: null\nname: alice\n";
    for typed in [false, true] {
        let (_d, p) = fresh();
        std::fs::write(&p, original).unwrap();
        if typed {
            set_value("other", "x", Scope::Project, &opts(&p)).unwrap();
        } else {
            set("other", "x", Scope::Project, &opts(&p)).unwrap();
        }
        assert_eq!(read(&p), format!("{original}other: x\n"));
    }
}

#[test]
fn set_value_repeated_writes_are_stable() {
    let (_d, p) = fresh();
    std::fs::write(&p, "threshold: 0.9\nretries: 3\n").unwrap();
    set_value("threshold", &0.75, Scope::Project, &opts(&p)).unwrap();
    let first = read(&p);
    for _ in 0..3 {
        set_value("threshold", &0.75, Scope::Project, &opts(&p)).unwrap();
        assert_eq!(read(&p), first);
    }
    assert_eq!(first, "threshold: 0.75\nretries: 3\n");
}

#[test]
fn set_downgrades_typed_neighbor_value() {
    let (_d, p) = fresh();
    std::fs::write(&p, "threshold: 0.9\n").unwrap();
    set("threshold", "0.5", Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "threshold: \"0.5\"\n");
    set_value("threshold", &parse_scalar("0.5"), Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "threshold: 0.5\n");
}

#[test]
fn set_value_deep_key_and_non_scalar() {
    let (_d, p) = fresh();
    set_value("a.b.threshold", &0.9, Scope::Project, &opts(&p)).unwrap();
    assert_eq!(get("a.b.threshold", &opts(&p)).unwrap(), Value::Float(0.9));

    #[derive(Serialize)]
    struct Db {
        host: String,
        tls: bool,
    }
    let (_d, p) = fresh();
    let mut m = BTreeMap::new();
    m.insert("host", Value::from("localhost"));
    m.insert("port", Value::Int(5432));
    set_value("db", &m, Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "db:\n  host: localhost\n  port: 5432\n");

    let (_d, p) = fresh();
    set_value("db", &["a", "b"], Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "db:\n  - a\n  - b\n");

    let (_d, p) = fresh();
    set_value(
        "db",
        &Db {
            host: "example.com".into(),
            tls: true,
        },
        Scope::Project,
        &opts(&p),
    )
    .unwrap();
    assert_eq!(read(&p), "db:\n  host: example.com\n  tls: true\n");
}

#[test]
fn set_value_non_scalar_replaces_scalar() {
    let (_d, p) = fresh();
    std::fs::write(&p, "db: sqlite\n").unwrap();
    let mut m = BTreeMap::new();
    m.insert("driver", "postgres");
    set_value("db", &m, Scope::Project, &opts(&p)).unwrap();
    assert_eq!(read(&p), "db:\n  driver: postgres\n");
}

#[test]
fn set_value_comment_preservation() {
    let cases: Vec<(&str, &str, Value, &str)> = vec![
        (
            "name: alice # the user name\n",
            "threshold",
            Value::Float(0.9),
            "name: alice # the user name\nthreshold: 0.9\n",
        ),
        (
            "# block comment about threshold\nthreshold: \"0.5\"\n",
            "threshold",
            Value::Float(0.9),
            "# block comment about threshold\nthreshold: 0.9\n",
        ),
        (
            "threshold: \"0.5\" # tuning knob\n",
            "threshold",
            Value::Float(0.9),
            "threshold: 0.9 # tuning knob\n",
        ),
        (
            "# first section\nalpha: one\n# second section\nbeta: two\n",
            "gamma",
            Value::Int(3),
            "# first section\nalpha: one\n# second section\nbeta: two\ngamma: 3\n",
        ),
    ];
    for (orig, key, v, want) in cases {
        let (_d, p) = fresh();
        std::fs::write(&p, orig).unwrap();
        set_value(key, &v, Scope::Project, &opts(&p)).unwrap();
        assert_eq!(read(&p), want);
    }
}

#[derive(Debug, Deserialize, PartialEq)]
struct Pair {
    base: Value,
    other: Value,
    #[serde(default)]
    third: Option<Value>,
}

fn decode_pair(p: &Path) -> Pair {
    let mut v = Pair {
        base: Value::Null,
        other: Value::Null,
        third: None,
    };
    #[derive(Serialize, Deserialize, Default)]
    #[serde(default)]
    struct Raw {
        base: Value,
        other: Value,
        third: Option<Value>,
    }
    let mut raw = Raw::default();
    hop_top_kit::config::load(
        &mut raw,
        &Options {
            user_config_path: p.to_path_buf(),
            ..Options::default()
        },
    )
    .unwrap();
    v.base = raw.base;
    v.other = raw.other;
    v.third = raw.third;
    v
}

#[test]
fn set_value_preserves_anchors() {
    type Want = (i64, i64, Option<i64>);
    let cases: Vec<(&str, &str, Want)> = vec![
        ("base: &b 1\nother: *b\n", "base", (2, 2, None)),
        ("base: &b\n  x: 1\nother: *b\n", "base", (2, 2, None)),
        ("base: &b 1\nother: *b\n", "other", (1, 2, None)),
        ("base: &b 1\nother: *b\n", "third", (1, 1, Some(9))),
    ];
    for (orig, key, (b, o, t)) in cases {
        let (_d, p) = fresh();
        std::fs::write(&p, orig).unwrap();
        let v = if key == "third" { 9 } else { 2 };
        set_value(key, &v, Scope::Project, &opts(&p)).unwrap();
        let got = decode_pair(&p);
        assert_eq!(got.base, Value::Int(b), "{orig} {key}: {}", read(&p));
        assert_eq!(got.other, Value::Int(o), "{orig} {key}: {}", read(&p));
        assert_eq!(got.third, t.map(Value::Int));
    }
}

#[test]
fn set_preserves_anchors() {
    let (_d, p) = fresh();
    std::fs::write(&p, "base: &b 1\nother: *b\n").unwrap();
    set("base", "two", Scope::Project, &opts(&p)).unwrap();
    assert!(read(&p).contains("&b"));
    let got = decode_pair(&p);
    assert_eq!((got.base, got.other), ("two".into(), "two".into()));
}

#[test]
fn set_value_through_parse_scalar() {
    for (arg, want) in [
        ("0.9", "k: 0.9\n"),
        ("123", "k: 123\n"),
        ("true", "k: true\n"),
        ("null", "k: null\n"),
        ("abc", "k: abc\n"),
        ("9223372036854775808", "k: \"9223372036854775808\"\n"),
        ("0x1F", "k: \"0x1F\"\n"),
        ("2024-01-01", "k: \"2024-01-01\"\n"),
    ] {
        let (_d, p) = fresh();
        set_value("k", &parse_scalar(arg), Scope::Project, &opts(&p)).unwrap();
        assert_eq!(read(&p), want, "{arg}");
    }
}

#[test]
fn unset_existing_nested_and_cleanup() {
    let (_d, p) = fresh();
    std::fs::write(&p, "name: x\nport: 8080\n").unwrap();
    unset("name", Scope::Project, &opts(&p)).unwrap();
    assert!(get("name", &opts(&p))
        .unwrap_err()
        .is(ErrorKind::KeyNotFound));
    assert_eq!(get("port", &opts(&p)).unwrap(), Value::Int(8080));

    let (_d, p) = fresh();
    std::fs::write(&p, "core:\n  a: 1\n  b: 2\n").unwrap();
    unset("core.a", Scope::Project, &opts(&p)).unwrap();
    assert!(get("core.a", &opts(&p))
        .unwrap_err()
        .is(ErrorKind::KeyNotFound));
    assert_eq!(get("core.b", &opts(&p)).unwrap(), Value::Int(2));

    let (_d, p) = fresh();
    std::fs::write(&p, "core:\n  only: val\n").unwrap();
    unset("core.only", Scope::Project, &opts(&p)).unwrap();
    assert!(get("core", &opts(&p))
        .unwrap_err()
        .is(ErrorKind::KeyNotFound));
    assert_eq!(decode(&p), Value::Map(BTreeMap::new()));
}

#[test]
fn unset_not_found_empty_scope_missing_file() {
    let (_d, p) = fresh();
    std::fs::write(&p, "a: 1\n").unwrap();
    let err = unset("nonexistent", Scope::Project, &opts(&p)).unwrap_err();
    assert!(err.is(ErrorKind::KeyNotFound));
    assert_eq!(err.to_string(), "config unset: config: key not found");
    assert!(unset("key", Scope::Project, &Options::default())
        .unwrap_err()
        .is(ErrorKind::EmptyScope));
    let (_d, p) = fresh();
    assert!(unset("key", Scope::Project, &opts(&p))
        .unwrap_err()
        .is(ErrorKind::KeyNotFound));
}

#[test]
fn get_sees_writes() {
    let (_d, p) = fresh();
    std::fs::write(&p, "k: 1\n").unwrap();
    assert_eq!(get("k", &opts(&p)).unwrap(), Value::Int(1));
    set_value("k", &2, Scope::Project, &opts(&p)).unwrap();
    assert_eq!(get("k", &opts(&p)).unwrap(), Value::Int(2));
}
