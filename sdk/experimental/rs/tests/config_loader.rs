//! Ports loader_test.go, override_test.go, defaults_test.go,
//! env_test.go and migrate_test.go from go/core/config.
#![cfg(feature = "config")]

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use hop_top_kit::bus::ConfigGetter;
use hop_top_kit::config::{
    apply_overrides, bind_env, load, migrate, parse_config_args, parse_overrides,
    with_project_marker, EnvBind, EnvFn, ErrorKind, Map, MigrateOptions, Migration, Options, Value,
};
use serde::{Deserialize, Serialize};

#[derive(Debug, Default, Clone, Serialize, Deserialize, PartialEq)]
#[serde(default)]
struct TestCfg {
    name: String,
    debug: bool,
    port: i64,
}

fn write(dir: &Path, name: &str, body: &str) -> PathBuf {
    std::fs::create_dir_all(dir).unwrap();
    let p = dir.join(name);
    std::fs::write(&p, body).unwrap();
    p
}

#[test]
fn loader_user_overrides_default() {
    let dir = tempfile::tempdir().unwrap();
    let p = write(dir.path(), "config.yaml", "name: user-name\n");
    let mut cfg = TestCfg {
        name: "default".into(),
        port: 8080,
        ..TestCfg::default()
    };
    load(
        &mut cfg,
        &Options {
            user_config_path: p,
            ..Options::default()
        },
    )
    .unwrap();
    assert_eq!(cfg.name, "user-name");
    assert_eq!(cfg.port, 8080);
}

#[test]
fn loader_project_overrides_user() {
    let (u, p) = (tempfile::tempdir().unwrap(), tempfile::tempdir().unwrap());
    let mut cfg = TestCfg::default();
    load(
        &mut cfg,
        &Options {
            user_config_path: write(u.path(), "config.yaml", "name: user\n"),
            project_config_path: write(p.path(), "config.yaml", "name: project\n"),
            ..Options::default()
        },
    )
    .unwrap();
    assert_eq!(cfg.name, "project");
}

#[test]
fn loader_system_user_project_order() {
    let d = tempfile::tempdir().unwrap();
    let mut cfg = TestCfg::default();
    load(
        &mut cfg,
        &Options {
            system_config_path: write(d.path(), "s.yaml", "name: sys\nport: 1\ndebug: true\n"),
            user_config_path: write(d.path(), "u.yaml", "name: user\nport: 2\n"),
            project_config_path: write(d.path(), "p.yaml", "name: proj\n"),
            ..Options::default()
        },
    )
    .unwrap();
    assert_eq!(
        cfg,
        TestCfg {
            name: "proj".into(),
            port: 2,
            debug: true
        }
    );
}

#[test]
fn loader_env_override() {
    let mut cfg = TestCfg::default();
    let opts = Options::default().env_override_for(|c: &mut TestCfg| c.debug = true);
    load(&mut cfg, &opts).unwrap();
    assert!(cfg.debug);
}

#[test]
fn loader_missing_files_are_skipped() {
    let mut cfg = TestCfg {
        name: "default".into(),
        ..TestCfg::default()
    };
    load(
        &mut cfg,
        &Options {
            user_config_path: "/nonexistent/config.yaml".into(),
            project_config_path: "/also/nonexistent.yaml".into(),
            ..Options::default()
        },
    )
    .unwrap();
    assert_eq!(cfg.name, "default");
}

#[test]
fn loader_invalid_yaml_errors_with_path() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "bad.yaml", "name: [unclosed\n");
    let err = load(
        &mut TestCfg::default(),
        &Options {
            user_config_path: p.clone(),
            ..Options::default()
        },
    )
    .unwrap_err();
    assert!(err.is(ErrorKind::Yaml));
    assert!(
        err.to_string()
            .starts_with(&format!("load config {}: yaml: ", p.display())),
        "{err}"
    );
}

// --- overrides ---

#[derive(Debug, Default, Clone, Serialize, Deserialize, PartialEq)]
#[serde(default)]
struct Nested {
    inherit: String,
}

