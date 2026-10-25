//! Ports of the Go `console/cli/completion` tests, by name: completers,
//! registry, the bridge and the package example. The root-side tests
//! (aliases, enum flags, the `completion` command) are in
//! `cli_completion_root.rs`.

#![cfg(feature = "cli")]

use std::sync::{Arc, Mutex};

use clap::{Arg, Command};
use hop_top_kit::cli::completion::{
    self, bind_args, bind_flag, config_keys, dir, file, func, prefixed, static_items,
    static_values, Completer, Context, Directive, Item, Registry,
};
use hop_top_kit::cli::Root;

fn orbits() -> Vec<Item> {
    vec![
        Item::with_description("leo", "Low Earth Orbit"),
        Item::with_description("geo", "Geostationary"),
        Item::with_description("lunar", "Trans-lunar"),
    ]
}

fn ctx() -> Context {
    Context::default()
}

fn values(items: &[Item]) -> Vec<&str> {
    items.iter().map(|i| i.value.as_str()).collect()
}

// --- completers (completion_test.go) ---

#[test]
fn static_empty_prefix() {
    let items = static_items(orbits()).complete(&ctx(), "").unwrap();
    assert_eq!(items.len(), 3);
}

#[test]
fn static_prefix_filter() {
    let items = static_items(orbits()).complete(&ctx(), "l").unwrap();
    assert_eq!(values(&items), ["leo", "lunar"]);
}

#[test]
fn static_case_insensitive() {
    let c = static_items(vec![
        Item::with_description("LEO", "Low Earth Orbit"),
        Item::with_description("GEO", "Geostationary"),
    ]);
    let items = c.complete(&ctx(), "le").unwrap();
    assert_eq!(values(&items), ["LEO"]);
}

#[test]
fn static_values_carry_no_description() {
    let items = static_values(["alpha", "beta", "gamma"])
        .complete(&ctx(), "b")
        .unwrap();
    assert_eq!(values(&items), ["beta"]);
    assert!(items[0].description.is_empty());
}

#[test]
fn func_callback_invoked() {
    let captured = Arc::new(Mutex::new(String::new()));
    let seen = captured.clone();
    let c = func(move |_ctx: &Context, prefix: &str| {
        *seen.lock().unwrap() = prefix.to_string();
        Ok(vec![Item::new("result")])
    });
    let items = c.complete(&ctx(), "test-prefix").unwrap();
    assert_eq!(*captured.lock().unwrap(), "test-prefix");
    assert_eq!(values(&items), ["result"]);
}

#[test]
fn prefixed_completes_dimension() {
    let c = prefixed("priority", static_values(["high", "low"]));
    let items = c.complete(&ctx(), "").unwrap();
    assert_eq!(values(&items), ["priority:"]);
    assert_eq!(items[0].description, "priority values");
}

#[test]
fn prefixed_completes_values_after_colon() {
    let c = prefixed("priority", static_values(["high", "low"]));
    let items = c.complete(&ctx(), "priority:h").unwrap();
    assert_eq!(values(&items), ["priority:high"]);
}

#[test]
fn prefixed_no_match_wrong_dimension() {
    let c = prefixed("priority", static_values(["high", "low"]));
    assert!(c.complete(&ctx(), "other:").unwrap().is_empty());
}

#[test]
fn config_keys_lists_and_filters() {
    let c = config_keys(vec![
        "log.level".to_string(),
        "log.format".to_string(),
        "server.port".to_string(),
    ]);
    assert_eq!(c.complete(&ctx(), "log").unwrap().len(), 2);
    assert_eq!(c.complete(&ctx(), "").unwrap().len(), 3);
}

#[cfg(feature = "config")]
#[test]
fn config_keys_from_kit_config() {
    use hop_top_kit::config::{Map, Value};
    let mut log = Map::new();
    log.insert("level".into(), "info".into());
    log.insert("format".into(), "json".into());
    let mut server = Map::new();
    server.insert("port".into(), Value::Int(8080));
    let mut root = Map::new();
    root.insert("log".into(), Value::Map(log));
    root.insert("server".into(), Value::Map(server));

    let c = config_keys(Value::Map(root));
    let mut got: Vec<String> = c
        .complete(&ctx(), "log")
        .unwrap()
        .into_iter()
        .map(|i| i.value)
        .collect();
    got.sort();
    assert_eq!(got, ["log.format", "log.level"]);
    assert_eq!(c.complete(&ctx(), "").unwrap().len(), 3);
}

#[test]
fn file_directive() {
    let c = file([".yaml", ".yml"]);
    assert_eq!(values(&c.complete(&ctx(), "").unwrap()), [".yaml", ".yml"]);
    assert_eq!(
        completion::resolve(&ctx(), &c, ""),
        (
            vec![".yaml".to_string(), ".yml".to_string()],
            Directive::FILTER_FILE_EXT
        )
    );
}

#[test]
fn dir_directive() {
    let c = dir();
    assert!(c.complete(&ctx(), "").unwrap().is_empty());
    assert_eq!(
        completion::resolve(&ctx(), &c, "x"),
        (vec![], Directive::FILTER_DIRS)
    );
}

