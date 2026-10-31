//! Runs the shared completion corpus in `contracts/completion-v1`. The
//! vectors are the source of truth: a failure means this port drifted
//! from the contract (Go is canonical), not that a vector should be
//! edited to fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, the corpus
//! directory must match [`FILES`] exactly and every golden script must
//! be claimed by a case.

#![cfg(all(feature = "cli", feature = "config"))]

use std::collections::BTreeMap;
use std::fs;
use std::path::PathBuf;

use hop_top_kit::cli::completion::{
    self, bind_args, bind_flag, config_keys, dir, file, func, prefixed, static_items,
    static_values, Completer, Context, Item,
};
use hop_top_kit::cli::{CommandSpec, Config, FlagSpec, Root};
use hop_top_kit::config::Value;
use serde::Deserialize;

const FILES: &[&str] = &["completers.json", "requests.json", "scripts.json"];

fn corpus() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/completion-v1")
}

fn load<T: for<'de> Deserialize<'de>>(name: &str) -> T {
    let dir = corpus();
    for e in fs::read_dir(&dir).unwrap() {
        let n = e.unwrap().file_name().into_string().unwrap();
        if n.ends_with(".json") {
            assert!(FILES.contains(&n.as_str()), "{n} has no runner");
        }
    }
    let raw = fs::read_to_string(dir.join(name)).unwrap();
    serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {name}: {e}"))
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
struct ConfItem {
    value: String,
    #[serde(default)]
    description: String,
}

impl ConfItem {
    fn item(&self) -> Item {
        Item::with_description(&self.value, &self.description)
    }
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
struct ConfFunc {
    items: Option<Vec<ConfItem>>,
    error: Option<String>,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
struct ConfPrefixed {
    dimension: String,
    values: Box<Spec>,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
struct Empty {}

/// A one-key completer spec.
#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
struct Spec {
    #[serde(rename = "static")]
    static_: Option<Vec<ConfItem>>,
    static_values: Option<Vec<String>>,
    func: Option<ConfFunc>,
    prefixed: Option<ConfPrefixed>,
    config_keys: Option<Value>,
    file: Option<Vec<String>>,
    dir: Option<Empty>,
}

fn build(s: &Spec) -> Box<dyn Completer> {
    let mut out: Vec<Box<dyn Completer>> = Vec::new();
    if let Some(items) = &s.static_ {
        out.push(Box::new(static_items(items.iter().map(ConfItem::item))));
    }
    if let Some(v) = &s.static_values {
        out.push(Box::new(static_values(v.clone())));
    }
    if let Some(f) = &s.func {
        assert!(
            f.items.is_some() != f.error.is_some(),
            "func spec needs exactly one of items, error"
        );
        let f = f.clone();
        out.push(Box::new(func(move |_: &Context, _: &str| match &f.error {
            Some(e) => Err(e.clone().into()),
            None => Ok(f.items.iter().flatten().map(ConfItem::item).collect()),
        })));
    }
    if let Some(p) = &s.prefixed {
        out.push(Box::new(prefixed(&p.dimension, build(&p.values))));
    }
    if let Some(v) = &s.config_keys {
        out.push(Box::new(config_keys(v.clone())));
    }
    if let Some(exts) = &s.file {
        out.push(Box::new(file(exts.clone())));
    }
    if s.dir.is_some() {
        out.push(Box::new(dir()));
    }
    assert_eq!(out.len(), 1, "completer spec needs exactly one key");
    out.pop().unwrap()
}

fn same(mut got: Vec<String>, mut want: Vec<String>, unordered: bool) -> bool {
    if unordered {
        got.sort();
        want.sort();
    }
    got == want
}

// --- completers.json ---

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Resolved {
    completions: Vec<String>,
    directive: u32,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CompleterCase {
    name: String,
    completer: Spec,
    prefix: String,
    items: Option<Vec<ConfItem>>,
    #[serde(default)]
    error: bool,
    #[serde(default)]
    unordered: bool,
    resolved: Resolved,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CompletersFile {
    #[serde(rename = "$schema")]
    #[allow(dead_code)]
    schema: String,
    #[serde(rename = "$id")]
    #[allow(dead_code)]
    id: String,
    version: String,
    #[allow(dead_code)]
    description: String,
    cases: Vec<CompleterCase>,
}

#[test]
fn completers() {
    let f: CompletersFile = load("completers.json");
    assert_eq!(f.version, "v1");
    assert!(!f.cases.is_empty(), "cases section is empty");
    let mut failures = Vec::new();
    for c in &f.cases {
        let comp = build(&c.completer);
        let ctx = Context::default();
        match (comp.complete(&ctx, &c.prefix), c.error) {
            (Ok(items), false) => {
                let want = c.items.as_ref().expect("a succeeding case needs items");
                let got: Vec<String> = items
                    .iter()
                    .map(|i| format!("{}|{}", i.value, i.description))
                    .collect();
                let want: Vec<String> = want
                    .iter()
                    .map(|i| format!("{}|{}", i.value, i.description))
                    .collect();
                if !same(got.clone(), want.clone(), c.unordered) {
                    failures.push(format!("{}: items {got:?}, want {want:?}", c.name));
                }
            }
            (Err(_), true) => assert!(
                c.items.is_none(),
                "{}: a failing case carries no items",
                c.name
            ),
            (Ok(items), true) => failures.push(format!("{}: {items:?}, want an error", c.name)),
            (Err(e), false) => failures.push(format!("{}: unexpected error {e}", c.name)),
        }
        let (comps, d) = completion::resolve(&ctx, comp.as_ref(), &c.prefix);
        if !same(comps.clone(), c.resolved.completions.clone(), c.unordered) {
            failures.push(format!(
                "{}: resolved {comps:?}, want {:?}",
                c.name, c.resolved.completions
            ));
        }
        if d.bits() != c.resolved.directive {
            failures.push(format!(
                "{}: directive {}, want {}",
                c.name,
                d.bits(),
                c.resolved.directive
            ));
        }
    }
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}

// --- requests.json ---

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfFlag {
    long: String,
    #[serde(default)]
    short: Option<String>,
    help: String,
    value: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfCommand {
    name: String,
    about: String,
    #[serde(default)]
    flags: Vec<ConfFlag>,
    #[serde(default)]
    commands: Vec<ConfCommand>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct FlagEnum {
    path: String,
    flag: String,
    values: Vec<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct FlagCompleter {
    path: String,
    flag: String,
    completer: Spec,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ArgCompleter {
    path: String,
    completer: Spec,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Tree {
    program: String,
    about: String,
    commands: Vec<ConfCommand>,
    aliases: BTreeMap<String, String>,
    flag_enums: Vec<FlagEnum>,
    flag_completers: Vec<FlagCompleter>,
    arg_completers: Vec<ArgCompleter>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RequestCase {
    name: String,
    tree: String,
    args: Vec<String>,
    #[serde(default)]
    no_desc: bool,
    #[serde(default)]
    unordered: bool,
    stdout: Vec<String>,
    #[serde(default)]
    stderr_contains: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RequestsFile {
    #[serde(rename = "$schema")]
    #[allow(dead_code)]
    schema: String,
    #[serde(rename = "$id")]
    #[allow(dead_code)]
    id: String,
    version: String,
    #[allow(dead_code)]
    description: String,
    trees: BTreeMap<String, Tree>,
    cases: Vec<RequestCase>,
}

fn build_command(c: &ConfCommand) -> CommandSpec {
    let mut cmd = CommandSpec::new(c.name.clone()).about(c.about.clone());
    for f in &c.flags {
        let mut a = if f.value {
            FlagSpec::string(f.long.clone())
        } else {
            FlagSpec::bool(f.long.clone())
        }
        .help(f.help.clone());
        if let Some(s) = &f.short {
            a = a.short(s.chars().next().unwrap());
        }
        cmd = cmd.flag(a);
    }
    for child in &c.commands {
        cmd = cmd.subcommand(build_command(child));
    }
    cmd
}

fn build_root(t: &Tree) -> Root {
    let mut root = Root::with_config(Config::new(&t.program, "1.0.0", &t.about));
    for c in &t.commands {
        root.add_command(build_command(c));
    }
    for (name, path) in &t.aliases {
        root.alias(name, path).unwrap();
    }
    for e in &t.flag_enums {
        if e.path.is_empty() {
            root.with_flag_enum(&e.flag, e.values.clone());
        } else {
            root.with_command_flag_enum(&e.path, &e.flag, e.values.clone());
        }
    }
    for fc in &t.flag_completers {
        bind_flag(&mut root, &fc.path, &fc.flag, build(&fc.completer));
    }
    for ac in &t.arg_completers {
        bind_args(&mut root, &ac.path, build(&ac.completer));
    }
    root
}

#[test]
fn requests() {
    let f: RequestsFile = load("requests.json");
    assert_eq!(f.version, "v1");
    assert!(!f.cases.is_empty(), "cases section is empty");
    assert!(!f.trees.is_empty(), "trees section is empty");
    let roots: BTreeMap<&String, Root> = f.trees.iter().map(|(n, t)| (n, build_root(t))).collect();
    let mut used = std::collections::BTreeSet::new();
    let mut failures = Vec::new();
    for c in &f.cases {
        let Some(root) = roots.get(&c.tree) else {
            failures.push(format!("{}: no tree {:?}", c.name, c.tree));
            continue;
        };
        used.insert(&c.tree);
        let (mut out, mut err) = (Vec::new(), Vec::new());
        root.complete(&c.args, c.no_desc, &mut out, &mut err)
            .unwrap();
        let out = String::from_utf8(out).unwrap();
        let mut got: Vec<String> = out
            .strip_suffix('\n')
            .unwrap_or(&out)
            .split('\n')
            .map(String::from)
            .collect();
        let mut want = c.stdout.clone();
        let (gl, wl) = (got.pop(), want.pop());
        if gl != wl {
            failures.push(format!("{}: directive line {gl:?}, want {wl:?}", c.name));
        }
        if !same(got.clone(), want.clone(), c.unordered) {
            failures.push(format!("{}: completions {got:?}, want {want:?}", c.name));
        }
        let err = String::from_utf8(err).unwrap();
        if let Some(needle) = &c.stderr_contains {
            if !err.contains(needle.as_str()) {
                failures.push(format!("{}: stderr {err:?} lacks {needle:?}", c.name));
            }
        }
    }
    for name in f.trees.keys() {
        if !used.contains(name) {
            failures.push(format!("tree {name:?} has no case"));
        }
    }
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}

// --- scripts.json ---

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ScriptCase {
    name: String,
    program: String,
    shell: String,
    no_descriptions: bool,
    golden: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ScriptsFile {
    #[serde(rename = "$schema")]
    #[allow(dead_code)]
    schema: String,
    #[serde(rename = "$id")]
    #[allow(dead_code)]
    id: String,
    version: String,
    #[allow(dead_code)]
    description: String,
    cases: Vec<ScriptCase>,
}

#[test]
fn scripts() {
    let f: ScriptsFile = load("scripts.json");
    assert_eq!(f.version, "v1");
    assert!(!f.cases.is_empty(), "cases section is empty");
    let mut goldens: BTreeMap<String, bool> = fs::read_dir(corpus().join("scripts"))
        .unwrap()
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .filter(|n| n != "README.md")
        .map(|n| (format!("scripts/{n}"), false))
        .collect();
    let mut failures = Vec::new();
    for c in &f.cases {
        let Some(used) = goldens.get_mut(&c.golden) else {
            failures.push(format!("{}: golden {} is missing", c.name, c.golden));
            continue;
        };
        *used = true;
        let want = fs::read_to_string(corpus().join(&c.golden)).unwrap();
        let root = Root::new(
            CommandSpec::new(c.program.clone())
                .about("Kit app")
                .subcommand(CommandSpec::new("sub").about("A subcommand")),
        );
        let mut argv = vec![c.program.as_str(), "completion", c.shell.as_str()];
        if c.no_descriptions {
            argv.push("--no-descriptions");
        }
        let (mut out, mut err) = (Vec::new(), Vec::new());
        root.try_builtin(argv, &mut out, &mut err)
            .expect("completion is a builtin")
            .unwrap();
        let got = String::from_utf8(out).unwrap();
        if got != want {
            let line = got
                .lines()
                .zip(want.lines())
                .position(|(g, w)| g != w)
                .map(|i| format!("; first difference at line {}", i + 1))
                .unwrap_or_default();
            failures.push(format!(
                "{}: script differs from {} ({} bytes, want {}){line}",
                c.name,
                c.golden,
                got.len(),
                want.len()
            ));
        }
    }
    for (g, used) in goldens {
        if !used {
            failures.push(format!("{g} has no case"));
        }
    }
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}
