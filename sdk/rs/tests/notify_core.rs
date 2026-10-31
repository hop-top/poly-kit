//! The notify core: severity, backoff, filter and retry. Ports of the
//! Go `notify_test.go`, `backoff_test.go`, `filter_test.go` and
//! `retry_test.go`.
//!
//! Not ported: `WithSeverity` typed payloads (a Rust payload is a JSON
//! value), `Severity(99).String()` (the enum has no such value) and the
//! nil-predicate test (no nil closure).

#![cfg(feature = "notify")]

mod notify_support;

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use hop_top_kit::bus::{BusError, Context, ContextError, Sink};
use hop_top_kit::notify::{
    exponential_backoff, is_broken_circuit, severity_of, FilterSink, RetrySink, Severity,
    BROKEN_CIRCUIT,
};
use notify_support::{event, with_severity, Scripted};
use serde_json::{json, Value};

const ALL: [Severity; 5] = [
    Severity::Debug,
    Severity::Info,
    Severity::Warn,
    Severity::Error,
    Severity::Critical,
];

fn sev(payload: Value) -> Severity {
    severity_of(&event("a.b.c.d", payload))
}

#[test]
fn severity_strings() {
    let want = ["debug", "info", "warn", "error", "critical"];
    for (s, w) in ALL.iter().zip(want) {
        assert_eq!(s.as_str(), w);
        assert_eq!(s.to_string(), w);
    }
}

#[test]
fn severity_orders_debug_to_critical() {
    assert!(ALL.windows(2).all(|w| w[0] < w[1]));
}

#[test]
fn severity_of_keyword() {
    for s in ALL {
        assert_eq!(sev(json!({"severity": s.as_str()})), s);
    }
}

#[test]
fn severity_of_integer_and_float() {
    for (i, s) in ALL.iter().enumerate() {
        assert_eq!(sev(json!({"severity": i})), *s);
        // The post-JSON-decode shape: numbers as floats.
        assert_eq!(sev(json!({"severity": i as f64})), *s);
    }
    assert_eq!(sev(json!({"severity": 3_i64})), Severity::Error);
}

#[test]
fn severity_of_invalid_keyword_is_info() {
    for kw in ["unknown", "INFO", "Warn", "panic", "", "FATAL"] {
        assert_eq!(sev(json!({"severity": kw})), Severity::Info, "{kw:?}");
    }
}

#[test]
fn severity_of_out_of_range_is_info() {
    for n in [-1, 5, 99, -100] {
        assert_eq!(sev(json!({"severity": n})), Severity::Info, "{n}");
    }
    assert_eq!(sev(json!({"severity": u64::MAX})), Severity::Info);
}

#[test]
fn severity_of_fractional_float_is_info() {
    assert_eq!(sev(json!({"severity": 2.5})), Severity::Info);
}

#[test]
fn severity_of_missing_or_wrong_type_is_info() {
    assert_eq!(sev(json!({"other": "value"})), Severity::Info);
    assert_eq!(sev(json!({"severity": ["warn"]})), Severity::Info);
    assert_eq!(sev(json!({"severity": null})), Severity::Info);
    assert_eq!(sev(json!({"severity": true})), Severity::Info);
}

#[test]
fn severity_of_non_object_payload_is_info() {
    assert_eq!(sev(Value::Null), Severity::Info);
    assert_eq!(sev(json!("critical")), Severity::Info);
    assert_eq!(sev(json!(4)), Severity::Info);
    assert_eq!(sev(json!([4])), Severity::Info);
}

#[test]
fn backoff_grows_without_jitter() {
    let base = Duration::from_millis(100);
    let b = exponential_backoff(base, 2.0, false);
    let mut prev = Duration::ZERO;
    for attempt in 0..=10 {
        let got = b(attempt);
        assert_eq!(got, base * 2u32.pow(attempt), "attempt {attempt}");
        assert!(attempt == 0 || got > prev);
        prev = got;
    }
}

#[test]
fn backoff_jitter_stays_below_the_bound() {
    let base = Duration::from_millis(100);
    let b = exponential_backoff(base, 2.0, true);
    for attempt in 0..=8 {
        let upper = base * 2u32.pow(attempt);
        for _ in 0..1000 {
            assert!(b(attempt) < upper, "attempt {attempt}");
        }
    }
}

