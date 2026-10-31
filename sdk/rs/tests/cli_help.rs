//! Help layout of the kit root: section titles and order, `--help-all`,
//! `--help-<id>`, the hidden `help <topic>` form and the themed (ANSI)
//! rendering. Layout follows the TypeScript and Python renderers
//! (`Usage:` line, `COMMANDS:` / group sections, `FLAGS:` last); the Go
//! topic resolution order is `all`, a command path, a group id, else a
//! usage error.

#![cfg(feature = "cli")]
#![allow(clippy::result_large_err)]

use hop_top_kit::cli::{ArgSpec, CommandSpec, Config, FlagSpec, GroupConfig, Palette, Root, Theme};

fn fixture() -> Root {
    let mut cfg = Config::new("prog", "1.0.0", "root desc");
    cfg.disable_validate = true;
    cfg.groups = vec![
        GroupConfig {
            id: "extras".into(),
            title: "EXTRAS".into(),
            hidden: false,
        },
        GroupConfig {
            id: "debug".into(),
            title: "DEBUG".into(),
            hidden: true,
        },
    ];
    let mut root = Root::with_config(cfg);
    root.add_command(
        CommandSpec::new("deploy")
            .about("Deploy the app")
            .arg(ArgSpec::new("target").help("Where to deploy").required())
            .flag(FlagSpec::string("env").short('e').help("Target env"))
            .handler(|_, _, _| Ok(())),
    );
    root.add_command(
        CommandSpec::new("remote")
            .about("Manage remotes")
            .subcommand(
                CommandSpec::new("add")
                    .about("Add a remote")
                    .flag(FlagSpec::bool("fetch").help("Fetch after adding"))
                    .handler(|_, _, _| Ok(())),
            ),
    );
    root.add_command(
        CommandSpec::new("bonus")
            .about("Bonus feature")
            .handler(|_, _, _| Ok(())),
    );
    root.add_command(
        CommandSpec::new("trace")
            .about("Trace things")
            .handler(|_, _, _| Ok(())),
    );
    root.set_group("bonus", "extras");
    root.set_group("trace", "debug");
    root
}

fn run(root: &Root, args: &[&str]) -> (i32, String, String) {
    let mut argv = vec!["prog".to_string()];
    argv.extend(args.iter().map(|s| s.to_string()));
    let (mut out, mut err) = (Vec::new(), Vec::new());
    let code = root.run(argv, &mut out, &mut err);
    (
        code,
        String::from_utf8(out).unwrap(),
        String::from_utf8(err).unwrap(),
    )
}

/// The lines of the section titled `title:` up to the next blank line.
fn section(help: &str, title: &str) -> Vec<String> {
    let head = format!("{title}:");
    let mut lines = help.lines().skip_while(|l| *l != head);
    assert!(lines.next().is_some(), "no {head} section in:\n{help}");
    lines
        .take_while(|l| !l.is_empty())
        .map(str::to_string)
        .collect()
}

#[test]
fn default_help_layout() {
    let help = fixture().render_help();
    assert!(help.starts_with("Usage: prog "), "{help}");
    assert!(help.contains("root desc"), "{help}");
    let commands = section(&help, "COMMANDS");
    assert!(commands
        .iter()
        .any(|l| l.trim_start().starts_with("deploy")));
    assert!(commands
        .iter()
        .any(|l| l.trim_start().starts_with("remote")));
    assert!(!commands.iter().any(|l| l.contains("bonus")));
    let extras = section(&help, "EXTRAS");
    assert!(extras.iter().any(|l| l.trim_start().starts_with("bonus")));
    // Hidden groups stay out; FLAGS closes the help.
    assert!(!help.contains("MANAGEMENT:"), "{help}");
    assert!(!help.contains("DEBUG:"), "{help}");
    assert!(!help.contains("Trace things"), "{help}");
    assert!(!help.contains("completion"), "{help}");
    let flags_at = help.find("FLAGS:").expect("FLAGS");
    assert!(help.find("COMMANDS:").unwrap() < help.find("EXTRAS:").unwrap());
    assert!(help.find("EXTRAS:").unwrap() < flags_at);
    let flags = section(&help, "FLAGS");
    assert!(flags.iter().any(|l| l.contains("--help-all")), "{flags:?}");
    assert!(flags.iter().any(|l| l.contains("-h, --help")), "{flags:?}");
    assert!(flags.iter().any(|l| l.contains("--format")), "{flags:?}");
    // Plumbing flags are hidden by default.
    assert!(!flags.iter().any(|l| l.contains("--chdir")), "{flags:?}");
    // Help lists no `help` subcommand.
    assert!(
        !section(&help, "COMMANDS")
            .iter()
            .any(|l| l.trim_start().starts_with("help ")),
        "{help}"
    );
}

