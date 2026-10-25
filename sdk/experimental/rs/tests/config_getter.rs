//! Ports getter_test.go, list_test.go and source_test.go from
//! go/core/config.
#![cfg(feature = "config")]

use std::path::{Path, PathBuf};

use hop_top_kit::config::{
    get, list, load, load_registry, save_registry, ErrorKind, Options, Registry, Scope, Source,
    Value,
};
use serde::{Deserialize, Serialize};

fn write(dir: &Path, body: &str) -> PathBuf {
    let p = dir.join("c.yaml");
    std::fs::write(&p, body).unwrap();
    p
}

fn project(p: &Path) -> Options {
    Options {
        project_config_path: p.to_path_buf(),
        ..Options::default()
    }
}

#[test]
fn get_scalar_and_nested() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "name: myapp\ncore:\n    timeout: 30s\n");
    assert_eq!(get("name", &project(&p)).unwrap(), Value::from("myapp"));
    assert_eq!(
        get("core.timeout", &project(&p)).unwrap(),
        Value::from("30s")
    );
}

#[test]
fn get_layer_precedence() {
    let (a, b, c) = (
        tempfile::tempdir().unwrap(),
        tempfile::tempdir().unwrap(),
        tempfile::tempdir().unwrap(),
    );
    let opts = Options {
        system_config_path: write(a.path(), "name: sys\n"),
        user_config_path: write(b.path(), "name: usr\n"),
        project_config_path: write(c.path(), "name: proj\n"),
        ..Options::default()
    };
    assert_eq!(get("name", &opts).unwrap(), Value::from("proj"));
    let opts = Options {
        project_config_path: PathBuf::new(),
        ..opts
    };
    assert_eq!(get("name", &opts).unwrap(), Value::from("usr"));
}

#[test]
fn get_not_found_missing_file_and_empty_options() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "name: x\n");
    let err = get("nonexistent", &project(&p)).unwrap_err();
    assert!(err.is(ErrorKind::KeyNotFound));
    assert_eq!(err.to_string(), "config: key not found");
    assert!(get("name", &project(Path::new("/nonexistent/path/c.yaml")))
        .unwrap_err()
        .is(ErrorKind::KeyNotFound));
    assert!(get("name", &Options::default())
        .unwrap_err()
        .is(ErrorKind::KeyNotFound));
}

#[test]
fn get_sequence() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "tags:\n    - a\n    - b\n    - c\n");
    assert_eq!(
        get("tags", &project(&p)).unwrap(),
        Value::Seq(vec!["a".into(), "b".into(), "c".into()])
    );
}

#[test]
fn get_preserves_scalar_types() {
    let d = tempfile::tempdir().unwrap();
    let p = write(
        d.path(),
        "port: 8080\nratio: 0.9\ndebug: true\ndisabled: false\nunset: null\nname: myapp\nquoted: \"8080\"\nyes_key: yes\non_key: on\noff_key: off\nno_key: no\nreleased: 2024-01-01\n",
    );
    let s = |v: &str| Value::String(v.into());
    for (key, want) in [
        ("port", Value::Int(8080)),
        ("ratio", Value::Float(0.9)),
        ("debug", Value::Bool(true)),
        ("disabled", Value::Bool(false)),
        ("unset", Value::Null),
        ("name", s("myapp")),
        ("quoted", s("8080")),
        ("yes_key", s("yes")),
        ("on_key", s("on")),
        ("off_key", s("off")),
        ("no_key", s("no")),
        ("released", s("2024-01-01")),
    ] {
        assert_eq!(get(key, &project(&p)).unwrap(), want, "{key}");
    }
}

#[test]
fn get_sequence_and_nested_mapping_preserve_types() {
    let d = tempfile::tempdir().unwrap();
    let p = write(
        d.path(),
        "mixed: [1, 2.5, three, true, null]\ncore:\n  server:\n    port: 8080\n    tls: true\n",
    );
    assert_eq!(
        get("mixed", &project(&p)).unwrap(),
        Value::Seq(vec![
            Value::Int(1),
            Value::Float(2.5),
            "three".into(),
            Value::Bool(true),
            Value::Null
        ])
    );
    let core = get("core", &project(&p)).unwrap();
    assert_eq!(core.lookup("server.port"), Some(&Value::Int(8080)));
    assert_eq!(core.lookup("server.tls"), Some(&Value::Bool(true)));
    assert_eq!(
        get("core.server.port", &project(&p)).unwrap(),
        Value::Int(8080)
    );
}

#[derive(Debug, Default, Serialize, Deserialize)]
#[serde(default)]
struct Cfg {
    name: String,
    port: i64,
}

