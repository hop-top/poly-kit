//! Ports parse_scalar_test.go, precedence_test.go, resolved_test.go,
//! scope_test.go and errors_test.go from go/core/config.
#![cfg(feature = "config")]

use std::collections::HashMap;
use std::sync::Arc;

use hop_top_kit::config::{
    format_resolved, format_resolved_table, load_yaml_layer, parse_scalar, resolve_string,
    resolved_as_json, scope_path, sorted_by_key, DefaultLayer, EnvFn, EnvLayer, ErrorKind,
    FlagLayer, Options, Origin, Resolved, SchemaError, Scope, Value,
};

#[test]
fn parse_scalar_table() {
    let s = |v: &str| Value::String(v.into());
    let cases: Vec<(&str, Value)> = vec![
        ("0.9", Value::Float(0.9)),
        ("-0.5", Value::Float(-0.5)),
        ("1e3", Value::Float(1000.0)),
        ("123", Value::Int(123)),
        ("-7", Value::Int(-7)),
        ("0", Value::Int(0)),
        ("true", Value::Bool(true)),
        ("false", Value::Bool(false)),
        ("null", Value::Null),
        ("Null", Value::Null),
        ("NULL", Value::Null),
        ("~", Value::Null),
        ("yes", s("yes")),
        ("no", s("no")),
        ("on", s("on")),
        ("off", s("off")),
        ("0x1F", s("0x1F")),
        ("0o17", s("0o17")),
        ("0b101", s("0b101")),
        ("2024-01-01", s("2024-01-01")),
        ("1_000", s("1_000")),
        ("9223372036854775807", Value::Int(i64::MAX)),
        ("9223372036854775808", s("9223372036854775808")),
        ("-9223372036854775808", Value::Int(i64::MIN)),
        ("-9223372036854775809", s("-9223372036854775809")),
        (
            "12345678901234567890123456789012345",
            s("12345678901234567890123456789012345"),
        ),
        ("1e400", s("1e400")),
        ("True", s("True")),
        ("TRUE", s("TRUE")),
        ("abc", s("abc")),
        ("", s("")),
        (" 1 ", s(" 1 ")),
        ("/usr/local/bin", s("/usr/local/bin")),
        ("1.2.3", s("1.2.3")),
        (".", s(".")),
        ("-", s("-")),
    ];
    for (input, want) in cases {
        assert_eq!(parse_scalar(input), want, "{input:?}");
    }
}

#[test]
fn parse_scalar_inf_and_nan() {
    for i in [".inf", ".Inf", ".INF", "+.inf"] {
        assert!(matches!(parse_scalar(i), Value::Float(f) if f.is_infinite() && f > 0.0));
    }
    for i in ["-.inf", "-.Inf", "-.INF"] {
        assert!(matches!(parse_scalar(i), Value::Float(f) if f.is_infinite() && f < 0.0));
    }
    for i in [".nan", ".NaN", ".NAN"] {
        assert!(matches!(parse_scalar(i), Value::Float(f) if f.is_nan()));
    }
}

fn env(pairs: &[(&str, &str)]) -> EnvFn {
    let m: HashMap<String, String> = pairs
        .iter()
        .map(|(k, v)| (k.to_string(), v.to_string()))
        .collect();
    Arc::new(move |k| m.get(k).cloned())
}

fn flags(pairs: &[(&str, &str)], changed: Option<&[(&str, bool)]>) -> FlagLayer {
    FlagLayer {
        flags: pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect(),
        changed: changed.map(|c| c.iter().map(|(k, v)| (k.to_string(), *v)).collect()),
    }
}

fn defaults(pairs: &[(&str, &str)]) -> DefaultLayer {
    DefaultLayer {
        defaults: pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect(),
    }
}

#[test]
fn resolve_string_flag_wins() {
    let flag = flags(&[("out_dir", "from-flag")], Some(&[("out_dir", true)]));
    let env = EnvLayer::with_env("MYTOOL", env(&[("MYTOOL_OUT_DIR", "from-env")]));
    let def = defaults(&[("out_dir", "default")]);
    let got = resolve_string("out_dir", &[&flag, &env, &def]);
    assert_eq!(got.value, "from-flag");
    assert_eq!(got.origin, Origin::Flag);
    assert_eq!(got.detail, "--out_dir");
}

#[test]
fn resolve_string_env_wins_when_no_flag() {
    let flag = flags(&[], Some(&[]));
    let env = EnvLayer::with_env("MYTOOL", env(&[("MYTOOL_OUT_DIR", "from-env")]));
    let def = defaults(&[("out_dir", "default")]);
    let got = resolve_string("out_dir", &[&flag, &env, &def]);
    assert_eq!(got.value, "from-env");
    assert_eq!(got.origin, Origin::Env);
    assert_eq!(got.detail, "MYTOOL_OUT_DIR");
}

