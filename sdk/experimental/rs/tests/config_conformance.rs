//! Runs the shared config corpus in `contracts/config-v1`. The vectors
//! are the source of truth: a failure means this port drifted from the
//! contract (Go is canonical), not that a vector should be edited.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly.
#![cfg(feature = "config")]

use std::collections::{BTreeMap, HashMap};
use std::fs;
use std::path::PathBuf;
use std::sync::Arc;

use hop_top_kit::config::{
    get, load, parse_overrides, parse_scalar, resolve_string, set, set_value, unset,
    yaml_layer_from_str, DefaultLayer, EnvLayer, FlagLayer, Layer, Options, Origin, Scope, Value,
};
use serde::{Deserialize, Serialize};
use serde_json::Value as Json;

const FILES: &[&str] = &[
    "parse_scalar.json",
    "resolve.json",
    "overrides.json",
    "precedence.json",
    "load.json",
    "edit.json",
    "paths.json",
];

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/config-v1")
}

/// Reads a vector file: the header (`$schema`, `$id`, `version` = v1,
/// `description`) is checked and stripped, the rest must decode into
/// `T` with no unknown fields.
fn read<T: serde::de::DeserializeOwned>(name: &str) -> T {
    assert!(FILES.contains(&name), "{name} not registered");
    let raw = fs::read_to_string(dir().join(name)).unwrap();
    let mut obj: serde_json::Map<String, Json> = serde_json::from_str(&raw).unwrap();
    for k in ["$schema", "$id", "description"] {
        assert!(obj.remove(k).is_some_and(|v| v.is_string()), "{name}: {k}");
    }
    assert_eq!(obj.remove("version"), Some(Json::from("v1")), "{name}");
    serde_json::from_value(Json::Object(obj)).unwrap_or_else(|e| panic!("{name}: {e}"))
}

#[test]
fn all_files_consumed() {
    let mut seen: Vec<String> = fs::read_dir(dir())
        .unwrap()
        .map(|e| e.unwrap().file_name().to_string_lossy().into_owned())
        .filter(|n| n.ends_with(".json"))
        .collect();
    seen.sort();
    let mut want: Vec<String> = FILES.iter().map(|s| s.to_string()).collect();
    want.sort();
    assert_eq!(seen, want);
}

/// The corpus encoding: `{kind, repr}`, `{seq}`, `{map}`.
fn encode(v: &Value) -> Json {
    let s = |k: &str, r: String| serde_json::json!({"kind": k, "repr": r});
    match v {
        Value::Null => s("null", "<nil>".into()),
        Value::Bool(b) => s("bool", b.to_string()),
        Value::Int(i) => s("int", i.to_string()),
        Value::Uint(u) => s("uint", u.to_string()),
        Value::Float(_) => s("float", v.to_string()),
        Value::String(x) => s("string", x.clone()),
        Value::Seq(items) => {
            serde_json::json!({"seq": items.iter().map(encode).collect::<Vec<_>>()})
        }
        Value::Map(m) => {
            let o: serde_json::Map<String, Json> =
                m.iter().map(|(k, v)| (k.clone(), encode(v))).collect();
            serde_json::json!({ "map": o })
        }
    }
}