#[test]
fn load_then_get() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "name: consistent\nport: 9090\n");
    let opts = Options {
        user_config_path: p,
        ..Options::default()
    };
    let mut c = Cfg::default();
    load(&mut c, &opts).unwrap();
    assert_eq!((c.name.as_str(), c.port), ("consistent", 9090));
    assert_eq!(get("name", &opts).unwrap(), Value::from(c.name.as_str()));
}

#[test]
fn list_layers_and_scopes() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "a: 1\nb: 2\n");
    let entries = list(&project(&p)).unwrap();
    assert_eq!(
        entries.iter().map(|e| e.key.as_str()).collect::<Vec<_>>(),
        ["a", "b"]
    );

    let (s, u, pr) = (
        tempfile::tempdir().unwrap(),
        tempfile::tempdir().unwrap(),
        tempfile::tempdir().unwrap(),
    );
    let entries = list(&Options {
        system_config_path: write(s.path(), "a: sys\nx: s\n"),
        user_config_path: write(u.path(), "a: usr\nb: bval\n"),
        project_config_path: write(pr.path(), "c: p\n"),
        ..Options::default()
    })
    .unwrap();
    let get = |k: &str| entries.iter().find(|e| e.key == k).unwrap();
    assert_eq!(get("a").value, "usr");
    assert_eq!(get("a").scope, Scope::User);
    assert_eq!(get("b").value, "bval");
    assert_eq!(get("x").scope, Scope::System);
    assert_eq!(get("c").scope, Scope::Project);
}

#[test]
fn list_nested_empty_and_missing() {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), "core:\n  x: 1\n  y: 2\n");
    let keys: Vec<String> = list(&project(&p))
        .unwrap()
        .into_iter()
        .map(|e| e.key)
        .collect();
    assert_eq!(keys, ["core.x", "core.y"]);
    assert!(list(&Options::default()).unwrap().is_empty());
    assert!(list(&Options {
        system_config_path: "/nonexistent/sys.yaml".into(),
        user_config_path: "/nonexistent/usr.yaml".into(),
        ..Options::default()
    })
    .unwrap()
    .is_empty());
}

fn src(name: &str, kind: &str) -> Source {
    Source {
        name: name.into(),
        kind: kind.into(),
        ..Source::default()
    }
}

#[test]
fn registry_add_get_upsert_remove_merge() {
    let mut r = Registry::default();
    r.add(Source {
        dirs: vec!["/tmp/a".into()],
        ..src("local", "filesystem")
    });
    r.add(Source {
        endpoint: "https://x".into(),
        ..src("remote", "ctxt")
    });
    assert_eq!(r.get("local").unwrap().kind, "filesystem");
    assert_eq!(r.get("local").unwrap().dirs, ["/tmp/a"]);
    assert!(r.get("missing").is_none());

    let mut r = Registry::default();
    r.add(src("x", "filesystem"));
    r.add(Source {
        endpoint: "https://y".into(),
        ..src("x", "ctxt")
    });
    assert_eq!(r.sources.len(), 1);
    assert_eq!(r.get("x").unwrap().endpoint, "https://y");

    let mut r = Registry::default();
    r.add(src("a", ""));
    r.add(src("b", ""));
    assert!(r.remove("a"));
    assert!(!r.remove("a"));
    assert_eq!(r.names(), ["b"]);

    let mut r = Registry::default();
    for n in ["c", "a", "b"] {
        r.add(src(n, ""));
    }
    assert_eq!(r.names(), ["c", "a", "b"]);

    let mut base = Registry::default();
    base.add(src("a", "filesystem"));
    base.add(src("b", "filesystem"));
    let mut over = Registry::default();
    over.add(src("b", "ctxt"));
    over.add(src("c", "ctxt"));
    base.merge(over);
    assert_eq!(base.get("b").unwrap().kind, "ctxt");
    assert_eq!(base.names(), ["a", "b", "c"]);
}

#[test]
fn registry_round_trip_yaml() {
    let d = tempfile::tempdir().unwrap();
    let path = d.path().join("sub/registry.yaml");
    let mut r = Registry::default();
    r.add(Source {
        dirs: vec!["/x".into(), "/y".into()],
        ..src("local", "filesystem")
    });
    r.add(Source {
        endpoint: "https://api.example.com".into(),
        token: "secret".into(),
        ..src("remote", "ctxt")
    });
    save_registry(&path, &r).unwrap();
    let loaded = load_registry(&path).unwrap();
    assert_eq!(loaded, r);
}

#[test]
fn registry_load_missing_empty_and_invalid() {
    let d = tempfile::tempdir().unwrap();
    assert!(load_registry(d.path().join("absent.yaml"))
        .unwrap()
        .sources
        .is_empty());
    assert!(load_registry("").unwrap().sources.is_empty());
    let bad = d.path().join("bad.yaml");
    std::fs::write(&bad, ":\n  - oops").unwrap();
    assert!(load_registry(&bad).is_err());
}