#[test]
fn help_all_reveals_hidden_groups_and_plumbing_flags() {
    let (code, out, _) = run(&fixture(), &["--help-all"]);
    assert_eq!(code, 0);
    assert!(section(&out, "MANAGEMENT")
        .iter()
        .any(|l| l.trim_start().starts_with("completion")));
    assert!(section(&out, "DEBUG")
        .iter()
        .any(|l| l.trim_start().starts_with("trace")));
    assert!(section(&out, "FLAGS").iter().any(|l| l.contains("--chdir")));
    assert!(out.find("COMMANDS:").unwrap() < out.find("MANAGEMENT:").unwrap());
}

#[test]
fn help_group_flag_shows_only_that_group() {
    let (code, out, _) = run(&fixture(), &["--help-extras"]);
    assert_eq!(code, 0);
    assert!(section(&out, "EXTRAS").iter().any(|l| l.contains("bonus")));
    assert!(!out.contains("COMMANDS:"), "{out}");
    assert!(!out.contains("deploy"), "{out}");
    let (_, out, _) = run(&fixture(), &["--help-management"]);
    assert!(
        out.contains("MANAGEMENT:") && out.contains("completion"),
        "{out}"
    );
    assert!(!out.contains("deploy"), "{out}");
}

#[test]
fn command_help_lists_arguments_and_flags() {
    let (code, out, _) = run(&fixture(), &["deploy", "--help"]);
    assert_eq!(code, 0);
    assert!(out.starts_with("Usage: prog deploy <target>"), "{out}");
    assert!(out.contains("Deploy the app"), "{out}");
    assert!(section(&out, "ARGUMENTS")
        .iter()
        .any(|l| l.contains("target") && l.contains("Where to deploy")));
    let flags = section(&out, "FLAGS");
    assert!(
        flags
            .iter()
            .any(|l| l.contains("-e, --env") && l.contains("Target env")),
        "{flags:?}"
    );
    assert!(!out.contains("COMMANDS:"), "{out}");
}

#[test]
fn nested_command_help_lists_subcommands() {
    let (_, out, _) = run(&fixture(), &["remote", "--help"]);
    assert!(out.starts_with("Usage: prog remote [command]"), "{out}");
    let commands = section(&out, "COMMANDS");
    assert!(commands
        .iter()
        .any(|l| l.trim_start().starts_with("add") && l.contains("Add a remote")));
}

#[test]
fn bare_group_command_prints_its_help() {
    let (code, out, _) = run(&fixture(), &["remote"]);
    assert_eq!(code, 0);
    assert!(out.starts_with("Usage: prog remote"), "{out}");
}

#[test]
fn help_topic_command_path() {
    let (code, out, _) = run(&fixture(), &["help", "remote", "add"]);
    assert_eq!(code, 0, "{out}");
    assert!(out.starts_with("Usage: prog remote add"), "{out}");
    assert!(out.contains("--fetch"), "{out}");
    let (_, via_flag, _) = run(&fixture(), &["remote", "add", "--help"]);
    assert_eq!(out, via_flag);
}

#[test]
fn help_topic_group_and_all() {
    let (code, out, _) = run(&fixture(), &["help", "management"]);
    assert_eq!(code, 0);
    let (_, flag, _) = run(&fixture(), &["--help-management"]);
    assert_eq!(out, flag);
    let (code, out, _) = run(&fixture(), &["help", "all"]);
    assert_eq!(code, 0);
    assert!(out.contains("MANAGEMENT:"), "{out}");
    // `help all` leaves the plumbing flags out.
    assert!(!section(&out, "FLAGS").iter().any(|l| l.contains("--chdir")));
}