#[test]
fn resolve_string_project_wins_over_global() {
    let dir = tempfile::tempdir().unwrap();
    let gp = dir.path().join("global.yaml");
    let pp = dir.path().join("project.yaml");
    std::fs::write(&gp, "out_dir: g\nlog_level: info\n").unwrap();
    std::fs::write(&pp, "out_dir: p\n").unwrap();
    let gl = load_yaml_layer(gp.to_str().unwrap(), Origin::Global).unwrap();
    let pl = load_yaml_layer(pp.to_str().unwrap(), Origin::Project).unwrap();
    let got = resolve_string("out_dir", &[&pl, &gl]);
    assert_eq!(got.value, "p");
    assert_eq!(got.origin, Origin::Project);
    assert_eq!(got.detail, pp.to_str().unwrap());
    let got = resolve_string("log_level", &[&pl, &gl]);
    assert_eq!(got.value, "info");
    assert_eq!(got.origin, Origin::Global);
}

#[test]
fn resolve_string_default_when_missing_everywhere() {
    let flag = flags(&[], None);
    let env = EnvLayer::with_env("NOPE", env(&[]));
    let def = defaults(&[("k", "fallback")]);
    let got = resolve_string("k", &[&flag, &env, &def]);
    assert_eq!(got.value, "fallback");
    assert_eq!(got.origin, Origin::Default);
    assert_eq!(got.detail, "built-in");
}

#[test]
fn resolve_string_no_match_returns_empty_default() {
    let got = resolve_string("missing", &[&defaults(&[])]);
    assert_eq!(got.value, "");
    assert_eq!(got.origin, Origin::Default);
}

#[test]
fn flag_layer_respects_changed() {
    let flag = flags(&[("k", "from-flag")], Some(&[("k", false)]));
    let got = resolve_string("k", &[&flag, &defaults(&[("k", "default")])]);
    assert_eq!(got.value, "default");
    assert_eq!(got.origin, Origin::Default);
}

#[test]
fn flag_layer_nil_changed_considers_all() {
    let got = resolve_string("k", &[&flags(&[("k", "v")], None)]);
    assert_eq!(got.value, "v");
    assert_eq!(got.origin, Origin::Flag);
}

#[test]
fn env_layer_normalizes_key() {
    let env = EnvLayer::with_env("APP", env(&[("APP_LOG_LEVEL", "debug")]));
    assert_eq!(resolve_string("log.level", &[&env]).value, "debug");
    assert_eq!(resolve_string("log-level", &[&env]).value, "debug");
}

#[test]
fn env_layer_empty_value_is_set() {
    let env = EnvLayer::with_env("APP", env(&[("APP_K", "")]));
    let got = resolve_string("k", &[&env, &defaults(&[("k", "d")])]);
    assert_eq!((got.value.as_str(), got.origin), ("", Origin::Env));
}

#[test]
fn yaml_layer_load_empty_path() {
    let l = load_yaml_layer("", Origin::Global).unwrap();
    assert!(hop_top_kit::config::Layer::lookup(&l, "anything").is_none());
}

#[test]
fn yaml_layer_load_missing() {
    let dir = tempfile::tempdir().unwrap();
    let p = dir.path().join("absent.yaml");
    let l = load_yaml_layer(p.to_str().unwrap(), Origin::Project).unwrap();
    assert!(hop_top_kit::config::Layer::lookup(&l, "k").is_none());
}

#[test]
fn yaml_layer_bool_and_int_coercion() {
    let dir = tempfile::tempdir().unwrap();
    let p = dir.path().join("c.yaml");
    std::fs::write(&p, "debug: true\nport: 8080\nnone: ~\n").unwrap();
    let l = load_yaml_layer(p.to_str().unwrap(), Origin::Global).unwrap();
    use hop_top_kit::config::Layer;
    assert_eq!(l.lookup("debug").as_deref(), Some("true"));
    assert_eq!(l.lookup("port").as_deref(), Some("8080"));
    assert_eq!(l.lookup("none").as_deref(), Some(""));
}

#[test]
fn resolve_string_full_ladder() {
    let dir = tempfile::tempdir().unwrap();
    let gp = dir.path().join("global.yaml");
    let pp = dir.path().join("project.yaml");
    std::fs::write(&gp, "k: from-global\n").unwrap();
    std::fs::write(&pp, "k: from-project\n").unwrap();
    let gl = load_yaml_layer(gp.to_str().unwrap(), Origin::Global).unwrap();
    let pl = load_yaml_layer(pp.to_str().unwrap(), Origin::Project).unwrap();
    let mut flag = flags(&[("k", "from-flag")], Some(&[("k", true)]));
    let with_env = EnvLayer::with_env("APP", env(&[("APP_K", "from-env")]));
    let no_env = EnvLayer::with_env("APP", env(&[]));
    let def = defaults(&[("k", "from-default")]);

    assert_eq!(
        resolve_string("k", &[&flag, &with_env, &pl, &gl, &def]).value,
        "from-flag"
    );
    flag.changed.as_mut().unwrap().insert("k".into(), false);
    assert_eq!(
        resolve_string("k", &[&flag, &with_env, &pl, &gl, &def]).value,
        "from-env"
    );
    assert_eq!(
        resolve_string("k", &[&flag, &no_env, &pl, &gl, &def]).value,
        "from-project"
    );
    assert_eq!(
        resolve_string("k", &[&flag, &no_env, &gl, &def]).value,
        "from-global"
    );
    assert_eq!(
        resolve_string("k", &[&flag, &no_env, &def]).value,
        "from-default"
    );
}

