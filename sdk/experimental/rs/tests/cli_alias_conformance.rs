//! Runs the shared alias corpus in `contracts/alias-v1`. The vectors are
//! the source of truth: a failure means this port drifted from the
//! contract, not that a vector should be edited to fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly.
//!
//! Rust has no dispatch hook: the caller runs an alias proxy through
//! `Root::alias_target`. This runner is that caller: it re-parses the
//! target path with the proxy's arguments, as an adopter's `main` does.

#![cfg(feature = "cli")]

use std::collections::BTreeMap;
use std::fs;
use std::path::PathBuf;

use clap::error::ErrorKind;
use clap::{Arg, ArgMatches, Command};
use hop_top_kit::cli::{
    detect_cycles, validate_alias_name, AliasHelp, AliasNameRules, AliasOptions, Root, SHIM_ARGS,
};
use serde::Deserialize;

const PORT: &str = "rs";
const FILES: &[&str] = &["bridge.json", "cycles.json", "validate.json"];

fn corpus() -> PathBuf {
    let dir = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/alias-v1");
    let mut seen: Vec<String> = fs::read_dir(&dir)
        .unwrap()
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .filter(|n| n.ends_with(".json"))
        .collect();
    seen.sort();
    assert_eq!(seen, FILES, "corpus files and runner registry differ");
    dir
}

fn load<T: for<'de> Deserialize<'de>>(name: &str) -> T {
    let raw = fs::read_to_string(corpus().join(name)).unwrap();
    serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {name}: {e}"))
}

