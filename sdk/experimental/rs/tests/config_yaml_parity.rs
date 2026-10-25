//! Parser and decoder parity with Go's yaml.v3 on hostile or unusual
//! input: alias bombs, BOM, tab separators, under-indented quoted
//! continuation, duplicate and non-scalar keys; and editor refusals.
#![cfg(feature = "config")]

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use hop_top_kit::config::{load, set, unset, ErrorKind, Options, Scope, Value};
use serde::{Deserialize, Serialize};

#[derive(Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
struct Shape {
    name: String,
    port: i64,
    tags: Option<Vec<String>>,
    labels: Option<BTreeMap<String, String>>,
    extra: Option<Value>,
}

fn write(dir: &Path, body: &str) -> PathBuf {
    let p = dir.join("c.yaml");
    std::fs::write(&p, body).unwrap();
    p
}

fn load_str(body: &str) -> Result<Shape, String> {
    let d = tempfile::tempdir().unwrap();
    let p = write(d.path(), body);
    let mut s = Shape::default();
    let opts = Options {
        extra_config_paths: vec![p.clone()],
        ..Options::default()
    };
    load(&mut s, &opts)
        .map(|()| s)
        .map_err(|e| e.to_string().replace(&p.display().to_string(), "<path>"))
}

/// `a0` is nine strings; each `aN` is nine aliases of `aN-1`.
fn laughs(depth: usize) -> String {
    let mut s = String::from("a0: &a0 [lol, lol, lol, lol, lol, lol, lol, lol, lol]\n");
    for i in 1..=depth {
        let refs = vec![format!("*a{}", i - 1); 9].join(", ");
        s.push_str(&format!("a{i}: &a{i} [{refs}]\n"));
    }
    s
}

#[test]
fn billion_laughs_fails_fast() {
    // 9^13 (2.5e12) nodes expanded; bounded only if aliases stay
    // references and the decode budget stops early.
    let body = format!("{}extra: *a12\n", laughs(12));
    let start = Instant::now();
    let err = load_str(&body).unwrap_err();
    assert_eq!(
        err,
        "load config <path>: yaml: document contains excessive aliasing"
    );
    assert!(
        start.elapsed() < Duration::from_secs(2),
        "{:?}",
        start.elapsed()
    );
    // Unread anchors cost nothing, as in Go (unknown keys are skipped).
    assert_eq!(load_str(&laughs(12)).unwrap(), Shape::default());
    // Small fan-out passes.
    let ok = load_str("a: &x [1, 2]\nextra: *x\n").unwrap();
    assert_eq!(
        ok.extra,
        Some(Value::Seq(vec![Value::Int(1), Value::Int(2)]))
    );
}

#[test]
fn readable_like_libyaml() {
    assert_eq!(load_str("\u{feff}name: x\n").unwrap().name, "x");
    assert_eq!(load_str("name:\tx\n").unwrap().name, "x");
    assert_eq!(load_str("name: \"x\ny\"\n").unwrap().name, "x y");
    assert_eq!(load_str("name: 'x\ny'\n").unwrap().name, "x y");
    let s = load_str("labels:\n  k: \"x\ny\"\n").unwrap();
    assert_eq!(s.labels.unwrap()["k"], "x y");
}

#[test]
fn duplicate_and_complex_keys_fail_per_layer() {
    assert_eq!(
        load_str("port: 1\nport: 2\n").unwrap_err(),
        "load config <path>: yaml: unmarshal errors:\n  line 2: mapping key \"port\" already defined at line 1"
    );
    assert_eq!(
        load_str("labels:\n  a: x\n  a: y\n").unwrap_err(),
        "load config <path>: yaml: unmarshal errors:\n  line 3: mapping key \"a\" already defined at line 2"
    );
    // Inside a key the target does not read: not decoded, not checked.
    assert!(load_str("zz:\n  a: 1\n  a: 2\n").is_ok());
    assert_eq!(
        load_str("? [a]\n: 1\n").unwrap_err(),
        "load config <path>: yaml: unmarshal errors:\n  line 1: cannot unmarshal !!seq into string"
    );
    assert_eq!(
        load_str("labels:\n  ? [a]\n  : x\n").unwrap_err(),
        "load config <path>: yaml: unmarshal errors:\n  line 2: cannot unmarshal !!seq into string"
    );
}

#[test]
fn refused_edits_leave_the_file_untouched() {
    let d = tempfile::tempdir().unwrap();
    let body = "? k\n: v # keep me\nz: 1\n";
    let p = write(d.path(), body);
    let opts = Options {
        project_config_path: p.clone(),
        ..Options::default()
    };
    let err = set("k", "x", Scope::Project, &opts).unwrap_err();
    assert!(err.is(ErrorKind::EditUnsupported), "{err}");
    assert!(err.to_string().starts_with("config set: "), "{err}");
    let err = unset("k", Scope::Project, &opts).unwrap_err();
    assert!(err.is(ErrorKind::EditUnsupported), "{err}");
    assert_eq!(std::fs::read_to_string(&p).unwrap(), body);
}

#[test]
fn many_repairs_load_fast() {
    // 20k tab separators and 20k under-indented continuations: one
    // repair pass, a few parses, linear merge.
    let mut body = String::from("name:\tx\n");
    for i in 0..20_000 {
        body.push_str(&format!("k{i}:\tv\nq{i}: \"a\nb\"\n"));
    }
    let start = Instant::now();
    let s = load_str(&body).unwrap();
    let took = start.elapsed();
    eprintln!("40k-line repaired load: {took:?}");
    assert_eq!(s.name, "x");
    // Generous for unoptimized builds; release is far below a second.
    assert!(took < Duration::from_secs(5), "{took:?}");
}

#[test]
fn edits_on_large_files_are_fast() {
    let d = tempfile::tempdir().unwrap();
    let mut body = String::new();
    for i in 0..20_000 {
        body.push_str(&format!("k{i}: &a{i} v # c{i}\n"));
    }
    let p = write(d.path(), &body);
    let opts = Options {
        project_config_path: p.clone(),
        ..Options::default()
    };
    let start = Instant::now();
    set("k19999", "w", Scope::Project, &opts).unwrap();
    unset("k0", Scope::Project, &opts).unwrap();
    let took = start.elapsed();
    eprintln!("20k-line set+unset: {took:?}");
    assert!(took < Duration::from_secs(5), "{took:?}");
    let text = std::fs::read_to_string(&p).unwrap();
    assert!(
        text.ends_with("k19999: &a19999 w # c19999\n"),
        "{}",
        &text[text.len() - 40..]
    );
    assert!(text.starts_with("k1: &a1 v # c1\n"));
}
