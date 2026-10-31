//! Runs the shared corpus in `contracts/cli-root-v1`. The vectors are
//! the source of truth: a failure means this port drifted from the
//! contract (Go is canonical), not that a vector should be edited.

#![cfg(all(feature = "cli", feature = "config"))]
#![allow(clippy::result_large_err)]

use std::collections::BTreeMap;
use std::fs;
use std::path::PathBuf;

use hop_top_kit::cli::{
    alias_command, aliases_command, config_command, default_policy_loader, AliasStore, ArgSpec,
    ChdirResolver, CommandSpec, Config, FlagSpec, PromptSource, ResolvedPath, Root,
    SignatureStrictness, ValidationFailureMode,
};
use hop_top_kit::output::CliError;
use serde::Deserialize;
use serde_json::{json, Value};
use std::sync::{Arc, Mutex};

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
    long: String,
    #[serde(default)]
    kind: String,
    #[serde(default)]
    dry_run: String,
    #[serde(default)]
    side_effect: String,
    #[serde(default)]
    annotations: BTreeMap<String, String>,
    #[serde(default)]
    flags: Vec<Flag>,
    #[serde(default)]
    commands: Vec<Cmd>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Validator {
    flag: String,
    reject: Vec<String>,
    code: String,
    message: String,
    exit: i32,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Rung {
    path: String,
    source: String,
    scope: String,
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
    #[serde(default)]
    fixtures: BTreeMap<String, String>,
    #[serde(default)]
    validate: bool,
    #[serde(default)]
    signature: String,
    #[serde(default)]
    policy: bool,
    #[serde(default)]
    chdir_resolver: BTreeMap<String, String>,
    #[serde(default)]
    project_marker: String,
    #[serde(default)]
    flag_validators: Vec<Validator>,
    #[serde(default)]
    alias_store: String,
    #[serde(default)]
    config_chain: Vec<Rung>,
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
    prompt_answer: Option<String>,
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
    if !c.long.is_empty() {
        spec = spec.long_about(c.long.clone());
    }
    for (k, v) in &c.annotations {
        spec = spec.annotate(k.clone(), v.clone());
    }
    if c.dry_run == "opted-out" {
        spec = spec.annotate("kit/dry-run", "opted-out");
    }
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
        let kind = c.kind.clone();
        spec = spec.handler(move |inv, out, _err| {
            match kind.as_str() {
                "pwd" => {
                    let cwd = std::env::current_dir().unwrap();
                    let line = json!({"cwd": cwd.file_name().unwrap().to_string_lossy()});
                    writeln!(out, "{line}").unwrap();
                    return Ok(());
                }
                "dryrun" => {
                    let line: BTreeMap<&str, Value> =
                        BTreeMap::from([("dry_run", json!(inv.dry_run)), ("path", json!(p))]);
                    writeln!(out, "{}", serde_json::to_string(&line).unwrap()).unwrap();
                    return Ok(());
                }
                "config" => {
                    let line = match inv.config_args() {
                        Ok((paths, overrides)) => {
                            let paths: Vec<String> = paths
                                .iter()
                                .map(|p| p.to_string_lossy().into_owned())
                                .collect();
                            let ov = serde_json::to_value(overrides.unwrap_or_default()).unwrap();
                            json!({"overrides": ov, "paths": paths})
                        }
                        Err(e) => json!({"error": e.to_string()}),
                    };
                    writeln!(out, "{line}").unwrap();
                    return Ok(());
                }
                _ => {}
            }
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
        let tmp = tempfile::tempdir().unwrap();
        let tmp_s = tmp.path().to_string_lossy().into_owned();
        let sub = |s: &str| s.replace("{tmp}", &tmp_s);
        for (name, body) in &t.fixtures {
            let p = tmp.path().join(name);
            if name.ends_with('/') {
                fs::create_dir_all(&p).unwrap();
            } else {
                fs::create_dir_all(p.parent().unwrap()).unwrap();
                fs::write(&p, sub(body)).unwrap();
            }
        }
        let mut cfg = Config::new(&t.program, &t.version, &t.about);
        cfg.disable_validate = !t.validate;
        cfg.project_marker = t.project_marker.clone();
        if t.validate || !t.signature.is_empty() {
            cfg.validation_failure = ValidationFailureMode::Error;
        }
        cfg.signature_strictness = match t.signature.as_str() {
            "" => SignatureStrictness::Silent,
            "warn" => SignatureStrictness::Warn,
            "reject" => SignatureStrictness::Reject,
            other => panic!("signature {other}"),
        };
        if !t.chdir_resolver.is_empty() {
            let (map, base) = (t.chdir_resolver.clone(), tmp.path().to_path_buf());
            cfg.chdir_resolver = Some(ChdirResolver::new(move |target| match map.get(target) {
                Some(rel) => Ok(base.join(rel)),
                None => Err(format!("unknown target {target:?}")),
            }));
        }
        let mut root = Root::with_config(cfg);
        root.with_prompt_source(match &c.prompt_answer {
            Some(a) => PromptSource::from_answers([a.clone()]),
            None => PromptSource::none(),
        });
        if t.policy {
            root.with_policy(default_policy_loader(&t.program));
        }
        for v in &t.flag_validators {
            let (reject, code, message, exit) =
                (v.reject.clone(), v.code.clone(), v.message.clone(), v.exit);
            root.with_flag_validator(&v.flag, move |val| {
                reject.iter().any(|r| r == val).then(|| {
                    let mut e = CliError::generic(message.clone());
                    e.code = code.clone();
                    e.exit_code = exit;
                    e
                })
            });
        }
        for cc in &t.commands {
            let cmd = command(cc, &mut root, "");
            root.add_command(cmd);
        }
        if !t.alias_store.is_empty() {
            let mut store = AliasStore::new(tmp.path().join(&t.alias_store));
            store.load().unwrap();
            root.add_command(alias_command(Arc::new(Mutex::new(store.clone()))));
            root.add_command(aliases_command());
            root.load_alias_store(&store).unwrap();
        }
        if !t.config_chain.is_empty() {
            let chain: Vec<(String, String, String)> = t
                .config_chain
                .iter()
                .map(|r| (sub(&r.path), r.source.clone(), r.scope.clone()))
                .collect();
            root.add_command(config_command(
                &t.program,
                Arc::new(move |_| {
                    chain
                        .iter()
                        .map(|(p, s, sc)| ResolvedPath {
                            path: p.clone(),
                            source: s.clone(),
                            scope: sc.clone(),
                            exists: std::path::Path::new(p).exists(),
                        })
                        .collect()
                }),
            ));
        }
        for (n, target) in &t.aliases {
            root.alias(n, target).unwrap();
        }
        // The environment and the working directory are process-wide:
        // set, run, restore.
        let names = ["KIT_AUTOCORRECT", "XDG_CONFIG_HOME"]
            .map(String::from)
            .into_iter()
            .chain(c.env.keys().cloned());
        let saved: Vec<(String, Option<String>)> = names
            .map(|k| {
                let v = std::env::var(&k).ok();
                (k, v)
            })
            .collect();
        let cwd = std::env::current_dir().unwrap();
        std::env::remove_var("KIT_AUTOCORRECT");
        std::env::set_var("XDG_CONFIG_HOME", tmp.path().join("xdg"));
        std::env::set_current_dir(tmp.path()).unwrap();
        for (k, v) in &c.env {
            std::env::set_var(k, sub(v));
        }
        let (mut out, mut err) = (Vec::new(), Vec::new());
        let mut argv = vec![t.program.clone()];
        argv.extend(c.args.iter().map(|a| sub(a)));
        let code = match root.execute(&argv, &mut out, &mut err) {
            Ok(()) => 0,
            Err(e) => {
                // A pre-flight failure comes back unrendered: show what
                // the Exit mode would have written.
                if err.is_empty() {
                    err.extend_from_slice(e.message.as_bytes());
                }
                e.exit_code
            }
        };
        std::env::set_current_dir(cwd).unwrap();
        for (k, v) in saved {
            match v {
                Some(v) => std::env::set_var(k, v),
                None => std::env::remove_var(k),
            }
        }
        let (out, err) = (
            String::from_utf8(out).unwrap(),
            String::from_utf8(err).unwrap(),
        );
        let mut bad = Vec::new();
        if code != c.exit {
            bad.push(format!("exit = {code}, want {}", c.exit));
        }
        let sub = |s: &str| s.replace("{tmp}", &tmp_s);
        if let Some(w) = &c.stdout {
            if out != sub(w) {
                bad.push(format!("stdout = {out:?}, want {:?}", sub(w)));
            }
        }
        for s in c.stdout_contains.iter().map(|s| sub(s)) {
            if !out.contains(&s) {
                bad.push(format!("stdout lacks {s:?}"));
            }
        }
        for s in &c.stdout_omits {
            if out.contains(s.as_str()) {
                bad.push(format!("stdout has {s:?}"));
            }
        }
        for s in c.stderr_contains.iter().map(|s| sub(s)) {
            if !err.contains(&s) {
                bad.push(format!("stderr lacks {s:?}"));
            }
        }
        if let Some(want) = &c.stderr_json {
            // The first JSON value: a renderer may add text after it.
            match serde_json::Deserializer::from_str(&err)
                .into_iter::<Value>()
                .next()
                .unwrap_or_else(|| Ok(Value::Null))
            {
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