#[derive(Debug, Default, Clone, Serialize, Deserialize, PartialEq)]
#[serde(default)]
struct Sample {
    model: String,
    retries: i64,
    tags: Vec<String>,
    nested: Nested,
}

fn map(pairs: &[(&str, Value)]) -> Map {
    pairs
        .iter()
        .map(|(k, v)| (k.to_string(), v.clone()))
        .collect()
}

#[test]
fn parse_overrides_empty() {
    assert_eq!(parse_overrides(&[] as &[&str]).unwrap(), None);
}

#[test]
fn parse_overrides_scalars() {
    let got = parse_overrides(&["s=hello", "n=3", "b=true", "f=1.5"])
        .unwrap()
        .unwrap();
    assert_eq!(
        got,
        map(&[
            ("s", "hello".into()),
            ("n", Value::Int(3)),
            ("b", Value::Bool(true)),
            ("f", Value::Float(1.5)),
        ])
    );
}

#[test]
fn parse_overrides_nested_dotted_keys() {
    let got = parse_overrides(&["a.b.c=1", "a.b.d=2", "a.e=hello"])
        .unwrap()
        .unwrap();
    let b = map(&[("c", Value::Int(1)), ("d", Value::Int(2))]);
    let a = map(&[("b", Value::Map(b)), ("e", "hello".into())]);
    assert_eq!(got, map(&[("a", Value::Map(a))]));
}

