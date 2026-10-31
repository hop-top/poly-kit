//! The logger of a kit root: levels from the `-V` count, the `--quiet`
//! floor, themed `LEVEL msg k=v` lines on stderr (Go `console/log`,
//! `log_test.go`, `verbose_test.go`; parity.json `verbosity`).

#![cfg(feature = "cli")]

use std::sync::{Arc, Mutex};

use hop_top_kit::cli::log::{Level, Logger, Verbosity};
use hop_top_kit::cli::Globals;

#[derive(Clone, Default)]
struct Sink(Arc<Mutex<Vec<u8>>>);

impl std::io::Write for Sink {
    fn write(&mut self, b: &[u8]) -> std::io::Result<usize> {
        self.0.lock().unwrap().extend_from_slice(b);
        Ok(b.len())
    }
    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

impl Sink {
    fn text(&self) -> String {
        String::from_utf8(self.0.lock().unwrap().clone()).unwrap()
    }
}

fn globals(quiet: bool, verbose: u8) -> Globals {
    Globals {
        quiet,
        verbose,
        ..Globals::default()
    }
}

fn logger(g: &Globals) -> (Logger, Sink) {
    let sink = Sink::default();
    let l = Logger::from_globals(g)
        .with_output(sink.clone())
        .with_color(false);
    (l, sink)
}

#[test]
fn verbosity_counts_map_through_the_contract() {
    let v = Verbosity::contract();
    assert_eq!(v.level(0), Level::Info);
    assert_eq!(v.level(1), Level::Debug);
    assert_eq!(v.level(2), Level::Trace);
    // Counts past the highest key clamp to it.
    assert_eq!(v.level(9), Level::Trace);
    assert_eq!(v.quiet_override(), Level::Warn);
}

#[test]
fn default_level_is_info() {
    let (l, out) = logger(&globals(false, 0));
    l.debug("hidden", &[]);
    l.info("shown", &[]);
    assert_eq!(out.text(), "INFO shown\n");
}

#[test]
fn verbose_count_lowers_the_threshold() {
    let (l, out) = logger(&globals(false, 1));
    l.trace("t", &[]);
    l.debug("d", &[]);
    assert_eq!(out.text(), "DEBU d\n");
    let (l, out) = logger(&globals(false, 2));
    l.trace("t", &[]);
    assert_eq!(out.text(), "TRAC t\n");
}

#[test]
fn quiet_floors_the_level_and_wins_over_verbose() {
    let (l, out) = logger(&globals(true, 2));
    l.debug("d", &[]);
    l.info("i", &[]);
    l.warn("w", &[]);
    l.error("e", &[]);
    assert_eq!(out.text(), "WARN w\nERRO e\n");
}

#[test]
fn quiet_only_raises_never_lowers() {
    let sink = Sink::default();
    let l = Logger::with_level(&globals(true, 0), Level::Error)
        .with_output(sink.clone())
        .with_color(false);
    l.warn("w", &[]);
    l.error("e", &[]);
    assert_eq!(sink.text(), "ERRO e\n");
}

#[test]
fn key_values_follow_the_message_quoted_when_needed() {
    let (l, out) = logger(&globals(false, 0));
    l.info(
        "starting",
        &[("port", &8080), ("name", &"a b"), ("empty", &"")],
    );
    assert_eq!(
        out.text(),
        "INFO starting port=8080 name=\"a b\" empty=\"\"\n"
    );
}

#[test]
fn success_logs_at_info_with_its_own_label() {
    let (l, out) = logger(&globals(false, 0));
    l.success("deployed", &[("env", &"prod")]);
    assert_eq!(out.text(), "SUCC deployed env=prod\n");
    // Filtered exactly like info.
    let (l, out) = logger(&globals(true, 0));
    l.success("deployed", &[]);
    assert_eq!(out.text(), "");
}

#[test]
fn colored_prefixes_use_the_theme_and_strip_with_no_color() {
    let sink = Sink::default();
    let l = Logger::from_globals(&globals(false, 0))
        .with_output(sink.clone())
        .with_color(true);
    l.error("boom", &[]);
    l.info("hi", &[]);
    let text = sink.text();
    // ERRO bold #FF388B; INFO #858392 not bold.
    assert!(
        text.contains("\x1b[1m\x1b[38;2;255;56;139mERRO\x1b[0m boom"),
        "{text:?}"
    );
    assert!(
        text.contains("\x1b[38;2;133;131;146mINFO\x1b[0m hi"),
        "{text:?}"
    );
}

#[test]
fn no_color_global_disables_color() {
    let g = Globals {
        no_color: true,
        ..Globals::default()
    };
    assert!(!Logger::from_globals(&g).color());
}

#[test]
fn invocation_exposes_the_verbosity_accessors() {
    use hop_top_kit::cli::Invocation;
    // Invocation has crate-private fields, so no struct-update syntax here.
    #[allow(clippy::field_reassign_with_default)]
    let inv = {
        let mut inv = Invocation::default();
        inv.globals = globals(true, 2);
        inv
    };
    assert_eq!(inv.verbose_count(), 2);
    assert!(inv.is_quiet());
    assert!(!Invocation::default().is_quiet());
}
