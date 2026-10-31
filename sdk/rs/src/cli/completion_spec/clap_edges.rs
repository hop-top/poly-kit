//! Spec-export rules that only a raw clap tree can state: a short-only
//! flag and an optional value without `require_equals`. They test the
//! adapter's walk, so they live beside it and build the clap tree
//! directly; the kit-typed builder cannot express either.

use clap::{Arg, ArgAction, Command};

use super::{render_fig, render_usage, Spec};
use crate::cli::Root;

/// A short-only flag has no long name to offer.
#[test]
fn short_only_flag() {
    let cmd = Command::new("tool").arg(Arg::new("quiet").short('q').action(ArgAction::SetTrue));
    let mut root = Root::from_clap(cmd);
    root.disable_completion_command();
    let out = render_usage(&Spec::from_root(&root));
    assert!(out.contains("\nflag \"-q\"\n"), "{out}");
}

/// A clap flag whose value may be left out but is not attached-only
/// (no `require_equals`, as the output flags' `--format-help`) binds
/// the next word: a normal value flag, candidates kept.
#[test]
fn optional_value_without_require_equals_is_a_normal_value() {
    let cmd = Command::new("tool")
        .arg(
            Arg::new("format-help")
                .long("format-help")
                .help("Show available formats and their options")
                .num_args(0..=1)
                .require_equals(false)
                .global(true)
                .hide(true),
        )
        .arg(
            Arg::new("many")
                .long("many")
                .num_args(0..)
                .value_parser(["a", "b"]),
        );
    let m = cmd
        .clone()
        .try_get_matches_from(["tool", "--format-help", "json"])
        .unwrap();
    assert_eq!(
        m.get_one::<String>("format-help").map(String::as_str),
        Some("json")
    );
    let mut root = Root::from_clap(cmd);
    root.disable_completion_command();
    let spec = Spec::from_root(&root);

    let usage = render_usage(&spec);
    assert!(
        usage.contains(
            "\nflag \"--format-help <string>\" help=\"Show available formats and their options\" global=#true hide=#true\n"
        ),
        "{usage}"
    );
    assert!(
        usage.contains("\nflag \"--many <string>\" {\n    choices strict=#false \"a\" \"b\"\n}\n"),
        "{usage}"
    );
    assert!(!usage.contains("require_equals"), "{usage}");
    assert!(!usage.contains("default_missing"), "{usage}");

    let fig = render_fig(&spec);
    assert!(
        fig.contains(
            "    {\n      name: \"--format-help\",\n      description: \"Show available formats and their options\",\n      hidden: true,\n      isPersistent: true,\n      args: {\n        name: \"string\",\n      },\n    },\n"
        ),
        "{fig}"
    );
    assert!(
        fig.contains("      args: {\n        name: \"string\",\n        suggestions: [\"a\", \"b\"],\n      },\n"),
        "{fig}"
    );
    assert!(!fig.contains("requiresSeparator"), "{fig}");
}