#[test]
fn help_topic_unknown_is_usage_error() {
    let (code, out, err) = run(&fixture(), &["help", "nosuch"]);
    assert_eq!(code, 2);
    assert!(out.is_empty(), "{out}");
    assert!(err.contains("unknown help topic \"nosuch\""), "{err}");
    let (code, _, err) = run(&fixture(), &["help", "remote", "nosuch"]);
    assert_eq!(code, 2);
    assert!(
        err.contains("unknown help topic \"remote nosuch\""),
        "{err}"
    );
}

#[test]
fn help_topic_command_wins_over_group() {
    let mut root = fixture();
    root.add_command(
        CommandSpec::new("extras")
            .about("Extras command")
            .handler(|_, _, _| Ok(())),
    );
    let (code, out, _) = run(&root, &["help", "extras"]);
    assert_eq!(code, 0);
    assert!(out.starts_with("Usage: prog extras"), "{out}");
}

#[test]
fn bare_help_prints_root_help() {
    let (code, out, _) = run(&fixture(), &["help"]);
    assert_eq!(code, 0);
    assert_eq!(out, fixture().render_help());
}

#[test]
fn help_subcommand_is_not_advertised() {
    let root = fixture();
    for help in [root.render_help(), root.render_help_all()] {
        assert!(
            !help.lines().any(|l| l.trim_start().starts_with("help ")),
            "{help}"
        );
    }
}

#[test]
fn an_adopter_help_command_is_not_shadowed() {
    let mut root = fixture();
    root.add_command(CommandSpec::new("help").about("Mine").handler(|_, out, _| {
        writeln!(out, "mine").ok();
        Ok(())
    }));
    let (code, out, _) = run(&root, &["help"]);
    assert_eq!(code, 0);
    assert_eq!(out, "mine\n");
}

// --- theme ---

#[test]
fn styled_help_colors_titles_commands_and_flags() {
    let root = fixture();
    let theme = Theme::new("");
    let help = root.render_help_styled(&theme);
    let want_cmd = format!("\x1b[38;2;{};{};{}m", 0x7E, 0xD9, 0x57);
    let want_flag = format!("\x1b[38;2;{};{};{}m", 0xFF, 0x00, 0xFF);
    assert!(help.contains(&want_cmd), "{help:?}");
    assert!(help.contains(&want_flag), "{help:?}");
    assert!(help.contains("\x1b[0m"), "{help:?}");
}

#[test]
fn styled_help_strips_to_the_plain_layout() {
    let root = fixture();
    let styled = root.render_help_styled(&Theme::new(""));
    assert_eq!(strip_ansi(&styled), root.render_help());
}

#[test]
fn accent_overrides_command_color_only() {
    let t = Theme::new("#112233");
    assert_eq!(t.palette.command, "#112233");
    assert_eq!(t.palette.flag, Palette::NEON.flag);
    assert_eq!(Theme::new("").palette, Palette::NEON);
}

#[test]
fn builtin_palettes_match_go() {
    for (p, command, flag) in [
        (Palette::NEON, "#7ED957", "#FF00FF"),
        (Palette::DARK, "#C1FF72", "#FF66C4"),
        (Palette::BAUHAUS, "#FF00AA", "#0000FF"),
    ] {
        assert_eq!(p.command, command);
        assert_eq!(p.flag, flag);
    }
}

#[test]
fn color_is_off_for_a_non_terminal_writer() {
    // `run` writes to a Vec: never a terminal, so no escapes even with
    // a theme installed.
    let mut root = fixture();
    root.set_help_theme(Theme::new(""));
    let (_, out, _) = run(&root, &["--help"]);
    assert!(!out.contains('\x1b'), "{out:?}");
}

fn strip_ansi(s: &str) -> String {
    let mut out = String::new();
    let mut chars = s.chars().peekable();
    while let Some(c) = chars.next() {
        if c == '\x1b' && chars.peek() == Some(&'[') {
            for n in chars.by_ref() {
                if n.is_ascii_alphabetic() {
                    break;
                }
            }
        } else {
            out.push(c);
        }
    }
    out
}
