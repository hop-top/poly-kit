//! The desktop notification sink: ports of the Go `osnotify_test.go`
//! and `integration_test.go`.

#![cfg(feature = "notify-os")]

mod notify_support;

use std::io;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use hop_top_kit::breaker::{Breaker, Record};
use hop_top_kit::bus::{BusError, Context, Sink};
use hop_top_kit::notify::osnotify::{ExecRunner, OsNotifyError, OsNotifySink, Platform, Runner};
use hop_top_kit::notify::{is_broken_circuit, LiteralTemplate, TextTemplate};
use hop_top_kit::redact::Redactor;
use notify_support::event;
use serde_json::json;

type Call = (String, Vec<String>);

#[derive(Clone, Default)]
struct Fake {
    calls: Arc<Mutex<Vec<Call>>>,
    err: Option<String>,
}

impl Runner for Fake {
    fn run(&self, _: &Context, name: &str, args: &[String]) -> io::Result<()> {
        self.calls
            .lock()
            .unwrap()
            .push((name.to_string(), args.to_vec()));
        self.err
            .as_ref()
            .map_or(Ok(()), |m| Err(io::Error::other(m.clone())))
    }
}

impl Fake {
    fn count(&self) -> usize {
        self.calls.lock().unwrap().len()
    }

    fn last(&self) -> Call {
        self.calls.lock().unwrap().last().cloned().expect("no call")
    }
}

fn ev() -> hop_top_kit::bus::Event {
    let mut e = event("kit.notify.test", json!({"msg": "hello"}));
    e.source = "test".into();
    e
}

fn sink(p: Platform, r: &Fake) -> OsNotifySink {
    OsNotifySink::for_platform(p)
        .title(LiteralTemplate::new("t"))
        .text(LiteralTemplate::new("x"))
        .runner(r.clone())
}

fn drain(s: &OsNotifySink) -> Result<(), BusError> {
    s.drain(&Context::background(), &ev())
}

#[test]
fn new_matches_the_host_platform() {
    match (std::env::consts::OS, OsNotifySink::new()) {
        ("macos", Ok(_)) => {}
        ("linux", Ok(_)) => {}
        ("linux", Err(e)) => assert_eq!(e, OsNotifyError::NotifySendMissing),
        ("windows", Err(e)) => assert_eq!(e, OsNotifyError::Windows),
        (os, Err(OsNotifyError::Unsupported(got))) => assert_eq!(got, os),
        (os, other) => panic!("{os}: unexpected {:?}", other.err()),
    }
}