#[test]
fn backoff_factor_one_is_flat() {
    let base = Duration::from_millis(100);
    let b = exponential_backoff(base, 1.0, false);
    for attempt in 0..=10 {
        assert_eq!(b(attempt), base);
    }
}

#[test]
fn backoff_zero_base_is_zero() {
    for jitter in [false, true] {
        let b = exponential_backoff(Duration::ZERO, 2.0, jitter);
        assert!((0..=10).all(|a| b(a).is_zero()));
    }
}

#[test]
fn backoff_huge_attempt_is_capped_not_wrapped() {
    let cap = Duration::from_nanos(i64::MAX as u64);
    for (base, factor, attempt) in [
        (Duration::from_millis(100), 2.0, 1000),
        (Duration::from_millis(100), 10.0, 400),
        (Duration::from_secs(1), 2.0, 63),
        (Duration::from_secs(1), 2.0, 100),
    ] {
        let d = exponential_backoff(base, factor, false)(attempt);
        // Zero for a product past f64 range, else at most the cap.
        assert!(d <= cap, "{base:?} {factor} {attempt}: {d:?}");
    }
    assert_eq!(
        exponential_backoff(Duration::from_secs(1), 2.0, false)(100),
        cap
    );
}

#[test]
fn backoff_degenerate_factors_are_zero() {
    let base = Duration::from_secs(1);
    for factor in [0.0, -2.0, f64::NAN, f64::INFINITY] {
        assert!(
            exponential_backoff(base, factor, false)(3).is_zero(),
            "{factor}"
        );
    }
}

#[test]
fn backoff_example_from_the_contract() {
    let b = exponential_backoff(Duration::from_millis(1), 2.0, false);
    assert_eq!(b(3), Duration::from_millis(8));
}

// Filter.

fn filtered(f: impl FnOnce(FilterSink) -> FilterSink) -> (FilterSink, Scripted) {
    let inner = Scripted::ok();
    (f(FilterSink::new(inner.clone())), inner)
}

fn drain(s: &dyn Sink, e: &hop_top_kit::bus::Event) -> Result<(), BusError> {
    s.drain(&Context::background(), e)
}

#[test]
fn filter_without_checks_passes_everything() {
    let (f, inner) = filtered(|f| f);
    for e in [
        event("a.b.c.d", Value::Null),
        with_severity("x.y.z.w", "debug"),
        event("p.q.r.s", json!({"k": 1})),
    ] {
        drain(&f, &e).unwrap();
    }
    assert_eq!(inner.calls(), 3);
}

#[test]
fn filter_topic_pattern_exact_star_and_hash() {
    let cases: [(&str, &str, bool); 6] = [
        ("a.b.c.d", "a.b.c.d", true),
        ("a.b.c.d", "a.b.c.e", false),
        ("a.*.c.d", "a.b.c.d", true),
        ("a.*.c.d", "a.b.x.d", false),
        ("a.#", "a.b.c.d", true),
        ("a.#", "z.b.c.d", false),
    ];
    for (pattern, topic, pass) in cases {
        let (f, inner) = filtered(|f| f.topic_pattern(pattern));
        drain(&f, &event(topic, Value::Null)).unwrap();
        assert_eq!(inner.calls(), usize::from(pass), "{pattern} vs {topic}");
    }
}

#[test]
fn filter_min_severity_floor() {
    let (f, inner) = filtered(|f| f.min_severity(Severity::Warn));
    for (kw, pass) in [
        ("critical", true),
        ("error", true),
        ("warn", true),
        ("info", false),
        ("debug", false),
    ] {
        let before = inner.calls();
        drain(&f, &with_severity("a.b.c.d", kw)).unwrap();
        assert_eq!(inner.calls() - before, usize::from(pass), "{kw}");
    }
    // No severity advertised reads as info, below the floor.
    drain(&f, &event("a.b.c.d", Value::Null)).unwrap();
    assert_eq!(inner.calls(), 3);
}

#[test]
fn filter_predicate() {
    let (f, inner) = filtered(|f| f.predicate(|e| e.source == "allowed"));
    let mut pass = event("a.b.c.d", Value::Null);
    pass.source = "allowed".into();
    let mut denied = pass.clone();
    denied.source = "denied".into();
    drain(&f, &pass).unwrap();
    drain(&f, &denied).unwrap();
    assert_eq!(inner.calls(), 1);
}