#[test]
fn parse_overrides_structured_values() {
    let got = parse_overrides(&[r#"tags=["a","b","c"]"#, "m={k1: v1, k2: v2}"])
        .unwrap()
        .unwrap();
    assert_eq!(
        got["tags"],
        Value::Seq(vec!["a".into(), "b".into(), "c".into()])
    );
    assert_eq!(
        got["m"],
        Value::Map(map(&[("k1", "v1".into()), ("k2", "v2".into())]))
    );
}

#[test]
fn parse_overrides_literal_fallback() {
    let got = parse_overrides(&["x=*not-an-alias"]).unwrap().unwrap();
    assert_eq!(got["x"], Value::from("*not-an-alias"));
}

#[test]
fn parse_overrides_empty_value_is_null() {
    let got = parse_overrides(&["x="]).unwrap().unwrap();
    assert_eq!(got["x"], Value::Null);
}

#[test]
fn parse_overrides_later_wins() {
    let got = parse_overrides(&["k=first", "k=second"]).unwrap().unwrap();
    assert_eq!(got["k"], Value::from("second"));
}

#[test]
fn parse_overrides_bad_input() {
    let err = parse_overrides(&["no-equals"]).unwrap_err();
    assert_eq!(
        err.to_string(),
        r#"override "no-equals": expected key=value"#
    );
    assert!(parse_overrides(&["=value-only"]).is_err());
}

#[test]
fn parse_overrides_conflict_segment() {
    let err = parse_overrides(&["a=1", "a.b=2"]).unwrap_err();
    assert_eq!(
        err.to_string(),
        r#"override "a.b=2": conflict at "a": expected map, got int"#
    );
}

#[test]
fn apply_overrides_overwrites_scalars_and_slices() {
    let mut cfg = Sample {
        model: "default".into(),
        retries: 1,
        tags: vec!["old".into()],
        nested: Nested {
            inherit: "none".into(),
        },
    };
    let ov = parse_overrides(&[
        "model=o3",
        "retries=5",
        r#"tags=["a","b"]"#,
        "nested.inherit=all",
    ])
    .unwrap()
    .unwrap();
    apply_overrides(&mut cfg, &ov).unwrap();
    assert_eq!(cfg.model, "o3");
    assert_eq!(cfg.retries, 5);
    assert_eq!(cfg.tags, ["a", "b"]);
    assert_eq!(cfg.nested.inherit, "all");
}

#[test]
fn apply_overrides_nil_is_noop() {
    let mut cfg = Sample {
        model: "keep".into(),
        ..Sample::default()
    };
    apply_overrides(&mut cfg, &Map::new()).unwrap();
    assert_eq!(cfg.model, "keep");
}

#[test]
fn apply_overrides_type_error() {
    let mut cfg = Sample::default();
    let err = apply_overrides(&mut cfg, &map(&[("retries", "many".into())])).unwrap_err();
    assert_eq!(
        err.to_string(),
        "apply overrides: yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `many` into int"
    );
}

#[test]
fn parse_config_args_splits_paths_and_overrides() {
    let d = tempfile::tempdir().unwrap();
    let fp = write(d.path(), "extra.yaml", "model: from-file\n");
    let fps = fp.to_str().unwrap();
    let (paths, ov) = parse_config_args(&["model=o3", fps, "retries=5"], &[]).unwrap();
    assert_eq!(paths, [fp]);
    let ov = ov.unwrap();
    assert_eq!(ov["model"], Value::from("o3"));
    assert_eq!(ov["retries"], Value::Int(5));
}

#[test]
fn parse_config_args_missing_path_errors() {
    let err = parse_config_args(&["/nonexistent/file/x.yaml"], &[]).unwrap_err();
    assert_eq!(
        err.to_string(),
        r#"-c "/nonexistent/file/x.yaml": not a key=value pair and no such file"#
    );
}

#[test]
fn parse_config_args_directory_rejected() {
    let d = tempfile::tempdir().unwrap();
    let ds = d.path().to_str().unwrap();
    let err = parse_config_args(&[ds], &[]).unwrap_err();
    assert_eq!(
        err.to_string(),
        format!(r#"-c "{ds}": path is a directory, expected a file"#)
    );
}

#[test]
fn parse_config_args_directory_resolved_via_project_marker() {
    let d = tempfile::tempdir().unwrap();
    let rel = Path::new(".rlz").join("config.yaml");
    let abs = d.path().join(&rel);
    std::fs::create_dir_all(abs.parent().unwrap()).unwrap();
    std::fs::write(&abs, "model: from-marker\n").unwrap();
    let (paths, _) = parse_config_args(
        &[d.path().to_str().unwrap()],
        &[with_project_marker(rel.to_str().unwrap())],
    )
    .unwrap();
    assert_eq!(paths, [abs]);
}

#[test]
fn parse_config_args_directory_with_missing_marker_errors() {
    let d = tempfile::tempdir().unwrap();
    let rel = Path::new(".rlz").join("config.yaml");
    let err = parse_config_args(
        &[d.path().to_str().unwrap()],
        &[with_project_marker(rel.to_str().unwrap())],
    )
    .unwrap_err();
    let msg = err.to_string();
    assert!(msg.contains(d.path().join(&rel).to_str().unwrap()), "{msg}");
    assert!(msg.contains(rel.to_str().unwrap()), "{msg}");
}

#[test]
fn parse_config_args_file_path_still_works_with_project_marker() {
    let d = tempfile::tempdir().unwrap();
    let fp = write(d.path(), "extra.yaml", "model: from-file\n");
    let (paths, _) = parse_config_args(
        &[fp.to_str().unwrap()],
        &[with_project_marker(".rlz/config.yaml")],
    )
    .unwrap();
    assert_eq!(paths, [fp]);
}

#[test]
fn parse_config_args_absolute_project_marker_rejected() {
    let d = tempfile::tempdir().unwrap();
    let err = parse_config_args(
        &[d.path().to_str().unwrap()],
        &[with_project_marker("/etc/global/config.yaml")],
    )
    .unwrap_err();
    assert_eq!(
        err.to_string(),
        r#"config: ParseConfigArgs: project marker "/etc/global/config.yaml" must be a relative path"#
    );
}

#[test]
fn parse_config_args_override_tokens_unaffected_by_project_marker() {
    let (_, ov) =
        parse_config_args(&["model=o3"], &[with_project_marker(".rlz/config.yaml")]).unwrap();
    assert_eq!(ov.unwrap()["model"], Value::from("o3"));
}

#[test]
fn parse_config_args_empty_key() {
    let err = parse_config_args(&["=value"], &[]).unwrap_err();
    assert_eq!(err.to_string(), r#"-c "=value": empty key"#);
}

#[test]
fn load_extra_config_paths_layer_after_project() {
    let d = tempfile::tempdir().unwrap();
    let mut cfg = Sample::default();
    load(
        &mut cfg,
        &Options {
            project_config_path: write(d.path(), "project.yaml", "model: project\nretries: 1\n"),
            extra_config_paths: vec![write(d.path(), "extra.yaml", "model: extra\n")],
            ..Options::default()
        },
    )
    .unwrap();
    assert_eq!(cfg.model, "extra");
    assert_eq!(cfg.retries, 1);
}

#[test]
fn load_overrides_win_over_extra_paths() {
    let d = tempfile::tempdir().unwrap();
    let mut cfg = Sample::default();
    load(
        &mut cfg,
        &Options {
            extra_config_paths: vec![write(d.path(), "extra.yaml", "model: from-file\n")],
            overrides: parse_overrides(&["model=from-cli"]).unwrap(),
            ..Options::default()
        },
    )
    .unwrap();
    assert_eq!(cfg.model, "from-cli");
}

#[test]
fn load_extra_config_missing_file_is_hard_error() {
    let err = load(
        &mut Sample::default(),
        &Options {
            extra_config_paths: vec!["/nonexistent/file.yaml".into()],
            ..Options::default()
        },
    )
    .unwrap_err();
    assert!(err.is(ErrorKind::NotFound));
    assert_eq!(
        err.to_string(),
        "load config /nonexistent/file.yaml: open /nonexistent/file.yaml: no such file or directory"
    );
}

// --- defaults and env (viper's role) ---

#[derive(Debug, Default, Clone, Serialize, Deserialize, PartialEq)]
#[serde(default)]
struct Auth {
    token: String,
}

#[derive(Debug, Default, Clone, Serialize, Deserialize, PartialEq)]
#[serde(default)]
struct DefaultsSample {
    model: String,
    retries: i64,
    tags: Vec<String>,
    auth: Auth,
}

#[test]
fn apply_defaults_struct_seed_then_file_overlay() {
    let d = tempfile::tempdir().unwrap();
    let file = write(d.path(), "config.yaml", "model: from-file\n");
    let defaults = DefaultsSample {
        model: "default-model".into(),
        retries: 3,
        tags: vec!["a".into(), "b".into()],
        auth: Auth {
            token: "default-token".into(),
        },
    };
    let opts = Options {
        user_config_path: file,
        ..Options::default()
    }
    .with_defaults(&defaults)
    .unwrap();
    let mut cfg = DefaultsSample::default();
    load(&mut cfg, &opts).unwrap();
    assert_eq!(cfg.model, "from-file");
    assert_eq!(cfg.retries, 3);
    assert_eq!(cfg.tags, ["a", "b"]);
    assert_eq!(cfg.auth.token, "default-token");
}

fn env(pairs: &[(&str, &str)]) -> EnvFn {
    let m: HashMap<String, String> = pairs
        .iter()
        .map(|(k, v)| (k.to_string(), v.to_string()))
        .collect();
    Arc::new(move |k| m.get(k).cloned())
}

#[test]
fn bind_env_automatic_prefix() {
    let l = bind_env("MYAPP", &[])
        .unwrap()
        .with_env(env(&[("MYAPP_AUTH_TOKEN", "from-env")]));
    assert_eq!(l.get_string("auth.token"), "from-env");
}

#[test]
fn bind_env_explicit_override() {
    let l = bind_env(
        "MYAPP",
        &[EnvBind {
            key: "auth.token".into(),
            env: "GH_TOKEN".into(),
        }],
    )
    .unwrap()
    .with_env(env(&[("GH_TOKEN", "literal-env")]));
    assert_eq!(l.get_string("auth.token"), "literal-env");
}

#[test]
fn bind_env_automatic_beats_explicit_like_viper() {
    let l = bind_env(
        "MYAPP",
        &[EnvBind {
            key: "auth.token".into(),
            env: "GH_TOKEN".into(),
        }],
    )
    .unwrap()
    .with_env(env(&[
        ("GH_TOKEN", "explicit"),
        ("MYAPP_AUTH_TOKEN", "auto"),
    ]));
    assert_eq!(l.get_string("auth.token"), "auto");
}

#[test]
fn bind_env_no_prefix_is_allowed() {
    let l = bind_env("", &[])
        .unwrap()
        .with_env(env(&[("AUTH_TOKEN", "raw-token")]));
    assert_eq!(l.get_string("auth.token"), "raw-token");
}

#[test]
fn bind_env_rejects_malformed_explicit() {
    let err = bind_env(
        "X",
        &[EnvBind {
            key: "k".into(),
            env: String::new(),
        }],
    )
    .unwrap_err();
    assert_eq!(err.to_string(), "env bind: empty Key or Env ({Key:k Env:})");
    assert!(bind_env(
        "X",
        &[EnvBind {
            key: String::new(),
            env: "E".into()
        }]
    )
    .is_err());
}

#[test]
fn bind_env_empty_variable_is_unset() {
    let l = bind_env("APP", &[])
        .unwrap()
        .with_env(env(&[("APP_K", "")]));
    assert_eq!(l.get_value("k"), None);
}

#[test]
fn load_env_binds_hydrate_lookup_not_dst() {
    let opts = Options {
        env_prefix: "MYAPP".into(),
        ..Options::default()
    }
    .with_defaults(&DefaultsSample {
        model: "default".into(),
        retries: 3,
        ..DefaultsSample::default()
    })
    .unwrap();
    let mut cfg = DefaultsSample::default();
    load(&mut cfg, &opts).unwrap();
    let l = opts
        .env_lookup()
        .unwrap()
        .with_env(env(&[("MYAPP_MODEL", "env-model")]));
    assert_eq!(l.get_string("model"), "env-model");
    assert_eq!(cfg.model, "default");
}

#[test]
fn seed_defaults_flattens() {
    let mut l = bind_env("", &[]).unwrap().with_env(env(&[]));
    l.seed_defaults(&DefaultsSample {
        model: "m".into(),
        retries: 7,
        auth: Auth { token: "t".into() },
        ..DefaultsSample::default()
    })
    .unwrap();
    assert_eq!(l.get_string("model"), "m");
    assert_eq!(l.get_string("retries"), "7");
    assert_eq!(l.get_string("auth.token"), "t");
}

#[test]
fn seed_defaults_explicit_set_wins() {
    let mut l = bind_env("", &[]).unwrap().with_env(env(&[]));
    l.seed_defaults(&DefaultsSample {
        model: "default".into(),
        ..DefaultsSample::default()
    })
    .unwrap();
    l.set("model", "explicit".into());
    assert_eq!(l.get_string("model"), "explicit");
}

#[test]
fn defaults_then_explicit_sync() {
    let d = tempfile::tempdir().unwrap();
    let file = write(
        d.path(),
        "config.yaml",
        "model: from-file\nauth:\n  token: ftok\n",
    );
    let opts = Options {
        user_config_path: file,
        ..Options::default()
    }
    .with_defaults(&DefaultsSample {
        model: "default".into(),
        retries: 3,
        ..DefaultsSample::default()
    })
    .unwrap();
    let mut cfg = DefaultsSample::default();
    load(&mut cfg, &opts).unwrap();
    let mut l = opts.env_lookup().unwrap().with_env(env(&[]));
    assert_eq!(l.get_string("model"), "default");
    assert_eq!(cfg.model, "from-file");
    l.sync(&cfg).unwrap();
    assert_eq!(l.get_string("model"), "from-file");
    assert_eq!(l.get_string("auth.token"), "ftok");
    // The view is a bus ConfigGetter.
    assert_eq!(ConfigGetter::get(&l, "auth.token").as_deref(), Some("ftok"));
}

// --- migrate ---

fn identity(from: i64, to: i64) -> Migration {
    Migration::new(from, to, Ok)
}

#[test]
fn migrate_noop_when_at_latest() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "c.yaml", "schema_version: 2\nmodel: x\n");
    let out = migrate(
        &p,
        &[identity(0, 1), identity(1, 2)],
        &MigrateOptions::default(),
    )
    .unwrap();
    assert_eq!(out["schema_version"], Value::Int(2));
}

#[test]
fn migrate_applies_in_order() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "c.yaml", "model: old\n");
    let migs = vec![
        Migration::new(0, 1, |mut m| {
            m.insert("retries".into(), Value::Int(3));
            Ok(m)
        }),
        Migration::new(1, 2, |mut m| {
            if let Some(v) = m.remove("model") {
                m.insert("engine".into(), v);
            }
            Ok(m)
        }),
    ];
    let seen = Arc::new(Mutex::new(Vec::new()));
    let s2 = seen.clone();
    let out = migrate(
        &p,
        &migs,
        &MigrateOptions {
            on_migration: Some(Arc::new(move |_, m| s2.lock().unwrap().push(m.to))),
            ..MigrateOptions::default()
        },
    )
    .unwrap();
    assert_eq!(*seen.lock().unwrap(), [1, 2]);
    assert_eq!(out["schema_version"], Value::Int(2));
    assert!(!out.contains_key("model"));
    assert_eq!(out["engine"], Value::from("old"));
    assert_eq!(out["retries"], Value::Int(3));
}

