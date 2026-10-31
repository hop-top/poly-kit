//! Runs the shared corpus in `contracts/completion-spec-v1`: rebuild the
//! fixture tree in clap, export it, compare byte for byte. The expected
//! files are the source of truth (Go is canonical): a failure means this
//! port drifted, not that an expected file should be edited to fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must hold exactly [`FILES`].
//!
//! `tree.json` is cobra-shaped. The cobra-side facts the contract lists
//! (Use operands, `kit/args`, pflag `UnquoteUsage`, deprecated meaning
//! hidden) are built into the clap tree here, as are two more that clap
//! spells differently: cobra's version flag (`--version`, short `v`
//! unless taken; clap's own is `-V`) and cobra `ValidArgs` (clap
//! possible values on a positional, a lone optional variadic `arg` when
//! the command names none).

#![cfg(feature = "cli")]

use std::fs;
use std::path::PathBuf;

use hop_top_kit::cli::completion::{bind_args, bind_flag, dir, file, func, Item};
use hop_top_kit::cli::completion_spec::{render_fig, render_usage, Spec};
use hop_top_kit::cli::{ArgSpec, CommandSpec, FlagSpec, Root, ValueSpec};
use serde::Deserialize;

const FILES: &[&str] = &[
    "README.md",
    "tree.json",
    "expected.fig.js",
    "expected.usage.kdl",
    "tree-tera.json",
    "expected-tera.fig.js",
    "expected-tera.usage.kdl",
];

/// Each fixture tree with its expected Fig and Usage files.
const CASES: &[(&str, &str, &str)] = &[
    ("tree.json", "expected.fig.js", "expected.usage.kdl"),
    (
        "tree-tera.json",
        "expected-tera.fig.js",
        "expected-tera.usage.kdl",
    ),
];

fn corpus() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/completion-spec-v1")
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfTree {
    version: String,
    #[allow(dead_code)]
    description: String,
    root: ConfCmd,
}

#[derive(Deserialize, Default)]
#[serde(deny_unknown_fields, default)]
struct ConfCmd {
    #[serde(rename = "use")]
    use_: String,
    short: String,
    version: String,
    aliases: Vec<String>,
    hidden: bool,
    deprecated: String,
    annotations: std::collections::BTreeMap<String, String>,
    valid_args: Vec<String>,
    valid_args_function: bool,
    no_args: bool,
    flags: Vec<ConfFlag>,
    commands: Vec<ConfCmd>,
}

#[derive(Deserialize, Default)]
#[serde(deny_unknown_fields, default)]
struct ConfFlag {
    name: String,
    short: String,
    #[serde(rename = "type")]
    type_: String,
    usage: String,
    persistent: bool,
    hidden: bool,
    deprecated: String,
    required: bool,
    no_opt_default: String,
    #[serde(rename = "enum")]
    enum_: Option<Vec<String>>,
    filename_ext: Option<Vec<String>>,
    dirname: bool,
    completion_function: bool,
}

fn load() -> ConfTree {
    load_file("tree.json")
}

fn load_file(name: &str) -> ConfTree {
    let dir = corpus();
    for e in fs::read_dir(&dir).unwrap() {
        let n = e.unwrap().file_name().into_string().unwrap();
        assert!(FILES.contains(&n.as_str()), "{n} has no runner");
    }
    let raw = fs::read_to_string(dir.join(name)).unwrap();
    let tree: ConfTree = serde_json::from_str(&raw).unwrap_or_else(|e| panic!("{name}: {e}"));
    assert_eq!(tree.version, "v1", "{name} version");
    tree
}

/// A completer binding to apply once the tree is wrapped in a [`Root`].
enum Binding {
    Args(String),
    Flag(String, String, Bind),
}

enum Bind {
    File(Vec<String>),
    Dir,
    Func,
}

fn none() -> impl hop_top_kit::cli::completion::Completer {
    func(|_, _| Ok(Vec::<Item>::new()))
}

/// Rebuilds the fixture tree as a [`Root`] with cobra's default
/// completion command disabled, as the Go runner's bare cobra tree has.
fn build(tree: &ConfCmd) -> Root {
    let mut bindings = Vec::new();
    let cmd = build_cmd(tree, "", true, &mut bindings);
    let mut root = Root::new(cmd);
    root.disable_completion_command();
    for b in bindings {
        match b {
            Binding::Args(path) => bind_args(&mut root, &path, none()),
            Binding::Flag(path, flag, Bind::File(exts)) => {
                bind_flag(&mut root, &path, &flag, file(exts))
            }
            Binding::Flag(path, flag, Bind::Dir) => bind_flag(&mut root, &path, &flag, dir()),
            Binding::Flag(path, flag, Bind::Func) => bind_flag(&mut root, &path, &flag, none()),
        }
    }
    root
}