#[test]
fn filter_checks_compose() {
    let (f, inner) = filtered(|f| {
        f.topic_pattern("billing.#")
            .min_severity(Severity::Warn)
            .predicate(|e| e.source == "billing-svc")
    });
    let mk = |topic: &str, source: &str, sev: &str| {
        let mut e = with_severity(topic, sev);
        e.source = source.into();
        e
    };
    let cases = [
        (
            "all-pass",
            mk("billing.invoice.failed", "billing-svc", "error"),
            true,
        ),
        (
            "topic-fails",
            mk("shipping.label.created", "billing-svc", "error"),
            false,
        ),
        (
            "severity-fails",
            mk("billing.invoice.created", "billing-svc", "info"),
            false,
        ),
        (
            "predicate-fails",
            mk("billing.invoice.failed", "ops-cli", "error"),
            false,
        ),
    ];
    let mut want = 0;
    for (name, e, pass) in cases {
        drain(&f, &e).unwrap();
        want += usize::from(pass);
        assert_eq!(inner.calls(), want, "{name}");
    }
}

#[test]
fn filter_wrapping_filter_ands() {
    let inner = Scripted::ok();
    let pipeline = FilterSink::new(FilterSink::new(inner.clone()).min_severity(Severity::Warn))
        .topic_pattern("kit.#");
    for (topic, sev, pass) in [
        ("kit.foo", "critical", true),
        ("kit.bar", "info", false),
        ("other.bar", "critical", false),
    ] {
        let before = inner.calls();
        drain(&pipeline, &with_severity(topic, sev)).unwrap();
        assert_eq!(inner.calls() - before, usize::from(pass), "{topic} {sev}");
    }
}

#[test]
fn filter_propagates_inner_error() {
    let f = FilterSink::new(Scripted::failing(&[Some("inner failed")]));
    let err = drain(&f, &event("a.b.c.d", Value::Null)).unwrap_err();
    assert_eq!(err.to_string(), "inner failed");
}

#[test]
fn filter_rejection_is_silent_and_skips_inner() {
    let inner = Scripted::failing(&[Some("inner should not be called")]);
    let f = FilterSink::new(inner.clone()).min_severity(Severity::Critical);
    drain(&f, &event("a.b.c.d", Value::Null)).unwrap();
    assert_eq!(inner.calls(), 0);
}

#[test]
fn filter_close_delegates() {
    let inner = Scripted::ok();
    FilterSink::new(inner.clone()).close().unwrap();
    assert_eq!(inner.closes(), 1);
}

// Retry.

fn fast() -> impl Fn(u32) -> Duration + Send + Sync + 'static {
    |_| Duration::from_millis(1)
}

fn retry_drain(r: &RetrySink) -> Result<(), BusError> {
    drain(r, &event("a.b.c.d", Value::Null))
}

#[test]
fn retry_succeeds_first_try_without_backoff() {
    let inner = Scripted::ok();
    let calls = Arc::new(AtomicUsize::new(0));
    let c = calls.clone();
    let r = RetrySink::new(inner.clone()).backoff(move |_| {
        c.fetch_add(1, Ordering::SeqCst);
        Duration::ZERO
    });
    retry_drain(&r).unwrap();
    assert_eq!(inner.calls(), 1);
    assert_eq!(calls.load(Ordering::SeqCst), 0);
}

#[test]
fn retry_succeeds_after_n() {
    let inner = Scripted::failing(&[Some("transient"), Some("transient"), None]);
    let calls = Arc::new(AtomicUsize::new(0));
    let c = calls.clone();
    let r = RetrySink::new(inner.clone())
        .max_attempts(5)
        .backoff(move |_| {
            c.fetch_add(1, Ordering::SeqCst);
            Duration::from_millis(1)
        });
    retry_drain(&r).unwrap();
    assert_eq!(inner.calls(), 3);
    assert_eq!(calls.load(Ordering::SeqCst), 2);
}

#[test]
fn retry_exhausted_without_dead_letter_returns_last_error() {
    let inner = Scripted::failing(&[Some("always fails")]);
    let r = RetrySink::new(inner.clone())
        .max_attempts(3)
        .backoff(fast());
    let err = retry_drain(&r).unwrap_err();
    assert_eq!(err.to_string(), "always fails");
    assert_eq!(inner.calls(), 3);
}

