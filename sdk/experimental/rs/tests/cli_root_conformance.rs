//! Runs the shared corpus in `contracts/cli-root-v1`. The vectors are
//! the source of truth: a failure means this port drifted from the
//! contract (Go is canonical), not that a vector should be edited.

#![cfg(feature = "cli")]
#![allow(clippy::result_large_err)]

use std::collections::BTreeMap;
use std::fs;
use std::path::PathBuf;

use hop_top_kit::cli::{ArgSpec, CommandSpec, Config, FlagSpec, Root};
use serde::Deserialize;
use serde_json::{json, Value};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Flag {
    long: String,
    #[serde(default)]
    short: String,
    #[serde(default)]
    help: String,
    #[serde(default, rename = "type")]
    ty: String,
    #[serde(default, rename = "enum")]
    enum_: Vec<String>,
    #[serde(default)]
    default: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Cmd {
    name: String,
    about: String,
    #[serde(default)]
    side_effect: String,
    #[serde(default)]
    flags: Vec<Flag>,
    #[serde(default)]
    commands: Vec<Cmd>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Tree {
    program: String,
    version: String,
    about: String,
    commands: Vec<Cmd>,
    #[serde(default)]
    aliases: BTreeMap<String, String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Case {
    name: String,
    tree: String,
    args: Vec<String>,
    #[serde(default)]
    env: BTreeMap<String, String>,
    exit: i32,
    stdout: Option<String>,
    #[serde(default)]
    stdout_contains: Vec<String>,
    #[serde(default)]
    stdout_omits: Vec<String>,
    #[serde(default)]
    stderr_contains: Vec<String>,
    stderr_json: Option<BTreeMap<String, Value>>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct File {
    #[serde(rename = "$schema")]
    _schema: String,
    #[serde(rename = "$id")]
    _id: String,
    version: String,
    #[allow(dead_code)]
    description: String,
    trees: BTreeMap<String, Tree>,
    cases: Vec<Case>,
}

fn command(c: &Cmd, root: &mut Root, prefix: &str) -> CommandSpec {
    let path = format!("{prefix} {}", c.name).trim().to_string();
    let mut spec = CommandSpec::new(c.name.clone()).about(c.about.clone());
    for f in &c.flags {
        let mut fl = match f.ty.as_str() {
            "bool" => FlagSpec::bool(f.long.clone()),
            "int" => FlagSpec::int(f.long.clone()),
            _ => FlagSpec::string(f.long.clone()),
        }
        .help(f.help.clone());
        if let Some(s) = f.short.chars().next() {
            fl = fl.short(s);
        }
        if !f.default.is_empty() {
            fl = fl.default_value(f.default.clone());
        }
        if !f.enum_.is_empty() {
            root.with_command_flag_enum(&path, &f.long, f.enum_.clone());
        }
        spec = spec.flag(fl);
    }
    if c.commands.is_empty() {
        spec = spec.arg(ArgSpec::new("args").variadic());
        let p = path.clone();
        if !c.side_effect.is_empty() {
            spec = spec.side_effect(&c.side_effect);
        }
        spec = spec.handler(move |inv, out, _err| {
            let g = &inv.globals;
            let flags: BTreeMap<_, _> = inv
                .flags
                .iter()
                .map(|(k, v)| (k.clone(), v.last().cloned().unwrap_or_default()))
                .collect();
            // Keys sorted, as Go's map encoding writes them.
            let globals: BTreeMap<&str, Value> = BTreeMap::from([
                ("format", json!(g.format)),
                ("no-color", json!(g.no_color.to_string())),
                ("no-hints", json!(g.no_hints.to_string())),
                ("offline", json!(g.offline.to_string())),
                ("quiet", json!(g.quiet.to_string())),
                ("verbose", json!(g.verbose)),
            ]);
            let line: BTreeMap<&str, Value> = BTreeMap::from([
                ("args", json!(inv.args)),
                ("flags", json!(flags)),
                ("globals", json!(globals)),
                ("path", json!(p)),
            ]);
            writeln!(out, "{}", serde_json::to_string(&line).unwrap()).unwrap();
            Ok(())
        });
    }
    for s in &c.commands {
        spec = spec.subcommand(command(s, root, &path));
    }
    spec
}

#[test]
fn cli_root_corpus() {
    let dir = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/cli-root-v1");
    let raw = fs::read_to_string(dir.join("vectors.json")).unwrap();
    let f: File = serde_json::from_str(&raw).unwrap();
    assert_eq!(f.version, "v1");
    let mut used = std::collections::BTreeSet::new();
    let mut failures = Vec::new();
    for c in &f.cases {
        used.insert(c.tree.clone());
        let t = &f.trees[&c.tree];
        let mut root = Root::with_config(Config::new(&t.program, &t.version, &t.about));
        for cc in &t.commands {
            let cmd = command(cc, &mut root, "");
            root.add_command(cmd);
        }
        for (n, target) in &t.aliases {
            root.alias(n, target).unwrap();
        }
        // The environment is process-wide: set, run, restore.
        let saved = std::env::var("KIT_AUTOCORRECT").ok();
        std::env::remove_var("KIT_AUTOCORRECT");
        for (k, v) in &c.env {
            std::env::set_var(k, v);
        }
        let (mut out, mut err) = (Vec::new(), Vec::new());
        let mut argv = vec![t.program.clone()];
        argv.extend(c.args.iter().cloned());
        let code = root.run(argv, &mut out, &mut err);
        match saved {
            Some(v) => std::env::set_var("KIT_AUTOCORRECT", v),
            None => std::env::remove_var("KIT_AUTOCORRECT"),
        }
        let (out, err) = (
            String::from_utf8(out).unwrap(),
            String::from_utf8(err).unwrap(),
        );
        let mut bad = Vec::new();
        if code != c.exit {
            bad.push(format!("exit = {code}, want {}", c.exit));
        }
        if let Some(w) = &c.stdout {
            if &out != w {
                bad.push(format!("stdout = {out:?}, want {w:?}"));
            }
        }
        for s in &c.stdout_contains {
            if !out.contains(s) {
                bad.push(format!("stdout lacks {s:?}"));
            }
        }
        for s in &c.stdout_omits {
            if out.contains(s) {
                bad.push(format!("stdout has {s:?}"));
            }
        }
        for s in &c.stderr_contains {
            if !err.contains(s) {
                bad.push(format!("stderr lacks {s:?}"));
            }
        }
        if let Some(want) = &c.stderr_json {
            match serde_json::from_str::<Value>(&err) {
                Ok(got) => {
                    for (k, w) in want {
                        if &got[k] != w {
                            bad.push(format!("stderr.{k} = {}, want {w}", got[k]));
                        }
                    }
                }
                Err(e) => bad.push(format!("stderr is not JSON: {e}: {err:?}")),
            }
        }
        if !bad.is_empty() {
            failures.push(format!("{}: {} (stderr {err:?})", c.name, bad.join("; ")));
        }
    }
    for name in f.trees.keys() {
        assert!(used.contains(name), "tree {name:?} has no case");
    }
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}