#[test]
fn migrate_gap_in_chain_errors() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "c.yaml", "schema_version: 0\n");
    let err = migrate(
        &p,
        &[identity(0, 1), identity(2, 3)],
        &MigrateOptions::default(),
    )
    .unwrap_err();
    assert!(err.is(ErrorKind::Migration));
    assert_eq!(
        err.to_string(),
        format!(
            "migrate {}: no migration from version 1 (highest target is 3); chain has a gap",
            p.display()
        )
    );
}

#[test]
fn migrate_step_error_wraps() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "c.yaml", "a: 1\n");
    let err = migrate(
        &p,
        &[Migration::new(0, 1, |_| Err("boom".into()))],
        &MigrateOptions::default(),
    )
    .unwrap_err();
    assert_eq!(
        err.to_string(),
        format!("migrate {}: migration 0 -> 1: boom", p.display())
    );
}

#[test]
fn migrate_write_back_persists_migrated_shape() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "c.yaml", "model: old\n");
    migrate(
        &p,
        &[Migration::new(0, 1, |mut m| {
            m.insert("model".into(), "new".into());
            Ok(m)
        })],
        &MigrateOptions {
            write_back: true,
            ..MigrateOptions::default()
        },
    )
    .unwrap();
    let back = migrate(&p, &[], &MigrateOptions::default()).unwrap();
    assert_eq!(back["model"], Value::from("new"));
    assert_eq!(back["schema_version"], Value::Int(1));
}