fn build_cmd(c: &ConfCmd, parent: &str, is_root: bool, bindings: &mut Vec<Binding>) -> CommandSpec {
    let words = use_words(&c.use_);
    // cobra `Name()`: the Use line up to the first ASCII space.
    let name = c.use_.split(' ').next().unwrap_or_default().to_string();
    let path = match (is_root, parent.is_empty()) {
        (true, _) => String::new(),
        (false, true) => name.clone(),
        (false, false) => format!("{parent} {name}"),
    };
    let mut cmd = CommandSpec::new(name);
    if !c.short.is_empty() {
        cmd = cmd.about(c.short.clone());
    }
    for a in &c.aliases {
        cmd = cmd.alias(a.clone());
    }
    if c.hidden || !c.deprecated.is_empty() {
        cmd = cmd.hidden();
    }

    // Positionals: none under NoArgs; else kit/args; else Use operands.
    let mut args = Vec::new();
    if !c.no_args {
        args = match c.annotations.get("kit/args") {
            Some(raw) if !raw.trim().is_empty() => kit_args(raw),
            _ => use_args(&words),
        };
        if !c.valid_args.is_empty() && args.is_empty() {
            args.push(Operand {
                name: "arg".into(),
                optional: true,
                variadic: true,
            });
        }
    }
    let valid: Vec<String> = c
        .valid_args
        .iter()
        .map(|v| v.split('\t').next().unwrap_or_default().to_string())
        .collect();
    for op in args {
        let mut a = ArgSpec::new(op.name);
        if !op.optional {
            a = a.required();
        }
        if op.variadic {
            a = a.variadic();
        }
        if !valid.is_empty() {
            a = a.allowed_values(valid.iter().map(ValueSpec::new));
        }
        cmd = cmd.arg(a);
    }
    if c.valid_args_function && !c.no_args {
        bindings.push(Binding::Args(path.clone()));
    }

    for f in &c.flags {
        cmd = add_flag(cmd, f, &path, bindings);
    }
    // cobra adds its version flag only when the root declares none.
    if is_root && !c.version.is_empty() && !c.flags.iter().any(|f| f.name == "version") {
        // cobra's version flag: `-v` unless the root already uses it.
        let taken = c.flags.iter().any(|f| f.short == "v");
        cmd = cmd.version(c.version.clone(), (!taken).then_some('v'));
    }
    for sub in &c.commands {
        cmd = cmd.subcommand(build_cmd(sub, &path, false, bindings));
    }
    cmd
}

fn add_flag(
    cmd: CommandSpec,
    f: &ConfFlag,
    path: &str,
    bindings: &mut Vec<Binding>,
) -> CommandSpec {
    let (varname, usage) = unquote_usage(&f.type_, &f.usage);
    let mut a = match f.type_.as_str() {
        _ if f.name == "help" => FlagSpec::help_flag(f.name.clone()),
        "bool" => FlagSpec::bool(f.name.clone()),
        "count" => FlagSpec::count(f.name.clone()),
        "string" | "int" | "duration" => FlagSpec::string(f.name.clone()).value_name(varname),
        "stringSlice" | "stringArray" => FlagSpec::strings(f.name.clone()).value_name(varname),
        t => panic!("flag {}: unknown type {t:?}", f.name),
    }
    .help(usage);
    if let Some(s) = f.short.chars().next() {
        a = a.short(s);
    }
    if f.persistent {
        a = a.persistent();
    }
    if f.hidden || !f.deprecated.is_empty() {
        a = a.hidden();
    }
    if f.required {
        a = a.required();
    }
    if !f.no_opt_default.is_empty() {
        a = a.no_opt_default(f.no_opt_default.clone());
    }
    if let Some(vals) = &f.enum_ {
        // A kit enum is advisory: the bare value stays accepted, hidden.
        let mut pv: Vec<ValueSpec> = vals.iter().map(ValueSpec::new).collect();
        if !f.no_opt_default.is_empty() && !vals.contains(&f.no_opt_default) {
            pv.push(ValueSpec::hidden(f.no_opt_default.clone()));
        }
        a = a.allowed_values(pv);
    }
    let key = |b| Binding::Flag(path.to_string(), f.name.clone(), b);
    if let Some(exts) = &f.filename_ext {
        bindings.push(key(Bind::File(exts.clone())));
    }
    if f.dirname {
        bindings.push(key(Bind::Dir));
    }
    if f.completion_function {
        bindings.push(key(Bind::Func));
    }
    cmd.flag(a)
}

