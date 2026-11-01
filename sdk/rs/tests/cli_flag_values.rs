//! A value-taking flag consumes the next token even when it starts with
//! `-`, as Go's pflag does: `--tag -crew` removes a tag, `--n -5` is
//! minus five. A switch and a flag given as `--name` alone are not
//! affected.

#![cfg(feature = "cli")]
#![allow(clippy::result_large_err)]

use std::sync::{Arc, Mutex};

use hop_top_kit::cli::{CommandSpec, Config, FlagSpec, Root};

type Seen = Arc<Mutex<Vec<(String, Vec<String>)>>>;

fn tool() -> (Root, Seen) {
    let seen: Seen = Arc::default();
    let sink = seen.clone();
    let mut cfg = Config::new("t", "0.0.1", "t");
    cfg.disable_validate = true;
    let mut root = Root::with_config(cfg);
    root.add_command(
        CommandSpec::new("go")
            .about("go")
            .flag(FlagSpec::strings("tag"))
            .flag(FlagSpec::string("name"))
            .flag(FlagSpec::int("n"))
            .flag(FlagSpec::bool("preview"))
            .handler(move |inv, _, _| {
                let mut s = sink.lock().unwrap();
                s.extend(inv.flags.iter().map(|(k, v)| (k.clone(), v.clone())));
                Ok(())
            }),
    );
    (root, seen)
}

fn run(args: &[&str]) -> (i32, Seen, String) {
    let (root, seen) = tool();
    let argv: Vec<String> = std::iter::once("t")
        .chain(args.iter().copied())
        .map(Into::into)
        .collect();
    let (mut out, mut err) = (Vec::new(), Vec::new());
    let code = root.run(argv, &mut out, &mut err);
    (code, seen, String::from_utf8(err).unwrap())
}

fn got(seen: &Seen, name: &str) -> Vec<String> {
    let s = seen.lock().unwrap();
    s.iter()
        .find(|(k, _)| k == name)
        .map(|(_, v)| v.clone())
        .unwrap_or_default()
}

#[test]
fn a_string_flag_takes_a_dash_value() {
    let (code, seen, err) = run(&["go", "--name", "-crew"]);
    assert_eq!(code, 0, "{err}");
    assert_eq!(got(&seen, "name"), ["-crew"]);
}

#[test]
fn a_repeatable_flag_takes_dash_values_in_order() {
    let (code, seen, err) = run(&["go", "--tag", "crew", "--tag", "-crew", "--tag", "=a,b"]);
    assert_eq!(code, 0, "{err}");
    assert_eq!(got(&seen, "tag"), ["crew", "-crew", "=a,b"]);
}

#[test]
fn an_int_flag_takes_a_negative_number() {
    let (code, seen, err) = run(&["go", "--n", "-5"]);
    assert_eq!(code, 0, "{err}");
    assert_eq!(got(&seen, "n"), ["-5"]);
}

#[test]
fn a_switch_does_not_swallow_the_next_flag() {
    let (code, seen, err) = run(&["go", "--preview", "--name", "x"]);
    assert_eq!(code, 0, "{err}");
    assert_eq!(got(&seen, "preview"), ["true"]);
    assert_eq!(got(&seen, "name"), ["x"]);
}

#[test]
fn a_value_flag_at_the_end_is_still_a_missing_value() {
    let (code, _, err) = run(&["go", "--name"]);
    assert_eq!(code, 2);
    assert!(err.contains("missing value for --name"), "{err}");
}
