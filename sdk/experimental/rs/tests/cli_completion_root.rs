//! Ports of the Go tests for what completion reads from the CLI root, by
//! name: alias names in root completions (`console/cli/alias_test.go`),
//! enum flags completing their values (the flag-enum tests) and the
//! `completion` command's placement (`cli_test.go`, `parity_test.go`,
//! `usage_unknown_test.go`), plus the `__complete` engine's flag handling.

#![cfg(feature = "cli")]

use clap::{Arg, ArgAction, Command};
use hop_top_kit::cli::completion::{
    bind_args, bind_flag, file, func, static_values, Context, Directive, Item,
};
use hop_top_kit::cli::Root;

fn complete(root: &Root, args: &[&str], no_desc: bool) -> (String, String) {
    let args: Vec<String> = args.iter().map(|s| s.to_string()).collect();
    let (mut out, mut err) = (Vec::new(), Vec::new());
    root.complete(&args, no_desc, &mut out, &mut err).unwrap();
    (
        String::from_utf8(out).unwrap(),
        String::from_utf8(err).unwrap(),
    )
}

// --- alias names in root completions (alias_test.go) ---

fn deploy() -> Command {
    Command::new("deploy").about("Deploy a target")
}

#[test]
fn alias_completion_includes_aliases() {
    let mut root = Root::new(Command::new("mytool").subcommand(deploy()));
    root.alias("d", "deploy").unwrap();
    let (results, d) = root.args_completion("", &[], "d").unwrap();
    assert_eq!(d, Directive::NO_FILE_COMP);
    let alias = results
        .iter()
        .find(|s| s.starts_with("d\t"))
        .expect("alias 'd' must appear");
    assert!(alias.contains("alias for"), "{alias}");
}

#[test]
fn alias_completion_filters_prefix() {
    let mut root = Root::new(
        Command::new("mytool")
            .subcommand(deploy())
            .subcommand(Command::new("serve")),
    );
    root.alias("d", "deploy").unwrap();
    root.alias("s", "serve").unwrap();
    let (results, _) = root.args_completion("", &[], "d").unwrap();
    assert!(results.iter().all(|s| !s.starts_with("s\t")), "{results:?}");
}

#[test]
fn alias_completion_preserves_original() {
    let mut root = Root::new(Command::new("mytool").subcommand(deploy()));
    bind_args(
        &mut root,
        "",
        func(|_: &Context, _: &str| Ok(vec![Item::new("original")])),
    );
    root.alias("d", "deploy").unwrap();
    let (results, _) = root.args_completion("", &[], "").unwrap();
    assert!(results.contains(&"original".to_string()), "{results:?}");
    assert!(results.iter().any(|s| s.starts_with("d\t")), "{results:?}");
}

#[test]
fn alias_completion_from_store() {
    // Go loads `rs: router start` from an alias store; the store is not
    // ported, the deep alias it registers is.
    let router = Command::new("router").subcommand(Command::new("start"));
    let mut root = Root::new(Command::new("mytool").subcommand(router));
    root.alias("rs", "router start").unwrap();
    let (results, _) = root.args_completion("", &[], "r").unwrap();
    let alias = results
        .iter()
        .find(|s| s.starts_with("rs\t"))
        .expect("alias 'rs' must appear");
    assert!(alias.contains("alias for"), "{alias}");
}

#[test]
fn alias_completion_preserves_directive() {
    let mut root = Root::new(Command::new("mytool").subcommand(deploy()));
    bind_args(&mut root, "", file(["txt"]));
    root.alias("d", "deploy").unwrap();
    let (_, d) = root.args_completion("", &[], "").unwrap();
    assert!(d.contains(Directive::FILTER_FILE_EXT), "{d:?}");
    assert!(d.contains(Directive::NO_FILE_COMP), "{d:?}");
}