#[test]
fn migrate_does_not_write_back_when_no_migrations_applied() {
    let d = tempfile::tempdir().unwrap();
    let original = "schema_version: 1\nmodel: keep\n";
    let p = write(d.path(), "c.yaml", original);
    migrate(
        &p,
        &[identity(0, 1)],
        &MigrateOptions {
            write_back: true,
            ..MigrateOptions::default()
        },
    )
    .unwrap();
    assert_eq!(std::fs::read_to_string(&p).unwrap(), original);
}

#[test]
fn migrate_missing_file() {
    let err = migrate(
        "/nonexistent/x.yaml",
        &[identity(0, 1)],
        &MigrateOptions::default(),
    )
    .unwrap_err();
    assert!(err.is(ErrorKind::NotFound));
}

#[derive(Debug, Default, Serialize, Deserialize)]
#[serde(default)]
struct Engine {
    engine: String,
    retries: i64,
}

#[test]
fn load_runs_migrations_on_each_file() {
    let d = tempfile::tempdir().unwrap();
    let migs = vec![Migration::new(0, 1, |mut m| {
        if let Some(v) = m.remove("model") {
            m.insert("engine".into(), v);
        }
        Ok(m)
    })];
    let mut cfg = Engine::default();
    load(
        &mut cfg,
        &Options {
            user_config_path: write(d.path(), "user.yaml", "model: m1\n"),
            project_config_path: write(d.path(), "project.yaml", "schema_version: 1\nretries: 9\n"),
            migrations: migs,
            ..Options::default()
        },
    )
    .unwrap();
    assert_eq!(cfg.engine, "m1");
    assert_eq!(cfg.retries, 9);
}