fn decode(j: &Json) -> Value {
    let o = j.as_object().expect("corpus value is an object");
    if let Some(seq) = o.get("seq") {
        assert_eq!(o.len(), 1);
        return Value::Seq(seq.as_array().unwrap().iter().map(decode).collect());
    }
    if let Some(m) = o.get("map") {
        assert_eq!(o.len(), 1);
        return Value::Map(
            m.as_object()
                .unwrap()
                .iter()
                .map(|(k, v)| (k.clone(), decode(v)))
                .collect(),
        );
    }
    assert_eq!(o.len(), 2, "{j}");
    let repr = o["repr"].as_str().unwrap();
    match o["kind"].as_str().unwrap() {
        "null" => Value::Null,
        "bool" => Value::Bool(repr == "true"),
        "int" => Value::Int(repr.parse().unwrap()),
        "float" => Value::Float(match repr {
            "+Inf" => f64::INFINITY,
            "-Inf" => f64::NEG_INFINITY,
            "NaN" => f64::NAN,
            r => r.parse().unwrap(),
        }),
        "string" => Value::String(repr.to_string()),
        k => panic!("unknown kind {k}"),
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ScalarCase {
    name: String,
    input: String,
    want: Json,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Cases<T> {
    cases: Vec<T>,
}

#[test]
fn parse_scalar_vectors() {
    let f: Cases<ScalarCase> = read("parse_scalar.json");
    assert!(!f.cases.is_empty());
    for c in f.cases {
        assert_eq!(encode(&parse_scalar(&c.input)), c.want, "{}", c.name);
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ResolveCase {
    name: String,
    yaml: String,
    want: Json,
}

#[test]
fn resolve_vectors() {
    let f: Cases<ResolveCase> = read("resolve.json");
    let d = tempfile::tempdir().unwrap();
    for (i, c) in f.cases.iter().enumerate() {
        let p = d.path().join(format!("c{i}.yaml"));
        fs::write(&p, format!("v: {}", c.yaml)).unwrap();
        let opts = Options {
            project_config_path: p,
            ..Options::default()
        };
        let got = get("v", &opts).unwrap_or_else(|e| panic!("{}: {e}", c.name));
        assert_eq!(encode(&got), c.want, "{}", c.name);
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct OverrideCase {
    name: String,
    pairs: Vec<String>,
    ok: bool,
    #[serde(default)]
    want: Option<Json>,
    #[serde(default)]
    error: Option<String>,
}

#[test]
fn overrides_vectors() {
    let f: Cases<OverrideCase> = read("overrides.json");
    for c in f.cases {
        let got = parse_overrides(&c.pairs);
        if !c.ok {
            let want = c.error.expect("failing case has an error");
            assert_eq!(got.unwrap_err().to_string(), want, "{}", c.name);
            continue;
        }
        let got = Value::Map(got.unwrap().unwrap_or_default());
        assert_eq!(encode(&got), c.want.unwrap(), "{}", c.name);
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct LayerSpec {
    #[serde(rename = "type")]
    kind: String,
    #[serde(default)]
    flags: HashMap<String, String>,
    #[serde(default)]
    changed: Option<HashMap<String, bool>>,
    #[serde(default)]
    prefix: String,
    #[serde(default)]
    env: HashMap<String, String>,
    #[serde(default)]
    origin: String,
    #[serde(default)]
    yaml: String,
    #[serde(default)]
    path: String,
    #[serde(default)]
    defaults: HashMap<String, String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WantResolved {
    value: String,
    origin: String,
    detail: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PrecCase {
    name: String,
    key: String,
    layers: Vec<LayerSpec>,
    want: WantResolved,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EnvName {
    prefix: String,
    key: String,
    env: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PrecFile {
    env_names: Vec<EnvName>,
    cases: Vec<PrecCase>,
}

fn origin(s: &str) -> Origin {
    match s {
        "default" => Origin::Default,
        "global" => Origin::Global,
        "project" => Origin::Project,
        "env" => Origin::Env,
        "flag" => Origin::Flag,
        o => panic!("origin {o}"),
    }
}

#[test]
fn precedence_vectors() {
    let f: PrecFile = read("precedence.json");
    for e in f.env_names {
        assert_eq!(EnvLayer::new(e.prefix).env_name(&e.key), e.env);
    }
    for c in f.cases {
        let layers: Vec<Box<dyn Layer>> = c
            .layers
            .into_iter()
            .map(|l| -> Box<dyn Layer> {
                match l.kind.as_str() {
                    "flag" => Box::new(FlagLayer {
                        flags: l.flags,
                        changed: l.changed,
                    }),
                    "env" => {
                        let env = l.env;
                        Box::new(EnvLayer::with_env(
                            l.prefix,
                            Arc::new(move |k| env.get(k).cloned()),
                        ))
                    }
                    "yaml" => {
                        Box::new(yaml_layer_from_str(&l.yaml, origin(&l.origin), &l.path).unwrap())
                    }
                    "default" => Box::new(DefaultLayer {
                        defaults: l.defaults,
                    }),
                    t => panic!("layer {t}"),
                }
            })
            .collect();
        let refs: Vec<&dyn Layer> = layers.iter().map(|b| b.as_ref()).collect();
        let got = resolve_string(&c.key, &refs);
        assert_eq!(
            (
                got.value.as_str(),
                got.origin.to_string(),
                got.detail.as_str()
            ),
            (
                c.want.value.as_str(),
                c.want.origin.clone(),
                c.want.detail.as_str()
            ),
            "{}",
            c.name
        );
    }
}

#[derive(Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
struct Inner {
    a: String,
    b: i64,
}

#[derive(Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
struct Shape {
    name: String,
    port: i64,
    ratio: f64,
    debug: bool,
    tags: Option<Vec<String>>,
    inner: Inner,
    labels: Option<BTreeMap<String, String>>,
    opt: Option<String>,
    extra: Option<Value>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct LoadCase {
    name: String,
    #[serde(default)]
    start: Option<Json>,
    files: Vec<String>,
    #[serde(default)]
    generate: Option<Generate>,
    #[serde(default)]
    overrides: Vec<String>,
    ok: bool,
    #[serde(default)]
    want: Option<Json>,
    #[serde(default)]
    error_contains: Vec<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Generate {
    prefix: String,
    line: String,
    count: usize,
    #[serde(default)]
    suffix: String,
}

#[test]
fn load_vectors() {
    let f: Cases<LoadCase> = read("load.json");
    for c in f.cases {
        let d = tempfile::tempdir().unwrap();
        let mut cfg: Shape = match c.start {
            Some(s) => serde_json::from_value(s).unwrap(),
            None => Shape::default(),
        };
        let mut opts = Options::default();
        let mut files = c.files.clone();
        if let Some(g) = &c.generate {
            let mut b = g.prefix.clone();
            for i in 0..g.count {
                b.push_str(&g.line.replace("{i}", &i.to_string()));
            }
            b.push_str(&g.suffix);
            files.push(b);
        }
        for (i, body) in files.iter().enumerate() {
            let p = d.path().join(format!("f{i}.yaml"));
            fs::write(&p, body).unwrap();
            opts.extra_config_paths.push(p);
        }
        if !c.overrides.is_empty() {
            opts.overrides = parse_overrides(&c.overrides).unwrap();
        }
        let res = load(&mut cfg, &opts);
        if !c.ok {
            let mut msg = res.expect_err(&c.name).to_string();
            for p in &opts.extra_config_paths {
                msg = msg.replace(&p.display().to_string(), "<path>");
            }
            for s in &c.error_contains {
                assert!(msg.contains(s), "{}: {msg:?} lacks {s:?}", c.name);
            }
            continue;
        }
        res.unwrap_or_else(|e| panic!("{}: {e}", c.name));
        let got = numbers_as_f64(serde_json::to_value(&cfg).unwrap());
        assert_eq!(got, numbers_as_f64(c.want.unwrap()), "{}", c.name);
    }
}

/// JSON numbers compare by value (Go writes `0`, serde `0.0`).
fn numbers_as_f64(j: Json) -> Json {
    match j {
        Json::Number(n) => serde_json::json!(n.as_f64().unwrap()),
        Json::Array(a) => Json::Array(a.into_iter().map(numbers_as_f64).collect()),
        Json::Object(o) => {
            Json::Object(o.into_iter().map(|(k, v)| (k, numbers_as_f64(v))).collect())
        }
        other => other,
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EditCase {
    name: String,
    original: String,
    op: String,
    key: String,
    #[serde(default)]
    value: Option<Json>,
    ok: bool,
    #[serde(default)]
    want: Option<Json>,
    #[serde(default)]
    keeps: Vec<String>,
    #[serde(default)]
    error: Option<String>,
}

#[test]
fn edit_vectors() {
    let f: Cases<EditCase> = read("edit.json");
    for c in f.cases {
        let d = tempfile::tempdir().unwrap();
        let path = d.path().join("config.yaml");
        if !c.original.is_empty() {
            fs::write(&path, &c.original).unwrap();
        }
        let opts = Options {
            project_config_path: path.clone(),
            ..Options::default()
        };
        let res = match c.op.as_str() {
            "set" => set(
                &c.key,
                c.value.as_ref().unwrap().as_str().unwrap(),
                Scope::Project,
                &opts,
            ),
            "set_value" => set_value(
                &c.key,
                &decode(c.value.as_ref().unwrap()),
                Scope::Project,
                &opts,
            ),
            "unset" => unset(&c.key, Scope::Project, &opts),
            op => panic!("op {op}"),
        };
        if !c.ok {
            assert_eq!(res.unwrap_err().to_string(), c.error.unwrap(), "{}", c.name);
            continue;
        }
        res.unwrap_or_else(|e| panic!("{}: {e}", c.name));
        let text = fs::read_to_string(&path).unwrap();
        // The whole document decoded (aliases resolve), as Go's runner.
        let got = yaml_layer_from_str(&text, Origin::Project, "")
            .unwrap_or_else(|e| panic!("{}: written file does not parse: {e}\n{text}", c.name))
            .data;
        assert_eq!(
            encode(&Value::Map(got)),
            c.want.unwrap(),
            "{}:\n{text}",
            c.name
        );
        for k in &c.keeps {
            assert!(text.contains(k.as_str()), "{}: lost {k:?}:\n{text}", c.name);
        }
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PathCase {
    name: String,
    layout: Vec<String>,
    #[serde(default)]
    arg: String,
    #[serde(default)]
    start: String,
    marker: String,
    #[serde(default)]
    ok: Option<bool>,
    #[serde(default)]
    want: String,
    #[serde(default)]
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PathsFile {
    config_args: Vec<PathCase>,
    nearest: Vec<PathCase>,
}

fn layout(files: &[String]) -> (tempfile::TempDir, String) {
    let d = tempfile::tempdir().unwrap();
    for f in files {
        let p = d.path().join(f);
        fs::create_dir_all(p.parent().unwrap()).unwrap();
        fs::write(&p, "").unwrap();
    }
    let root = d.path().display().to_string();
    (d, root)
}

#[cfg(unix)]
#[test]
fn paths_vectors() {
    use hop_top_kit::config::{nearest_with_depth, parse_config_args, ParseOption};
    let f: PathsFile = read("paths.json");
    for c in f.config_args {
        let (_d, root) = layout(&c.layout);
        let sub = |s: &str| s.replace("<root>", &root);
        let got = parse_config_args(
            &[sub(&c.arg)],
            &[ParseOption::ProjectMarker(c.marker.clone())],
        );
        if c.ok != Some(true) {
            assert_eq!(got.unwrap_err().to_string(), sub(&c.error), "{}", c.name);
            continue;
        }
        let (paths, _) = got.unwrap_or_else(|e| panic!("{}: {e}", c.name));
        assert_eq!(paths, [PathBuf::from(sub(&c.want))], "{}", c.name);
    }
    for c in f.nearest {
        let (_d, root) = layout(&c.layout);
        let sub = |s: &str| s.replace("<root>", &root);
        let got = nearest_with_depth(sub(&c.start), &c.marker, 0)
            .unwrap_or_else(|e| panic!("{}: {e}", c.name));
        assert_eq!(got, PathBuf::from(sub(&c.want)), "{}", c.name);
    }
}