// --- validate.json ---

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ValidateFile {
    version: String,
    #[allow(dead_code)]
    description: String,
    cases: Vec<ValidateCase>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ValidateCase {
    name: String,
    alias: String,
    #[serde(default)]
    builtins: Vec<String>,
    existing: Option<BTreeMap<String, String>>,
    expect: String,
}

#[test]
fn validate() {
    let f: ValidateFile = load("validate.json");
    assert_eq!(f.version, "v1");
    assert!(!f.cases.is_empty());
    for c in f.cases {
        let builtins: Vec<&str> = c.builtins.iter().map(String::as_str).collect();
        let rules = AliasNameRules {
            builtins: &builtins,
            existing: c.existing.as_ref(),
        };
        let got = match validate_alias_name(&c.alias, &rules) {
            Ok(()) => "ok",
            Err(e) => e.kind.as_str(),
        };
        assert_eq!(got, c.expect, "{}", c.name);
    }
}

// --- cycles.json ---

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CyclesFile {
    version: String,
    #[allow(dead_code)]
    description: String,
    cases: Vec<CyclesCase>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CyclesCase {
    name: String,
    aliases: BTreeMap<String, String>,
    cycles: Vec<Vec<String>>,
}

#[test]
fn cycles() {
    let f: CyclesFile = load("cycles.json");
    assert_eq!(f.version, "v1");
    assert!(!f.cases.is_empty());
    for c in f.cases {
        assert_eq!(detect_cycles(&c.aliases), c.cycles, "{}", c.name);
    }
}

// --- bridge.json ---

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct BridgeFile {
    version: String,
    #[allow(dead_code)]
    description: String,
    tree: Node,
    describe_template: String,
    cases: Vec<BridgeCase>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Node {
    name: String,
    #[serde(default)]
    description: String,
    #[serde(default)]
    flags: Vec<Flag>,
    #[serde(default)]
    commands: Vec<Node>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Flag {
    name: String,
    default: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Options {
    hidden: Option<bool>,
    #[serde(default)]
    describe: bool,
    help: Option<String>,
    dispatch: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct BridgeCase {
    name: String,
    aliases: BTreeMap<String, String>,
    options: Option<Options>,
    #[serde(default)]
    skip: BTreeMap<String, String>,
    #[serde(default)]
    proxy_flags: Vec<ProxyFlag>,
    checks: Vec<Check>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ProxyFlag {
    #[allow(dead_code)]
    name: String,
    #[allow(dead_code)]
    short: String,
}

#[derive(Clone, Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct Check {
    alias: Option<String>,
    argv: Option<Vec<String>>,
    entry: Option<Entry>,
    run: Option<Run>,
    help: Option<String>,
    complete: Option<Vec<String>>,
    candidates: Option<Vec<String>>,
    #[serde(default)]
    by_port: BTreeMap<String, Check>,
}

impl Check {
    fn for_port(&self, port: &str) -> Check {
        let mut c = self.clone();
        if let Some(o) = self.by_port.get(port) {
            if o.entry.is_some() {
                c.entry = o.entry.clone();
            }
            if o.run.is_some() {
                c.run = o.run.clone();
            }
            if o.help.is_some() {
                c.help = o.help.clone();
            }
        }
        c
    }
}

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
struct Entry {
    kind: String,
    hidden: Option<bool>,
    description: Option<String>,
}

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
struct Run {
    ran: String,
    path: Option<String>,
    #[serde(default)]
    env: Option<String>,
    args: Option<Vec<String>>,
    #[allow(dead_code)]
    name: Option<String>,
    #[allow(dead_code)]
    target: Option<String>,
}

fn build(n: &Node) -> Command {
    let mut c = Command::new(n.name.clone()).about(n.description.clone());
    for f in &n.flags {
        c = c.arg(
            Arg::new(f.name.clone())
                .long(f.name.clone())
                .default_value(f.default.clone()),
        );
    }
    if n.commands.is_empty() {
        c = c.arg(Arg::new("args").num_args(0..));
    }
    for sub in &n.commands {
        c = c.subcommand(build(sub));
    }
    c
}

fn descriptions(n: &Node, prefix: &str, out: &mut BTreeMap<String, String>) {
    for sub in &n.commands {
        let path = format!("{prefix} {}", sub.name).trim().to_string();
        out.insert(path.clone(), sub.description.clone());
        descriptions(sub, &path, out);
    }
}

fn setup(f: &BridgeFile, c: &BridgeCase) -> Root {
    assert!(
        c.proxy_flags.is_empty(),
        "{}: rust cannot add flags to a proxy",
        c.name
    );
    let mut root = Root::new(build(&f.tree));
    let opts = c.options.as_ref().map(|o| {
        assert!(
            o.dispatch.is_none(),
            "{}: rust runs no dispatch option",
            c.name
        );
        let mut opts = AliasOptions::new();
        if let Some(h) = o.hidden {
            opts = opts.hidden(h);
        }
        if o.describe {
            let tmpl = f.describe_template.clone();
            opts = opts.describe(move |name, target| {
                tmpl.replace("{name}", name).replace("{target}", target)
            });
        }
        match o.help.as_deref() {
            Some("own") => opts = opts.help(AliasHelp::Own),
            Some("passthrough") => opts = opts.help(AliasHelp::Passthrough),
            None => {}
            Some(h) => panic!("unknown help {h}"),
        }
        opts
    });
    for (name, target) in &c.aliases {
        match &opts {
            Some(o) => root.alias_with(name, target, o),
            None => root.alias(name, target),
        }
        .unwrap();
    }
    root
}

/// Parses argv the way an adopter's main does: an alias proxy is
/// dispatched by re-parsing its target path with the proxy's arguments.
fn parse(root: &Root, argv: &[String]) -> Result<ArgMatches, clap::Error> {
    let name = root.command().get_name().to_string();
    let m = root
        .command()
        .try_get_matches_from(std::iter::once(name.clone()).chain(argv.iter().cloned()))?;
    if let Some((sub, sm)) = m.subcommand() {
        // A native clap alias matches as its target's name, never as an
        // alias key, so a key here is a proxy.
        if let Some(target) = root.alias_target(sub) {
            let rest: Vec<String> = sm
                .get_many::<String>(SHIM_ARGS)
                .map(|v| v.cloned().collect())
                .unwrap_or_default();
            return root.command().try_get_matches_from(
                std::iter::once(name)
                    .chain(target.split_whitespace().map(String::from))
                    .chain(rest),
            );
        }
    }
    Ok(m)
}

#[test]
fn bridge() {
    let f: BridgeFile = load("bridge.json");
    assert_eq!(f.version, "v1");
    assert!(!f.cases.is_empty());
    let mut descs = BTreeMap::new();
    descriptions(&f.tree, "", &mut descs);

    for c in &f.cases {
        if let Some(why) = c.skip.get(PORT) {
            eprintln!("skip {}: {why}", c.name);
            continue;
        }
        for raw in &c.checks {
            let chk = raw.for_port(PORT);
            let root = setup(&f, c);
            if let Some(want) = &chk.entry {
                let alias = chk.alias.as_deref().unwrap();
                let cmd = root.command();
                match cmd.get_subcommands().find(|s| s.get_name() == alias) {
                    Some(proxy) => {
                        assert_eq!(want.kind, "proxy", "{}: {alias}", c.name);
                        if let Some(h) = want.hidden {
                            assert_eq!(proxy.is_hide_set(), h, "{}: {alias} hidden", c.name);
                        }
                        if let Some(d) = &want.description {
                            let about = proxy.get_about().map(|a| a.to_string());
                            assert_eq!(about.as_deref(), Some(d.as_str()), "{}: {alias}", c.name);
                        }
                    }
                    None => {
                        let native = cmd
                            .get_subcommands()
                            .any(|s| s.get_all_aliases().any(|a| a == alias));
                        let got = if native { "native" } else { "missing" };
                        assert_eq!(got, want.kind, "{}: {alias}", c.name);
                    }
                };
            } else if let Some(want) = &chk.run {
                let argv = chk.argv.clone().unwrap();
                let m = parse(&root, &argv).unwrap_or_else(|e| panic!("{}: {argv:?}: {e}", c.name));
                assert_eq!(want.ran, "target", "{}: rust runs targets only", c.name);
                let mut path = Vec::new();
                let mut cur = &m;
                while let Some((sub, sm)) = cur.subcommand() {
                    path.push(sub.to_string());
                    cur = sm;
                }
                let env = cur.try_get_one::<String>("env").ok().flatten().cloned();
                let args: Vec<String> = cur
                    .get_many::<String>("args")
                    .map(|v| v.cloned().collect())
                    .unwrap_or_default();
                assert_eq!(Some(path.join(" ")), want.path, "{}: {argv:?} path", c.name);
                assert_eq!(env, want.env, "{}: {argv:?} env", c.name);
                assert_eq!(Some(args), want.args, "{}: {argv:?} args", c.name);
            } else if let Some(want) = &chk.help {
                let argv = chk.argv.clone().unwrap();
                let err = parse(&root, &argv).expect_err("help renders as an error");
                assert_eq!(err.kind(), ErrorKind::DisplayHelp, "{}: {argv:?}", c.name);
                let text = err.render().to_string();
                let alias = &argv[0];
                let target_desc = &descs[&c.aliases[alias]];
                let proxy_about = root
                    .command()
                    .get_subcommands()
                    .find(|s| s.get_name() == alias)
                    .and_then(|p| p.get_about().map(|a| a.to_string()));
                let got = if text.contains(target_desc.as_str()) {
                    "target"
                } else if proxy_about.is_some_and(|a| text.contains(&a)) {
                    "alias"
                } else {
                    "unknown"
                };
                assert_eq!(got, want, "{}: {argv:?}\n{text}", c.name);
            } else if let Some(words) = &chk.complete {
                let (mut out, mut err) = (Vec::new(), Vec::new());
                root.complete(words, true, &mut out, &mut err).unwrap();
                let mut got: Vec<String> = String::from_utf8(out)
                    .unwrap()
                    .lines()
                    .filter(|l| !l.is_empty() && !l.starts_with(':'))
                    .map(String::from)
                    .collect();
                got.sort();
                let mut want = chk.candidates.clone().unwrap();
                want.sort();
                assert_eq!(got, want, "{}: complete {words:?}", c.name);
            } else {
                panic!("{}: check has no observable", c.name);
            }
        }
    }
}
