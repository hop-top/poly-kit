//! The notify wiring end to end: a `TeeBus` fanning out to a filtered
//! webhook, a filtered and retried email sink and a JSONL audit trail.
//! Ports of the Go `e2e_test.go`.

#![cfg(all(feature = "notify-webhook", feature = "notify-email"))]

mod notify_support;

use std::io::Write;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use hop_top_kit::breaker::{Breaker, Record};
use hop_top_kit::bus::{Adapter, BusError, Context, Event, JsonlSink, MemoryAdapter, Sink, TeeBus};
use hop_top_kit::notify::email::{EmailSink, Message};
use hop_top_kit::notify::webhook::WebhookSink;
use hop_top_kit::notify::{
    exponential_backoff, is_broken_circuit, FilterSink, LiteralTemplate, RetrySink, Severity,
};
use hop_top_kit::redact::Redactor;
use notify_support::http::Server;
use serde_json::{json, Value};

/// A writer a test can read after the sink flushes and drops it.
#[derive(Clone, Default)]
struct Buf(Arc<Mutex<Vec<u8>>>);

impl Write for Buf {
    fn write(&mut self, b: &[u8]) -> std::io::Result<usize> {
        self.0.lock().unwrap().extend_from_slice(b);
        Ok(b.len())
    }
    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

fn ev(topic: &str, source: &str, payload: Value) -> Event {
    let mut e = Event::new_at(topic, source, payload, std::time::UNIX_EPOCH);
    e.id = String::new();
    e
}

fn errs() -> (
    Arc<Mutex<Vec<BusError>>>,
    impl Fn(&BusError) + Send + Sync + 'static,
) {
    let seen = Arc::new(Mutex::new(Vec::new()));
    let s = Arc::clone(&seen);
    (seen, move |e: &BusError| {
        s.lock().unwrap().push(BusError::handler(e.to_string()));
    })
}

#[test]
fn all_sinks_wired_through_tee_bus() {
    let pages_srv = Server::ok();
    let sent: Arc<Mutex<Vec<Message>>> = Arc::default();
    let s = Arc::clone(&sent);
    let mailer = move |_: &Context, m: &Message| {
        s.lock().unwrap().push(m.clone());
        Ok(())
    };
    let audit_buf = Buf::default();
    let (seen, on_err) = errs();

    let pages = FilterSink::new(WebhookSink::new(&pages_srv.url))
        .min_severity(Severity::Critical)
        .topic_pattern("kit.runtime.breaker.#");
    let summaries = RetrySink::new(
        FilterSink::new(
            EmailSink::new(mailer)
                .recipients(["ops@example.com"])
                .subject(LiteralTemplate::new("alert"))
                .body(LiteralTemplate::new("body")),
        )
        .min_severity(Severity::Warn)
        .topic_pattern("billing.#"),
    )
    .max_attempts(2)
    .backoff({
        let b = exponential_backoff(Duration::from_millis(1), 1.0, false);
        move |a| b(a)
    });
    let audit = JsonlSink::new(audit_buf.clone());

    let tee = TeeBus::new(
        MemoryAdapter::new(),
        vec![Box::new(pages), Box::new(summaries), Box::new(audit)],
    )
    .on_error(on_err);

    // (event, to_pages, to_summaries)
    let workload = [
        (
            ev(
                "kit.runtime.breaker.tripped",
                "kit",
                json!({"severity": "critical", "message": "fuse blew"}),
            ),
            true,
            false,
        ),
        (
            ev(
                "kit.runtime.breaker.opened",
                "kit",
                json!({"severity": "critical", "name": "egress"}),
            ),
            true,
            false,
        ),
        (
            ev(
                "kit.runtime.breaker.reset",
                "kit",
                json!({"severity": "info"}),
            ),
            false,
            false,
        ),
        (
            ev(
                "billing.invoice.paid",
                "billing-svc",
                json!({"severity": "warn", "message": "paid"}),
            ),
            false,
            true,
        ),
        (
            ev(
                "billing.invoice.failed",
                "billing-svc",
                json!({"severity": "error", "id": "inv_99"}),
            ),
            false,
            true,
        ),
        (
            ev(
                "billing.invoice.created",
                "billing-svc",
                json!({"severity": "info"}),
            ),
            false,
            false,
        ),
        (
            ev("kit.runtime.entity.created", "kit", Value::Null),
            false,
            false,
        ),
        (
            ev(
                "shipping.label.dispatched",
                "ship",
                json!({"severity": "critical"}),
            ),
            false,
            false,
        ),
        (
            ev(
                "billing.payout.failed",
                "billing-svc",
                json!({"severity": "critical", "message": "stuck"}),
            ),
            false,
            true,
        ),
        (
            ev(
                "kit.runtime.breaker.degraded.persistent",
                "kit",
                json!({"severity": "critical"}),
            ),
            true,
            false,
        ),
    ];
    let ctx = Context::background();
    for (e, _, _) in &workload {
        tee.publish(&ctx, e).unwrap();
    }
    tee.close(&ctx).unwrap();

    let want_pages = workload.iter().filter(|w| w.1).count();
    let want_summaries = workload.iter().filter(|w| w.2).count();
    assert_eq!(pages_srv.hits(), want_pages);
    assert_eq!(sent.lock().unwrap().len(), want_summaries);

    let audit = String::from_utf8(audit_buf.0.lock().unwrap().clone()).unwrap();
    assert_eq!(audit.lines().count(), workload.len());

    for r in pages_srv.requests() {
        assert!(r.json()["topic"]
            .as_str()
            .unwrap()
            .starts_with("kit.runtime.breaker."));
    }
    for m in sent.lock().unwrap().iter() {
        assert_eq!(m.subject, "alert");
        assert_eq!(m.to, ["ops@example.com"]);
    }
    assert!(
        seen.lock().unwrap().is_empty(),
        "{:?}",
        seen.lock().unwrap()
    );
}

#[test]
fn guardrails_isolate_sinks() {
    let redactor_srv = Server::ok();
    let breaker_srv = Server::ok();
    let control_srv = Server::ok();

    let mut red = Redactor::new();
    red.add_rule("inline-secret", r"secret=\S+", "").unwrap();
    let br = Arc::new(
        Breaker::builder("notify-e2e-guardrails")
            .logger(Arc::new(|_: &Record| {}))
            .build(),
    );
    br.trip("e2e: pre-test trip");

    let (seen, on_err) = errs();
    let tee = TeeBus::new(
        MemoryAdapter::new(),
        vec![
            Box::new(WebhookSink::new(&redactor_srv.url).redactor(Arc::new(red))),
            Box::new(WebhookSink::new(&breaker_srv.url).breaker(br)),
            Box::new(WebhookSink::new(&control_srv.url)),
        ],
    )
    .on_error(on_err);

    tee.publish(
        &Context::background(),
        &ev(
            "kit.test.secret.published",
            "e2e",
            json!({"note": "secret=hunter2 leaked"}),
        ),
    )
    .unwrap();

    assert_eq!(redactor_srv.hits(), 1);
    let body = String::from_utf8(redactor_srv.one().body).unwrap();
    assert!(!body.contains("hunter2"), "{body}");
    assert!(body.contains("REDACTED"), "{body}");

    assert_eq!(
        breaker_srv.hits(),
        0,
        "a tripped breaker must short-circuit"
    );
    assert!(seen.lock().unwrap().iter().any(is_broken_circuit));

    assert_eq!(
        control_srv.hits(),
        1,
        "a sibling's breaker must not affect it"
    );
}

#[test]
fn events_without_severity_default_to_info() {
    let warn_srv = Server::ok();
    let open_srv = Server::ok();
    let tee = TeeBus::new(
        MemoryAdapter::new(),
        vec![
            Box::new(FilterSink::new(WebhookSink::new(&warn_srv.url)).min_severity(Severity::Warn)),
            Box::new(FilterSink::new(WebhookSink::new(&open_srv.url))),
        ],
    );
    let ctx = Context::background();
    let cases = [
        ev("t.a", "e2e", Value::Null),
        ev("t.b", "e2e", json!("just a string")),
        ev("t.c", "e2e", json!({"id": 1})),
    ];
    for e in &cases {
        tee.publish(&ctx, e).unwrap();
    }
    assert_eq!(warn_srv.hits(), 0, "info is below a warn floor");
    assert_eq!(open_srv.hits(), cases.len());
}

#[test]
fn retry_outermost_dead_letters_a_failing_webhook() {
    let down = Server::start(503, "down", Duration::ZERO);
    let dead = Server::ok();
    let sink =
        RetrySink::new(FilterSink::new(WebhookSink::new(&down.url)).min_severity(Severity::Error))
            .max_attempts(3)
            .backoff(|_| Duration::from_millis(1))
            .dead_letter(WebhookSink::new(&dead.url));
    sink.drain(
        &Context::background(),
        &ev(
            "ops.pager.alert.fired",
            "ops",
            json!({"severity": "critical"}),
        ),
    )
    .unwrap();
    assert_eq!(down.hits(), 3);
    assert_eq!(dead.hits(), 1);
    sink.close().unwrap();
}