#[test]
fn origin_string_and_ordering() {
    assert_eq!(Origin::Default.to_string(), "default");
    assert_eq!(Origin::Global.to_string(), "global");
    assert_eq!(Origin::Project.to_string(), "project");
    assert_eq!(Origin::Env.to_string(), "env");
    assert_eq!(Origin::Flag.to_string(), "flag");
    assert!(Origin::Flag > Origin::Env);
    assert!(Origin::Env > Origin::Project);
    assert!(Origin::Project > Origin::Global);
    assert!(Origin::Global > Origin::Default);
}

#[test]
fn format_resolved_string_and_generic() {
    let r = Resolved {
        key: "out".into(),
        value: "out/".to_string(),
        origin: Origin::Project,
        detail: String::new(),
    };
    assert_eq!(format_resolved(&r), "out/ (project)");
    let r = Resolved {
        key: "n".into(),
        value: 42,
        origin: Origin::Flag,
        detail: String::new(),
    };
    assert_eq!(format_resolved(&r), "42 (flag)");
}

fn entry(k: &str, v: &str, o: Origin, d: &str) -> Resolved<String> {
    Resolved {
        key: k.into(),
        value: v.into(),
        origin: o,
        detail: d.into(),
    }
}

#[test]
fn format_resolved_table_layout() {
    let out = format_resolved_table(&[
        entry("a", "1", Origin::Flag, "--a"),
        entry("longkey", "v", Origin::Env, "MY_LONGKEY"),
    ]);
    // Same bytes Go's fmt produces for these entries.
    let want = format!(
        "KEY      VALUE  ORIGIN  DETAIL\n{}\na        1      flag    --a\nlongkey  v      env     MY_LONGKEY\n",
        "-".repeat(7 + 5 + 6 + 30)
    );
    assert_eq!(out, want);
}

#[test]
fn resolved_as_json_shape() {
    let got = resolved_as_json(&[entry("k1", "v1", Origin::Env, "K1")]);
    assert_eq!(got["k1"]["value"], "v1");
    assert_eq!(got["k1"]["origin"], "env");
    assert_eq!(got["k1"]["detail"], "K1");
}

#[test]
fn sorted_by_key_copies() {
    let entries = vec![
        entry("c", "", Origin::Default, ""),
        entry("a", "", Origin::Default, ""),
        entry("b", "", Origin::Default, ""),
    ];
    let got = sorted_by_key(&entries);
    let keys: Vec<&str> = got.iter().map(|e| e.key.as_str()).collect();
    assert_eq!(keys, ["a", "b", "c"]);
    assert_eq!(entries[0].key, "c");
}

#[test]
fn scope_paths() {
    let opts = Options {
        system_config_path: "/etc/tool/config.yaml".into(),
        user_config_path: "/home/u/.config/tool/config.yaml".into(),
        project_config_path: "/repo/.tool.yaml".into(),
        ..Options::default()
    };
    assert_eq!(
        scope_path(&opts, Scope::System).unwrap(),
        std::path::Path::new("/etc/tool/config.yaml")
    );
    assert_eq!(
        scope_path(&opts, Scope::User).unwrap(),
        std::path::Path::new("/home/u/.config/tool/config.yaml")
    );
    assert_eq!(
        scope_path(&opts, Scope::Project).unwrap(),
        std::path::Path::new("/repo/.tool.yaml")
    );
    for s in [Scope::System, Scope::User, Scope::Project] {
        let err = scope_path(&Options::default(), s).unwrap_err();
        assert!(err.is(ErrorKind::EmptyScope));
        assert_eq!(err.to_string(), "config: scope path is empty");
    }
}

#[test]
fn schema_error_texts() {
    let e = SchemaError::UnknownKey {
        key: "foo.bar".into(),
        valid_keys: vec!["a".into(), "b".into()],
    };
    assert_eq!(e.to_string(), r#"unknown key "foo.bar"; valid keys: a, b"#);
    let e = SchemaError::TypeMismatch {
        key: "port".into(),
        expected: "int".into(),
        got: "hello".into(),
    };
    assert_eq!(e.to_string(), r#"key "port" expects int, got "hello""#);
    let e = SchemaError::ConstraintViolation {
        key: "name".into(),
        constraint: "minLen(2)".into(),
        value: "x".into(),
    };
    assert_eq!(
        e.to_string(),
        r#"key "name" violates constraint minLen(2): x"#
    );
}