#[test]
fn alias_errors_match_go() {
    let mut root = Root::new(Command::new("mytool").subcommand(deploy()));
    assert_eq!(
        root.alias("", "deploy").unwrap_err().to_string(),
        r#"alias name "" must be non-empty without whitespace"#
    );
    assert_eq!(
        root.alias("a b", "deploy").unwrap_err().to_string(),
        r#"alias name "a b" must be non-empty without whitespace"#
    );
    assert_eq!(
        root.alias("deploy", "deploy").unwrap_err().to_string(),
        r#"alias "deploy" collides with command "deploy""#
    );
    root.alias("d", "deploy").unwrap();
    assert_eq!(
        root.alias("d", "deploy").unwrap_err().to_string(),
        r#"alias "d" already registered for "deploy""#
    );
    assert_eq!(
        root.alias("x", "nosuch").unwrap_err().to_string(),
        r#"command "nosuch" not found in "mytool""#
    );
    assert_eq!(root.aliases().get("d").map(String::as_str), Some("deploy"));
}

#[test]
fn deep_alias_shim_has_none_of_the_target_flags() {
    let start = Command::new("start").arg(Arg::new("level").long("level").help("Log level"));
    let router = Command::new("router").subcommand(start);
    let mut root = Root::new(Command::new("mytool").subcommand(router));
    root.alias("rs", "router start").unwrap();
    let (out, _) = complete(&root, &["rs", "--"], false);
    assert_eq!(out, "--help\thelp for rs\n:4\n");
    // Like Go's shim: positional arguments pass, the target's flags do not.
    let m = root
        .command()
        .try_get_matches_from(["mytool", "rs", "a", "b"])
        .unwrap();
    let (name, sub) = m.subcommand().unwrap();
    assert_eq!(name, "rs");
    let args: Vec<&String> = sub.get_many(hop_top_kit::cli::SHIM_ARGS).unwrap().collect();
    assert_eq!(args, ["a", "b"]);
    assert!(root
        .command()
        .try_get_matches_from(["mytool", "rs", "--level", "x"])
        .is_err());
}

#[test]
fn alias_named_completion_replaces_the_builtin() {
    let mut root = Root::new(Command::new("mytool").subcommand(deploy()));
    root.alias("completion", "deploy").unwrap();
    assert_eq!(root.command_group("completion"), None);
    let m = root
        .command()
        .try_get_matches_from(["mytool", "completion"])
        .unwrap();
    assert_eq!(m.subcommand_name(), Some("deploy"));
    assert!(run(&root, &["mytool", "completion", "bash"]).is_none());
    let (out, _) = complete(&root, &["comp"], false);
    assert_eq!(out, "completion\talias for deploy\n:4\n");
}

#[test]
fn alias_dispatches_through_clap() {
    let router = Command::new("router").subcommand(Command::new("start"));
    let mut root = Root::new(
        Command::new("mytool")
            .subcommand(deploy())
            .subcommand(router),
    );
    root.alias("d", "deploy").unwrap();
    root.alias("rs", "router start").unwrap();
    let m = root
        .command()
        .try_get_matches_from(["mytool", "d"])
        .unwrap();
    assert_eq!(m.subcommand_name(), Some("deploy"));
    let m = root
        .command()
        .try_get_matches_from(["mytool", "rs"])
        .unwrap();
    assert_eq!(m.subcommand_name(), Some("rs"));
    assert_eq!(root.alias_target("rs"), Some("router start"));
}

// --- enum flags complete their values (flag_enum*_test.go) ---

fn enum_tree() -> Root {
    let list = Command::new("list").arg(Arg::new("status").long("status").help("Status"));
    Root::new(Command::new("tool").subcommand(list))
}

#[test]
fn bind_flag_enum_completions_serves_enum_prefix_filtered() {
    let mut root = enum_tree();
    root.with_flag_enum("status", ["TODO", "IN_PROGRESS", "DONE"]);
    let (out, d) = root.flag_completion("list", "status", "").unwrap();
    assert_eq!(out.len(), 3, "{out:?}");
    assert_eq!(d, Directive::NO_FILE_COMP);
    let (out, _) = root.flag_completion("list", "status", "do").unwrap();
    assert_eq!(out, ["DONE"]);
}

#[test]
fn bind_flag_enum_completions_adopter_completer_wins() {
    let mut root = enum_tree();
    bind_flag(
        &mut root,
        "list",
        "status",
        static_values(["adopter-supplied"]),
    );
    root.with_flag_enum("status", ["TODO", "DONE"]);
    let (out, _) = root.flag_completion("list", "status", "").unwrap();
    assert_eq!(out, ["adopter-supplied"]);
}