#[test]
fn retry_exhausted_routes_to_dead_letter() {
    let inner = Scripted::failing(&[Some("inner fails")]);
    let dl = Scripted::ok();
    let r = RetrySink::new(inner.clone())
        .max_attempts(3)
        .backoff(fast())
        .dead_letter(dl.clone());
    let e = event("t.dl.x.y", json!("payload"));
    drain(&r, &e).unwrap();
    assert_eq!(inner.calls(), 3);
    assert_eq!(dl.calls(), 1);
    assert_eq!(dl.last(), e);
}

#[test]
fn retry_dead_letter_error_is_returned() {
    let inner = Scripted::failing(&[Some("inner fails")]);
    let dl = Scripted::failing(&[Some("DL fails too")]);
    let r = RetrySink::new(inner.clone())
        .max_attempts(2)
        .backoff(fast())
        .dead_letter(dl.clone());
    let err = retry_drain(&r).unwrap_err();
    assert_eq!(err.to_string(), "DL fails too");
    assert_eq!((inner.calls(), dl.calls()), (2, 1));
}

#[test]
fn retry_open_circuit_is_terminal() {
    // Wrapped, to show the match is on the text, not the whole message.
    let wrapped = format!("webhook: {BROKEN_CIRCUIT}");
    let inner = Scripted::failing(&[Some(&wrapped)]);
    let dl = Scripted::ok();
    let calls = Arc::new(AtomicUsize::new(0));
    let c = calls.clone();
    let r = RetrySink::new(inner.clone())
        .max_attempts(5)
        .backoff(move |_| {
            c.fetch_add(1, Ordering::SeqCst);
            Duration::ZERO
        })
        .dead_letter(dl.clone());
    retry_drain(&r).unwrap();
    assert_eq!((inner.calls(), dl.calls()), (1, 1));
    assert_eq!(calls.load(Ordering::SeqCst), 0);
}

#[test]
fn retry_open_circuit_without_dead_letter_returns_the_error() {
    let inner = Scripted::failing(&[Some(BROKEN_CIRCUIT)]);
    let r = RetrySink::new(inner.clone())
        .max_attempts(5)
        .backoff(fast());
    let err = retry_drain(&r).unwrap_err();
    assert!(is_broken_circuit(&err));
    assert_eq!(inner.calls(), 1);
}

#[test]
fn retry_open_circuit_after_partial_failures() {
    let inner = Scripted::failing(&[
        Some("transient"),
        Some("transient"),
        Some(BROKEN_CIRCUIT),
        None,
    ]);
    let dl = Scripted::ok();
    let calls = Arc::new(AtomicUsize::new(0));
    let c = calls.clone();
    let r = RetrySink::new(inner.clone())
        .max_attempts(10)
        .backoff(move |_| {
            c.fetch_add(1, Ordering::SeqCst);
            Duration::from_millis(1)
        })
        .dead_letter(dl.clone());
    retry_drain(&r).unwrap();
    assert_eq!(inner.calls(), 3);
    assert_eq!(calls.load(Ordering::SeqCst), 2);
    assert_eq!(dl.calls(), 1);
}

#[test]
fn broken_circuit_text_is_the_breakers() {
    #[cfg(feature = "breaker")]
    assert_eq!(
        hop_top_kit::breaker::BrokenCircuit.to_string(),
        BROKEN_CIRCUIT
    );
    assert!(is_broken_circuit(&BusError::handler(BROKEN_CIRCUIT)));
    assert!(!is_broken_circuit(&BusError::handler("other")));
    assert!(!is_broken_circuit(&BusError::Closed));
}

#[test]
fn retry_cancel_during_sleep_wakes_early() {
    let inner = Scripted::failing(&[Some("transient")]);
    let (ctx, cancel) = Context::background().with_cancel();
    let (tx, rx) = std::sync::mpsc::channel::<()>();
    let tx = Mutex::new(tx);
    let r = RetrySink::new(inner.clone())
        .max_attempts(5)
        .backoff(move |_| {
            // The sleep is about to start: cancel from another thread.
            tx.lock().unwrap().send(()).ok();
            Duration::from_secs(10)
        });
    let t = std::thread::spawn(move || {
        rx.recv().unwrap();
        cancel.cancel();
    });
    let start = Instant::now();
    let err = r.drain(&ctx, &event("a.b.c.d", Value::Null)).unwrap_err();
    t.join().unwrap();
    assert!(
        matches!(err, BusError::Context(ContextError::Canceled)),
        "{err}"
    );
    assert_eq!(inner.calls(), 1);
    assert!(
        start.elapsed() < Duration::from_secs(1),
        "{:?}",
        start.elapsed()
    );
}