/// pflag `UnquoteUsage`: the first backquoted word names the value and
/// loses its quotes; else the type names it.
fn unquote_usage(typ: &str, usage: &str) -> (String, String) {
    if let Some(i) = usage.find('`') {
        if let Some(j) = usage[i + 1..].find('`') {
            let name = &usage[i + 1..i + 1 + j];
            let rest = format!("{}{name}{}", &usage[..i], &usage[i + 2 + j..]);
            return (name.to_string(), rest);
        }
    }
    let name = match typ {
        "float64" => "float",
        "int64" => "int",
        "uint64" => "uint",
        "stringSlice" => "strings",
        "intSlice" => "ints",
        "uintSlice" => "uints",
        "boolSlice" => "bools",
        "durationSlice" => "durations",
        t => t,
    };
    (name.to_string(), usage.to_string())
}

struct Operand {
    name: String,
    optional: bool,
    variadic: bool,
}

/// kit/args: a comma list, `?` marking an optional name, `...` a
/// variadic one (`...?` optional variadic); the dots come before the
/// question mark, any other suffix is part of the name.
fn kit_args(raw: &str) -> Vec<Operand> {
    raw.split(',')
        .map(str::trim)
        .filter(|n| !n.is_empty())
        .map(|n| {
            let (n, optional) = match n.strip_suffix('?') {
                Some(s) => (s, true),
                None => (n, false),
            };
            let (n, variadic) = match n.strip_suffix("...") {
                Some(s) => (s, true),
                None => (n, false),
            };
            Operand {
                name: n.to_string(),
                optional,
                variadic,
            }
        })
        .collect()
}

/// The operands of a cobra Use line, skipping `[flags]`, `[command]`
/// and flag syntax.
fn use_args(words: &[String]) -> Vec<Operand> {
    let mut out = Vec::new();
    for w in words.iter().skip(1) {
        if w == "[flags]" || w == "[command]" {
            continue;
        }
        if w.trim_start_matches(['[', '<', '(']).starts_with('-') {
            continue;
        }
        let (mut w, mut variadic) = match w.strip_suffix("...") {
            Some(s) => (s, true),
            None => (w.as_str(), false),
        };
        let mut optional = false;
        if w.starts_with('[') && w.ends_with(']') {
            optional = true;
            w = &w[1..w.len() - 1];
        } else if w.starts_with('<') && w.ends_with('>') {
            w = &w[1..w.len() - 1];
        }
        if let Some(inner) = w.strip_suffix("...") {
            w = inner;
            variadic = true;
        }
        if w.is_empty() {
            continue;
        }
        out.push(Operand {
            name: w.to_string(),
            optional,
            variadic,
        });
    }
    out
}

/// Splits a Use line on spaces, keeping a bracketed group spanning
/// several words as one word without its spaces.
fn use_words(use_: &str) -> Vec<String> {
    let mut out = Vec::new();
    let mut cur = String::new();
    let mut depth: i64 = 0;
    for w in use_.split_whitespace() {
        cur.push_str(w);
        let count = |c: char| w.matches(c).count() as i64;
        depth += count('[') + count('<') - count(']') - count('>');
        if depth > 0 {
            continue;
        }
        out.push(std::mem::take(&mut cur));
        depth = 0;
    }
    if !cur.is_empty() {
        out.push(cur);
    }
    out
}

/// The first differing line, with both sides.
fn line_diff(want: &str, got: &str) -> String {
    let (w, g): (Vec<&str>, Vec<&str>) = (want.split('\n').collect(), got.split('\n').collect());
    for i in 0..w.len().max(g.len()) {
        let (wl, gl) = (w.get(i).copied(), g.get(i).copied());
        if wl != gl {
            return format!("line {}:\n  want: {wl:?}\n  got:  {gl:?}", i + 1);
        }
    }
    "(no line differs; trailing bytes differ)".into()
}

/// An emitter.
type Render = fn(&Spec) -> String;

#[test]
fn completion_spec_conformance() {
    for &(tree, fig, usage) in CASES {
        let spec = Spec::from_root(&build(&load_file(tree).root));
        let cases: [(&str, Render); 2] = [(fig, render_fig), (usage, render_usage)];
        for (name, render) in cases {
            let want = fs::read_to_string(corpus().join(name)).unwrap();
            let got = render(&spec);
            assert!(got == want, "{name} drifted:\n{}", line_diff(&want, &got));
        }
    }
}

fn reverse(c: &mut ConfCmd) {
    c.flags.reverse();
    c.commands.reverse();
    c.commands.iter_mut().for_each(reverse);
}

/// Declaration order must not leak into the output: reversing every
/// list in the tree gives the same bytes.
#[test]
fn completion_spec_order() {
    let mut tree = load();
    let want = render_fig(&Spec::from_root(&build(&tree.root)));
    reverse(&mut tree.root);
    let got = render_fig(&Spec::from_root(&build(&tree.root)));
    assert!(
        got == want,
        "declaration order leaked:\n{}",
        line_diff(&want, &got)
    );
}