#[test]
fn flag_enum_annotation_drives_completion() {
    // clap's possible values are the annotation: no registry entry.
    let list = Command::new("list").arg(
        Arg::new("mode")
            .long("mode")
            .help("Mode")
            .value_parser(["fast", "slow"]),
    );
    let root = Root::new(Command::new("tool").subcommand(list));
    let (out, d) = root.flag_completion("list", "mode", "").unwrap();
    assert_eq!(out, ["fast", "slow"]);
    assert_eq!(d, Directive::NO_FILE_COMP);
}

#[test]
fn command_flag_enum_scopes_to_one_command() {
    let list = Command::new("list").arg(Arg::new("type").long("type"));
    let export = Command::new("export").arg(Arg::new("type").long("type"));
    let mut root = Root::new(Command::new("tool").subcommand(list).subcommand(export));
    root.with_flag_enum("type", ["any"]);
    root.with_command_flag_enum("list", "type", ["TASK", "TRACK"]);
    assert_eq!(
        root.flag_completion("list", "type", "").unwrap().0,
        ["TASK", "TRACK"]
    );
    assert_eq!(
        root.flag_completion("export", "type", "").unwrap().0,
        ["any"]
    );
}

#[test]
fn flag_enum_help_suffix() {
    let mut root = enum_tree();
    root.with_flag_enum("status", ["TODO", "DONE"]);
    let cmd = root.command();
    let list = cmd.find_subcommand("list").unwrap();
    let help = list
        .get_arguments()
        .find(|a| a.get_id() == "status")
        .unwrap()
        .get_help()
        .unwrap()
        .to_string();
    assert_eq!(help, "Status (one of: TODO, DONE)");
    // Re-registering replaces the suffix instead of stacking it.
    root.with_flag_enum("status", ["A"]);
    let cmd = root.command();
    let help = cmd
        .find_subcommand("list")
        .unwrap()
        .get_arguments()
        .find(|a| a.get_id() == "status")
        .unwrap()
        .get_help()
        .unwrap()
        .to_string();
    assert_eq!(help, "Status (one of: A)");
}

#[test]
fn execute_enum_reaches_completion() {
    let mut root = enum_tree();
    root.with_flag_enum("status", ["TODO", "IN_PROGRESS", "DONE", "SKIPPED"]);
    let (out, _) = complete(&root, &["list", "--status", ""], false);
    for v in ["TODO", "IN_PROGRESS", "DONE", "SKIPPED"] {
        assert!(out.contains(v), "completion omitted {v}:\n{out}");
    }
}

// --- the completion command (cli_test.go, parity_test.go, usage_unknown_test.go) ---

fn root_with_sub() -> Root {
    Root::new(
        Command::new("mytool")
            .about("A test tool")
            .subcommand(Command::new("sub").about("A subcommand")),
    )
}

#[test]
fn completion_in_management_group() {
    let root = root_with_sub();
    let cmd = root.command();
    let comp = cmd
        .find_subcommand("completion")
        .expect("completion subcommand must exist");
    let shells: Vec<&str> = comp.get_subcommands().map(|c| c.get_name()).collect();
    assert_eq!(
        shells,
        ["bash", "zsh", "fish", "powershell", "fig", "usage"]
    );
    assert_eq!(root.command_group("completion"), Some("management"));
}

#[test]
fn completion_hidden_from_default_help() {
    let help = root_with_sub().command().render_help().to_string();
    assert!(!help.contains("completion"), "{help}");
    assert!(help.contains("sub"), "{help}");
}

#[test]
fn completion_visible_in_help_all() {
    let help = root_with_sub().help_all_command().render_help().to_string();
    assert!(help.contains("completion"), "{help}");
}

#[test]
fn help_structure_no_help_or_completion_subcommand() {
    // Go pins what help lists, not what dispatches.
    let root = root_with_sub();
    let help = root.command().render_help().to_string();
    let commands = help.split("Commands:").nth(1).unwrap_or_default();
    assert!(
        !commands.lines().any(|l| l.trim_start().starts_with("help")),
        "{help}"
    );
    assert!(
        !commands
            .lines()
            .any(|l| l.trim_start().starts_with("completion")),
        "{help}"
    );
    let (out, _) = complete(&root, &["h"], false);
    assert_eq!(out, ":4\n");
}