#[test]
fn registry_flag_lookup() {
    let mut r = Registry::new();
    r.register("orbit", static_values(["a", "b"]));
    assert!(r.for_flag("orbit").is_some());
    assert!(r.for_flag("missing").is_none());
}

#[test]
fn registry_arg_lookup() {
    let mut r = Registry::new();
    r.register_arg("launch", 0, static_values(["m1", "m2"]));
    assert!(r.for_arg("launch", 0).is_some());
    assert!(r.for_arg("launch", 1).is_none());
    assert!(r.for_arg("other", 0).is_none());
}

fn test_root(flags: &[&str]) -> Root {
    let mut cmd = Command::new("test");
    for f in flags {
        cmd = cmd.arg(
            Arg::new(f.to_string())
                .long(f.to_string())
                .help(format!("{f} flag")),
        );
    }
    Root::new(Command::new("tool").subcommand(cmd))
}

#[test]
fn bind_flag_cobra_integration() {
    let mut root = test_root(&["orbit"]);
    bind_flag(
        &mut root,
        "test",
        "orbit",
        static_values(["leo", "geo", "lunar"]),
    );
    let (comps, d) = root.flag_completion("test", "orbit", "l").unwrap();
    assert_eq!(d, Directive::NO_FILE_COMP);
    assert_eq!(comps, ["leo", "lunar"]);
}

#[test]
fn bind_args_cobra_integration() {
    let mut root = Root::new(Command::new("tool").subcommand(Command::new("launch")));
    bind_args(
        &mut root,
        "launch",
        static_values(["starlink-42", "crew-dragon-1", "starship-ift-3"]),
    );
    let (comps, d) = root.args_completion("launch", &[], "star").unwrap();
    assert_eq!(d, Directive::NO_FILE_COMP);
    assert_eq!(comps, ["starlink-42", "starship-ift-3"]);
}

#[test]
fn bind_flag_file_completer() {
    let mut root = test_root(&["config"]);
    bind_flag(&mut root, "test", "config", file([".yaml", ".yml"]));
    let (comps, d) = root.flag_completion("test", "config", "").unwrap();
    assert_eq!(d, Directive::FILTER_FILE_EXT);
    assert_eq!(comps, [".yaml", ".yml"]);
}

#[test]
fn bind_flag_file_completer_no_extensions() {
    let mut root = test_root(&["any"]);
    bind_flag(&mut root, "test", "any", file(Vec::<String>::new()));
    let (comps, d) = root.flag_completion("test", "any", "").unwrap();
    assert_eq!(d, Directive::DEFAULT);
    assert!(comps.is_empty());
}

#[test]
fn bind_flag_dir_completer() {
    let mut root = test_root(&["outdir"]);
    bind_flag(&mut root, "test", "outdir", dir());
    let (comps, d) = root.flag_completion("test", "outdir", "").unwrap();
    assert!(comps.is_empty());
    assert_eq!(d, Directive::FILTER_DIRS);
}

#[test]
fn bind_flag_unknown_flag_is_silent() {
    let mut root = test_root(&["orbit"]);
    bind_flag(&mut root, "test", "nosuch", static_values(["x"]));
    bind_flag(&mut root, "nosuch", "orbit", static_values(["x"]));
    assert!(root.flag_completion("test", "nosuch", "").is_none());
    assert!(root.flag_completion("test", "orbit", "").is_none());
}

#[test]
fn bind_flag_first_registration_wins() {
    let mut root = test_root(&["orbit"]);
    bind_flag(&mut root, "test", "orbit", static_values(["first"]));
    bind_flag(&mut root, "test", "orbit", static_values(["second"]));
    let (comps, _) = root.flag_completion("test", "orbit", "").unwrap();
    assert_eq!(comps, ["first"]);
}

fn complete(root: &Root, args: &[&str], no_desc: bool) -> (String, String) {
    let args: Vec<String> = args.iter().map(|s| s.to_string()).collect();
    let (mut out, mut err) = (Vec::new(), Vec::new());
    root.complete(&args, no_desc, &mut out, &mut err).unwrap();
    (
        String::from_utf8(out).unwrap(),
        String::from_utf8(err).unwrap(),
    )
}

#[test]
fn example_bind_args() {
    let mut root = Root::new(Command::new("deploy"));
    bind_args(
        &mut root,
        "",
        prefixed("env", static_values(["prod", "preview", "staging"])),
    );
    let (out, err) = complete(&root, &["env:pr"], true);
    assert_eq!(out, "env:prod\nenv:preview\n:4\n");
    assert_eq!(
        err,
        "Completion ended with directive: ShellCompDirectiveNoFileComp\n"
    );
}

#[test]
fn completer_error_is_reported_with_the_error_directive() {
    let mut root = Root::new(Command::new("tool"));
    bind_args(
        &mut root,
        "",
        func(|_: &Context, _: &str| Err("backend down".into())),
    );
    let (out, err) = complete(&root, &[""], false);
    assert_eq!(out, ":1\n");
    assert_eq!(
        err,
        "Completion ended with directive: ShellCompDirectiveError\n"
    );
}