#[cfg(target_os = "linux")]
#[test]
fn new_probes_notify_send_on_path() {
    use std::os::unix::fs::PermissionsExt;
    let saved = std::env::var_os("PATH");

    std::env::set_var("PATH", "");
    let err = OsNotifySink::new().err().unwrap();
    assert_eq!(err, OsNotifyError::NotifySendMissing);
    assert!(err.to_string().contains("notify-send"));

    let dir = std::env::temp_dir().join(format!("kit-notify-os-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let stub = dir.join("notify-send");
    std::fs::write(&stub, "#!/bin/sh\nexit 0\n").unwrap();
    std::fs::set_permissions(&stub, std::fs::Permissions::from_mode(0o755)).unwrap();
    std::env::set_var("PATH", &dir);
    let found = OsNotifySink::new().is_ok();

    match saved {
        Some(p) => std::env::set_var("PATH", p),
        None => std::env::remove_var("PATH"),
    }
    std::fs::remove_dir_all(&dir).ok();
    assert!(found);
}

#[test]
fn error_texts() {
    assert_eq!(
        OsNotifyError::Windows.to_string(),
        "osnotify: not supported on windows in MVP"
    );
    assert_eq!(
        OsNotifyError::Unsupported("plan9".into()).to_string(),
        r#"osnotify: unsupported platform "plan9""#
    );
}

#[test]
fn command_darwin() {
    let s = OsNotifySink::for_platform(Platform::Darwin);
    let (name, args) = s.command("the title", "the body");
    assert_eq!(name, "osascript");
    // Title and text are argv of the script, never part of its source.
    assert_eq!(
        args,
        [
            "-e",
            "on run argv",
            "-e",
            "display notification (item 1 of argv) with title (item 2 of argv)",
            "-e",
            "end run",
            "--",
            "the body",
            "the title"
        ]
    );
}

#[test]
fn command_linux() {
    let (name, args) = OsNotifySink::for_platform(Platform::Linux).command("hello", "world");
    assert_eq!(name, "notify-send");
    assert_eq!(args, ["--", "hello", "world"]);
}

#[test]
fn drain_darwin_passes_text_as_argv() {
    let r = Fake::default();
    let s = OsNotifySink::for_platform(Platform::Darwin)
        .title(LiteralTemplate::new(r#"a "title" \ here"#))
        .text(LiteralTemplate::new("body"))
        .runner(r.clone());
    drain(&s).unwrap();
    let (name, args) = r.last();
    assert_eq!(name, "osascript");
    // Quotes and backslashes need no escaping: they are argv.
    assert_eq!(&args[6..], ["--", "body", r#"a "title" \ here"#]);
    assert!(!args[..6].iter().any(|a| a.contains("here")));
}

#[test]
fn drain_linux_runs_notify_send() {
    let r = Fake::default();
    let s = OsNotifySink::for_platform(Platform::Linux)
        .title(LiteralTemplate::new("kit alert"))
        .text(LiteralTemplate::new("queue depth high"))
        .runner(r.clone());
    drain(&s).unwrap();
    assert_eq!(
        r.last(),
        (
            "notify-send".to_string(),
            vec!["--".into(), "kit alert".into(), "queue depth high".into()]
        )
    );
}

#[test]
fn missing_title_or_text_is_a_drain_error() {
    let r = Fake::default();
    let no_title = OsNotifySink::for_platform(Platform::Darwin)
        .text(LiteralTemplate::new("x"))
        .runner(r.clone());
    let no_text = OsNotifySink::for_platform(Platform::Darwin)
        .title(LiteralTemplate::new("x"))
        .runner(r.clone());
    for s in [no_title, no_text] {
        assert!(drain(&s)
            .unwrap_err()
            .to_string()
            .contains("templates required"));
    }
    assert_eq!(r.count(), 0);
}

#[test]
fn runner_error_is_wrapped_with_the_command() {
    let r = Fake {
        err: Some("boom".into()),
        ..Fake::default()
    };
    let err = drain(&sink(Platform::Linux, &r)).unwrap_err().to_string();
    assert_eq!(err, "osnotify: run notify-send: boom");
}

#[test]
fn redactor_runs_on_title_and_text() {
    let mut red = Redactor::new();
    red.add_rule("secret", "topsecret", "***").unwrap();
    let r = Fake::default();
    let s = OsNotifySink::for_platform(Platform::Linux)
        .title(LiteralTemplate::new("title topsecret"))
        .text(LiteralTemplate::new("body topsecret end"))
        .redactor(Arc::new(red))
        .runner(r.clone());
    drain(&s).unwrap();
    let (_, args) = r.last();
    assert_eq!(
        args,
        ["--", "title ***REDACTED***", "body ***REDACTED*** end"]
    );
}

fn breaker(name: &str) -> Arc<Breaker> {
    Arc::new(
        Breaker::builder(name)
            .logger(Arc::new(|_: &Record| {}))
            .build(),
    )
}

#[test]
fn open_circuit_skips_the_runner() {
    let r = Fake::default();
    let b = breaker("osnotify-test-open");
    b.trip("test");
    let err = drain(&sink(Platform::Linux, &r).breaker(b)).unwrap_err();
    assert!(is_broken_circuit(&err), "{err}");
    assert_eq!(r.count(), 0);
}

#[test]
fn closed_circuit_allows_the_call() {
    let r = Fake::default();
    drain(&sink(Platform::Linux, &r).breaker(breaker("osnotify-test-closed"))).unwrap();
    assert_eq!(r.count(), 1);
}

#[test]
fn text_template_renders_the_event() {
    let t = TextTemplate::parse("{{.Source}}/{{.Topic}}").unwrap();
    let r = Fake::default();
    let s = OsNotifySink::for_platform(Platform::Linux)
        .title(t)
        .text(LiteralTemplate::new("x"))
        .runner(r.clone());
    drain(&s).unwrap();
    assert_eq!(r.last().1[1], "test/kit.notify.test");
}

#[test]
fn close_is_a_no_op() {
    assert!(OsNotifySink::for_platform(Platform::Linux).close().is_ok());
}

#[cfg(unix)]
mod exec {
    use super::*;

    fn run(ctx: &Context, name: &str, args: &[&str]) -> io::Result<()> {
        let args: Vec<String> = args.iter().map(ToString::to_string).collect();
        ExecRunner.run(ctx, name, &args)
    }

    #[test]
    fn success_and_failure_exit_codes() {
        let ctx = Context::background();
        run(&ctx, "true", &[]).unwrap();
        let err = run(&ctx, "false", &[]).unwrap_err().to_string();
        assert!(err.contains("exit status"), "{err}");
    }

    #[test]
    fn a_missing_binary_is_an_error() {
        assert!(run(&Context::background(), "kit-notify-no-such-binary", &[]).is_err());
    }

    #[test]
    fn arguments_are_not_interpreted_by_a_shell() {
        // `test` receives the whole string as one operand: non-empty.
        run(
            &Context::background(),
            "test",
            &["-n", "$(touch /nonexistent/x); `x`"],
        )
        .unwrap();
    }

    #[test]
    fn cancellation_kills_the_child() {
        let (ctx, cancel) = Context::background().with_cancel();
        let t = std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(50));
            cancel.cancel();
        });
        let start = Instant::now();
        let err = run(&ctx, "sleep", &["5"]).unwrap_err().to_string();
        t.join().unwrap();
        assert!(start.elapsed() < Duration::from_secs(2));
        assert!(err.contains("context canceled"), "{err}");
    }

    #[test]
    fn a_done_context_never_spawns() {
        let (ctx, cancel) = Context::background().with_cancel();
        cancel.cancel();
        assert!(run(&ctx, "true", &[]).is_err());
    }
}

/// Pops a real notification: opt in with `--ignored`.
#[cfg(any(target_os = "macos", target_os = "linux"))]
#[test]
#[ignore = "shows a real desktop notification"]
fn real_notification() {
    let s = OsNotifySink::new()
        .unwrap()
        .title(LiteralTemplate::new("kit notify e2e"))
        .text(LiteralTemplate::new(
            "hello from the osnotify integration test",
        ));
    let (ctx, _c) = Context::background().with_timeout(Duration::from_secs(10));
    s.drain(&ctx, &ev()).unwrap();
}

#[test]
fn leading_dash_is_never_an_option() {
    let (_, linux) = OsNotifySink::for_platform(Platform::Linux).command("-u critical", "--hint=x");
    assert_eq!(linux, ["--", "-u critical", "--hint=x"]);
    let (_, mac) = OsNotifySink::for_platform(Platform::Darwin).command("-e evil", "-x");
    let dd = mac.iter().position(|a| a == "--").expect("-- present");
    assert_eq!(&mac[dd + 1..], ["-x", "-e evil"]);
}

/// The real osascript takes a hostile title and text as plain values.
#[cfg(target_os = "macos")]
#[test]
fn osascript_receives_hostile_text_as_argv() {
    let s = OsNotifySink::for_platform(Platform::Darwin);
    let (name, mut args) = s.command(
        r#"-x" & (do shell script "touch /nonexistent/x") & ""#,
        r"\",
    );
    // Same invocation, but return the values instead of notifying.
    args[3] = "return (item 1 of argv) & \"|\" & (item 2 of argv)".into();
    let out = std::process::Command::new(name)
        .args(&args)
        .output()
        .unwrap();
    assert!(
        out.status.success(),
        "{}",
        String::from_utf8_lossy(&out.stderr)
    );
    assert_eq!(
        String::from_utf8_lossy(&out.stdout).trim_end(),
        r#"\|-x" & (do shell script "touch /nonexistent/x") & ""#
    );
}