#[test]
fn adopter_completion_command_is_kept() {
    let root =
        Root::new(Command::new("mytool").subcommand(Command::new("completion").about("mine")));
    let cmd = root.command();
    let comp = cmd.find_subcommand("completion").unwrap();
    assert_eq!(comp.get_about().unwrap().to_string(), "mine");
    assert_eq!(comp.get_subcommands().count(), 0);
}

fn run(root: &Root, argv: &[&str]) -> Option<(String, String)> {
    let (mut out, mut err) = (Vec::new(), Vec::new());
    let handled = root.try_builtin(argv.iter().copied(), &mut out, &mut err)?;
    handled.unwrap();
    Some((
        String::from_utf8(out).unwrap(),
        String::from_utf8(err).unwrap(),
    ))
}

#[test]
fn parity_completion_subcommand() {
    let (out, _) = run(&root_with_sub(), &["mytool", "completion"]).unwrap();
    assert!(
        !out.contains("_mytool_completions")
            && !out.contains("compdef")
            && !out.contains("complete -F"),
        "must not dump a script:\n{out}"
    );
    assert!(out.contains("bash"), "{out}");
}

#[test]
fn completion_bash_emits_a_script() {
    let (out, _) = run(&root_with_sub(), &["mytool", "completion", "bash"]).unwrap();
    assert!(out.starts_with("# bash completion V2 for mytool"), "{out}");
    assert!(out.contains("__complete "), "{out}");
    let (out, _) = run(
        &root_with_sub(),
        &["mytool", "completion", "bash", "--no-descriptions"],
    )
    .unwrap();
    assert!(out.contains("__completeNoDesc"), "{out}");
}

#[test]
fn unknown_completion_requests_are_not_refused() {
    for argv in [
        &["mytool", "__complete", "wid"][..],
        &["mytool", "__completeNoDesc", "sub", ""][..],
    ] {
        let (out, _) = run(&root_with_sub(), argv).unwrap();
        assert!(out.contains(':'), "{out}");
    }
}

#[test]
fn ordinary_commands_are_not_builtins() {
    assert!(run(&root_with_sub(), &["mytool", "sub"]).is_none());
    assert!(run(&root_with_sub(), &["mytool"]).is_none());
}

#[test]
fn flag_name_completion_lists_flags_with_usage() {
    let leaf = Command::new("leaf")
        .arg(
            Arg::new("verbose")
                .long("verbose")
                .short('v')
                .action(ArgAction::SetTrue)
                .help("Talk more"),
        )
        .arg(Arg::new("name").long("name").help("A name"));
    let root = Root::new(Command::new("tool").subcommand(leaf));
    let (out, _) = complete(&root, &["leaf", "-"], false);
    assert_eq!(
        out,
        "--help\thelp for leaf\n-h\thelp for leaf\n--name\tA name\n--verbose\tTalk more\n-v\tTalk more\n:4\n"
    );
    // A flag already given is not offered again.
    let (out, _) = complete(&root, &["leaf", "--name", "x", "--n"], false);
    assert_eq!(out, ":4\n");
}

#[test]
fn required_flags_come_first() {
    let leaf = Command::new("leaf")
        .arg(
            Arg::new("must")
                .long("must")
                .required(true)
                .help("Required"),
        )
        .arg(Arg::new("may").long("may").help("Optional"));
    let root = Root::new(Command::new("tool").subcommand(leaf));
    let (out, _) = complete(&root, &["leaf", "--m"], false);
    assert_eq!(out, "--must\tRequired\n:4\n");
    let (out, _) = complete(&root, &["leaf", ""], false);
    assert_eq!(out, "--must\tRequired\n:0\n");
}

#[test]
fn unknown_flag_reports_on_stderr() {
    let root = root_with_sub();
    let (out, err) = complete(&root, &["sub", "--nosuch", ""], false);
    assert_eq!(out, ":0\n");
    assert!(
        err.starts_with(
            "[Debug] [Error] Error while parsing flags from args [--nosuch]: unknown flag: --nosuch\n"
        ),
        "{err}"
    );
}