#[test]
fn retry_deadline_wakes_the_sleep() {
    let inner = Scripted::failing(&[Some("transient")]);
    let (ctx, _cancel) = Context::background().with_timeout(Duration::from_millis(30));
    let r = RetrySink::new(inner.clone())
        .max_attempts(5)
        .backoff(|_| Duration::from_secs(10));
    let start = Instant::now();
    let err = r.drain(&ctx, &event("a.b.c.d", Value::Null)).unwrap_err();
    assert!(
        matches!(err, BusError::Context(ContextError::DeadlineExceeded)),
        "{err}"
    );
    assert!(start.elapsed() < Duration::from_secs(1));
}

#[test]
fn retry_already_canceled_context_short_circuits() {
    let inner = Scripted::failing(&[Some("inner ignored canceled ctx")]);
    let (ctx, cancel) = Context::background().with_cancel();
    cancel.cancel();
    let r = RetrySink::new(inner.clone())
        .max_attempts(5)
        .backoff(fast());
    let err = r.drain(&ctx, &event("a.b.c.d", Value::Null)).unwrap_err();
    assert!(
        matches!(err, BusError::Context(ContextError::Canceled)),
        "{err}"
    );
    assert_eq!(inner.calls(), 1);
}

#[test]
fn retry_zero_attempts_behave_as_one() {
    let inner = Scripted::failing(&[Some("inner fails")]);
    let r = RetrySink::new(inner.clone())
        .max_attempts(0)
        .backoff(fast());
    assert_eq!(retry_drain(&r).unwrap_err().to_string(), "inner fails");
    assert_eq!(inner.calls(), 1);
}

#[test]
fn retry_one_attempt_never_backs_off() {
    let inner = Scripted::failing(&[Some("inner fails")]);
    let r = RetrySink::new(inner.clone())
        .max_attempts(1)
        .backoff(|a| panic!("backoff invoked with attempt {a}"));
    assert_eq!(retry_drain(&r).unwrap_err().to_string(), "inner fails");
    assert_eq!(inner.calls(), 1);
}

#[test]
fn retry_defaults() {
    // Three attempts: a sink failing twice then succeeding gets through
    // on the default policy (the default waits are ~100ms, jittered).
    let inner = Scripted::failing(&[Some("a"), Some("b"), None]);
    let r = RetrySink::new(inner.clone());
    retry_drain(&r).unwrap();
    assert_eq!(inner.calls(), 3);
    let inner = Scripted::failing(&[Some("x")]);
    let r = RetrySink::new(inner.clone()).backoff(|_| Duration::ZERO);
    assert!(retry_drain(&r).is_err());
    assert_eq!(inner.calls(), 3);
}

#[test]
fn retry_close_closes_inner_then_dead_letter() {
    let (inner, dl) = (Scripted::ok(), Scripted::ok());
    let r = RetrySink::new(inner.clone()).dead_letter(dl.clone());
    r.close().unwrap();
    assert_eq!((inner.closes(), dl.closes()), (1, 1));
}

#[test]
fn retry_close_inner_error_still_closes_dead_letter() {
    let (inner, dl) = (
        Scripted::ok().close_err("inner close fails"),
        Scripted::ok(),
    );
    let r = RetrySink::new(inner).dead_letter(dl.clone());
    assert_eq!(r.close().unwrap_err().to_string(), "inner close fails");
    assert_eq!(dl.closes(), 1);
}

#[test]
fn retry_close_both_error_returns_the_first() {
    let inner = Scripted::ok().close_err("inner close fails");
    let dl = Scripted::ok().close_err("DL close fails");
    let r = RetrySink::new(inner).dead_letter(dl.clone());
    assert_eq!(r.close().unwrap_err().to_string(), "inner close fails");
    assert_eq!(dl.closes(), 1);
}

#[test]
fn retry_close_without_dead_letter() {
    let inner = Scripted::ok();
    RetrySink::new(inner.clone()).close().unwrap();
    assert_eq!(inner.closes(), 1);
}

