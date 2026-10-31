//! Next-step hints: registry, enablement and rendering (Go
//! `console/output/hint.go`, `hint_test.go`; sdk/ts `hint.ts`).

#![cfg(feature = "output")]

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, MutexGuard};

use hop_top_kit::output::hint::{
    active, hints_enabled, register_upgrade_hints, register_version_hints, render_hints, Hint,
    HintOptions, HintSet, HintStyle,
};

static ENV: Mutex<()> = Mutex::new(());

fn lock() -> MutexGuard<'static, ()> {
    ENV.lock().unwrap_or_else(|e| e.into_inner())
}

fn style(tty: bool, no_color: bool) -> HintStyle {
    HintStyle {
        tty,
        no_color,
        muted: "#858183".into(),
    }
}

#[test]
fn lookup_returns_a_copy_and_register_appends() {
    let s = HintSet::new();
    assert!(s.lookup("x").is_empty());
    s.register("x", [Hint::new("one")]);
    s.register("x", [Hint::new("two"), Hint::new("three")]);
    let got = s.lookup("x");
    let msgs: Vec<_> = got.iter().map(|h| h.message.as_str()).collect();
    assert_eq!(msgs, ["one", "two", "three"]);
}

#[test]
fn active_filters_on_conditions() {
    let yes = Hint::new("y").when(|| true);
    let no = Hint::new("n").when(|| false);
    let always = Hint::new("a");
    let got = active(&[yes, no, always]);
    let msgs: Vec<_> = got.iter().map(|h| h.message.as_str()).collect();
    assert_eq!(msgs, ["y", "a"]);
}

#[test]
fn enabled_unless_a_switch_says_otherwise() {
    let _g = lock();
    std::env::remove_var("HOP_QUIET_HINTS");
    assert!(hints_enabled(&HintOptions::default()));
    assert!(!hints_enabled(&HintOptions {
        no_hints: true,
        ..HintOptions::default()
    }));
    assert!(!hints_enabled(&HintOptions {
        quiet: true,
        ..HintOptions::default()
    }));
    assert!(!hints_enabled(&HintOptions {
        config_enabled: Some(false),
        ..HintOptions::default()
    }));
    assert!(hints_enabled(&HintOptions {
        config_enabled: Some(true),
        ..HintOptions::default()
    }));
}

#[test]
fn quiet_hints_env_disables() {
    let _g = lock();
    // The only test touching the variable; the others clear it first.
    for (v, on) in [("1", false), ("true", false), ("yes", false), ("0", true)] {
        std::env::set_var("HOP_QUIET_HINTS", v);
        assert_eq!(hints_enabled(&HintOptions::default()), on, "{v}");
    }
    std::env::remove_var("HOP_QUIET_HINTS");
}

fn render(hints: &[Hint], format: &str, opts: &HintOptions, st: &HintStyle) -> String {
    let mut out = Vec::new();
    render_hints(&mut out, hints, format, opts, st).unwrap();
    String::from_utf8(out).unwrap()
}

#[test]
fn renders_an_arrow_line_per_active_hint_after_a_blank_line() {
    let _g = lock();
    std::env::remove_var("HOP_QUIET_HINTS");
    let hints = [
        Hint::new("Run `x version`."),
        Hint::new("skip").when(|| false),
    ];
    let got = render(&hints, "table", &HintOptions::default(), &style(true, true));
    assert_eq!(got, "\n→ Run `x version`.\n");
}

#[test]
fn colored_hints_use_the_muted_color() {
    let _g = lock();
    std::env::remove_var("HOP_QUIET_HINTS");
    let got = render(
        &[Hint::new("m")],
        "table",
        &HintOptions::default(),
        &style(true, false),
    );
    assert_eq!(got, "\n\x1b[38;2;133;129;131m→ m\x1b[0m\n");
}

#[test]
fn renders_nothing_off_table_off_a_tty_or_disabled() {
    let _g = lock();
    std::env::remove_var("HOP_QUIET_HINTS");
    let hints = [Hint::new("m")];
    let o = HintOptions::default();
    assert_eq!(render(&hints, "json", &o, &style(true, true)), "");
    assert_eq!(render(&hints, "table", &o, &style(false, true)), "");
    let off = HintOptions {
        no_hints: true,
        ..HintOptions::default()
    };
    assert_eq!(render(&hints, "table", &off, &style(true, true)), "");
    assert_eq!(render(&[], "table", &o, &style(true, true)), "");
}

#[test]
fn standard_hints_follow_their_flags() {
    let s = HintSet::new();
    let upgraded = Arc::new(AtomicBool::new(false));
    let avail = Arc::new(AtomicBool::new(false));
    let (u, a) = (upgraded.clone(), avail.clone());
    register_upgrade_hints(&s, "mytool", move || u.load(Ordering::SeqCst));
    register_version_hints(&s, "mytool", move || a.load(Ordering::SeqCst));
    assert!(active(&s.lookup("upgrade")).is_empty());
    assert!(active(&s.lookup("version")).is_empty());
    upgraded.store(true, Ordering::SeqCst);
    avail.store(true, Ordering::SeqCst);
    assert_eq!(
        active(&s.lookup("upgrade"))[0].message,
        "Run `mytool version` to verify."
    );
    assert_eq!(
        active(&s.lookup("version"))[0].message,
        "Run `mytool upgrade` to get latest."
    );
}