#[test]
fn retry_backoff_is_indexed_from_zero() {
    let inner = Scripted::failing(&[Some("transient")]);
    let seen = Arc::new(Mutex::new(Vec::new()));
    let s = seen.clone();
    let r = RetrySink::new(inner).max_attempts(4).backoff(move |a| {
        s.lock().unwrap().push(a);
        Duration::from_millis(1)
    });
    retry_drain(&r).unwrap_err();
    assert_eq!(*seen.lock().unwrap(), [0, 1, 2]);
}

#[test]
fn custom_sleeper_replaces_the_wait() {
    let inner = Scripted::failing(&[Some("transient")]);
    let slept = Arc::new(Mutex::new(Vec::new()));
    let s = slept.clone();
    let r = RetrySink::new(inner)
        .max_attempts(3)
        .backoff(|a| Duration::from_secs(u64::from(a) + 1))
        .sleeper(move |_, d| {
            s.lock().unwrap().push(d);
            Ok(())
        });
    let start = Instant::now();
    retry_drain(&r).unwrap_err();
    assert_eq!(
        *slept.lock().unwrap(),
        [Duration::from_secs(1), Duration::from_secs(2)]
    );
    assert!(start.elapsed() < Duration::from_secs(1));
}

// Templates.

use hop_top_kit::notify::{LiteralTemplate, Template, TextTemplate};

#[test]
fn text_template_renders_event_fields() {
    let mut e = event(
        "kit.test.thing.created",
        json!({"id": "42", "n": 7, "o": {"k": [1]}}),
    );
    e.source = "svc".into();
    e.subject = "sub".into();
    let t = TextTemplate::parse(
        "{{.Source}}/{{ .Topic }} {{.Subject}} {{.Payload.id}} {{.Payload.n}} {{.Payload.o}}",
    )
    .unwrap();
    assert_eq!(
        t.render(&e).unwrap(),
        r#"svc/kit.test.thing.created sub 42 7 {"k":[1]}"#
    );
}

#[test]
fn text_template_payload_edges() {
    let e = event("a.b.c.d", json!({"present": null}));
    let r = |src: &str| TextTemplate::parse(src).unwrap().render(&e);
    assert_eq!(r("{{.Payload.present}}").unwrap(), "<nil>");
    assert_eq!(r("{{.Payload.absent}}").unwrap(), "<no value>");
    assert_eq!(r("{{.Payload.absent.deeper}}").unwrap(), "<no value>");
    assert!(r("{{.Topic.Nope}}").is_err());
    let scalar = event("a.b.c.d", json!(3));
    assert!(TextTemplate::parse("{{.Payload.k}}")
        .unwrap()
        .render(&scalar)
        .is_err());
}

#[test]
fn text_template_bad_syntax_fails_at_parse() {
    for src in [
        "{{.Topic",
        "{{}}",
        "{{ . }}",
        "{{Topic}}",
        "{{.Topic..X}}",
        "{{.A B}}",
    ] {
        assert!(TextTemplate::parse(src).is_err(), "{src}");
    }
}

#[test]
fn text_template_unknown_field_fails_at_render() {
    let t = TextTemplate::parse("{{.NotAField}}").unwrap();
    assert!(t.render(&event("a.b.c.d", Value::Null)).is_err());
}

#[test]
fn text_template_plain_text_and_empty() {
    let e = event("a.b.c.d", Value::Null);
    assert_eq!(
        TextTemplate::parse("no actions")
            .unwrap()
            .render(&e)
            .unwrap(),
        "no actions"
    );
    assert_eq!(TextTemplate::parse("").unwrap().render(&e).unwrap(), "");
}

#[test]
fn literal_template_ignores_the_event() {
    let t = LiteralTemplate::new("kit alert");
    assert_eq!(
        t.render(&event("a.b.c.d", Value::Null)).unwrap(),
        "kit alert"
    );
    assert_eq!(t.render(&event("x.y.z.w", json!(1))).unwrap(), "kit alert");
}

#[test]
fn closures_are_templates() {
    let t = |e: &hop_top_kit::bus::Event| Ok(e.source.to_uppercase());
    assert_eq!(
        t.render(&event("a.b.c.d", Value::Null)).unwrap(),
        "NOTIFY-TEST"
    );
}
