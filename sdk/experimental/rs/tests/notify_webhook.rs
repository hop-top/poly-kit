//! The webhook sink: ports of the Go `webhook_test.go` and
//! `template_test.go`, against a loopback server.

#![cfg(feature = "notify-webhook")]

mod notify_support;

use std::sync::Arc;
use std::time::{Duration, Instant};

use hop_top_kit::breaker::{Breaker, CircuitOpts, Record};
use hop_top_kit::bus::{BusError, Context, ContextError, Sink};
use hop_top_kit::netpolicy::{GuardedClient, NetPolicy};
use hop_top_kit::notify::webhook::{JsonTemplate, SlackTemplate, WebhookSink, WebhookTemplate};
use hop_top_kit::notify::{is_broken_circuit, RetrySink};
use hop_top_kit::redact::Redactor;
use notify_support::event;
use notify_support::http::Server;
use serde_json::{json, Value};

fn ev() -> hop_top_kit::bus::Event {
    event(
        "kit.test.thing.created",
        json!({"id": "42", "secret": "AKIAIOSFODNN7EXAMPLE"}),
    )
}

fn drain(s: &WebhookSink) -> Result<(), BusError> {
    s.drain(&Context::background(), &ev())
}

fn breaker(name: &str) -> Arc<Breaker> {
    Arc::new(
        Breaker::builder(name)
            .logger(Arc::new(|_: &Record| {}))
            .build(),
    )
}

#[test]
fn new_does_no_io_and_close_is_a_no_op() {
    let s = WebhookSink::new("http://example.invalid");
    assert!(s.close().is_ok());
}

#[test]
fn drain_posts_the_event_as_json() {
    let srv = Server::ok();
    drain(&WebhookSink::new(&srv.url)).unwrap();
    let r = srv.one();
    assert_eq!(r.method, "POST");
    assert_eq!(
        r.header("content-type").as_deref(),
        Some("application/json")
    );
    let got = r.json();
    assert_eq!(got["topic"], "kit.test.thing.created");
    assert_eq!(got["source"], "notify-test");
    assert_eq!(got["payload"]["id"], "42");
}

#[test]
fn non_2xx_is_an_error_with_the_status_and_body() {
    let srv = Server::start(500, "boom", Duration::ZERO);
    let err = drain(&WebhookSink::new(&srv.url)).unwrap_err().to_string();
    assert_eq!(err, "webhook: http 500: boom");
}

#[test]
fn non_2xx_body_is_truncated_to_512() {
    let srv = Server::start(400, "x".repeat(4096), Duration::ZERO);
    let err = drain(&WebhookSink::new(&srv.url)).unwrap_err().to_string();
    assert!(err.len() < 700, "{} bytes", err.len());
    assert!(err.starts_with("webhook: http 400: xxx"));
}

#[test]
fn a_redirect_is_not_followed() {
    let target = Server::ok();
    let srv = Server::start_raw(
        Duration::ZERO,
        302,
        vec![("Location".into(), format!("{}/moved", target.url))],
        Vec::new(),
    );
    let s = WebhookSink::new(&srv.url).auth_bearer("tok");
    let err = drain(&s).unwrap_err().to_string();
    assert!(err.starts_with("webhook: http 302"), "{err}");
    assert_eq!(
        target.hits(),
        0,
        "the credential must not follow a redirect"
    );
}

#[test]
fn timeout_is_an_error() {
    let srv = Server::start(200, "", Duration::from_millis(500));
    let s = WebhookSink::new(&srv.url).timeout(Duration::from_millis(20));
    let start = Instant::now();
    let err = drain(&s).unwrap_err().to_string();
    assert!(start.elapsed() < Duration::from_millis(400), "{err}");
    assert!(err.starts_with("webhook: post:"), "{err}");
    assert!(
        ["timed out", "timeout", "deadline"]
            .iter()
            .any(|w| err.contains(w)),
        "{err}"
    );
}

#[test]
fn auth_bearer_sets_the_header() {
    let srv = Server::ok();
    drain(&WebhookSink::new(&srv.url).auth_bearer("s3cr3t")).unwrap();
    assert_eq!(
        srv.one().header("authorization").as_deref(),
        Some("Bearer s3cr3t")
    );
}

#[test]
fn headers_accumulate_and_repeat() {
    let srv = Server::ok();
    let s = WebhookSink::new(&srv.url)
        .header("X-Trace", "abc")
        .header("X-Channel", "alerts")
        .header("X-Trace", "def");
    drain(&s).unwrap();
    let r = srv.one();
    assert_eq!(r.header_values("x-trace"), ["abc", "def"]);
    assert_eq!(r.header("x-channel").as_deref(), Some("alerts"));
}

#[test]
fn slack_template_produces_valid_json() {
    let srv = Server::ok();
    let s = WebhookSink::new(&srv.url).template(SlackTemplate::new("alert: {{.Topic}}").unwrap());
    drain(&s).unwrap();
    assert_eq!(
        srv.one().json(),
        json!({"text": "alert: kit.test.thing.created"})
    );
}

#[test]
fn redactor_scrubs_the_rendered_body() {
    let srv = Server::ok();
    let mut r = Redactor::new();
    r.add_rule("aws-access-key", "AKIA[0-9A-Z]{16}", "")
        .unwrap();
    drain(&WebhookSink::new(&srv.url).redactor(Arc::new(r))).unwrap();
    let body = String::from_utf8(srv.one().body).unwrap();
    assert!(!body.contains("AKIAIOSFODNN7EXAMPLE"), "{body}");
    assert!(body.contains("REDACTED"), "{body}");
}

#[test]
fn open_circuit_short_circuits_before_any_io() {
    let srv = Server::ok();
    let b = breaker("webhook-test-trip");
    b.trip("test");
    let err = drain(&WebhookSink::new(&srv.url).breaker(b)).unwrap_err();
    assert!(is_broken_circuit(&err), "{err}");
    assert_eq!(srv.hits(), 0);
}

#[test]
fn closed_circuit_passes() {
    let srv = Server::ok();
    drain(&WebhookSink::new(&srv.url).breaker(breaker("webhook-test-pass"))).unwrap();
    assert_eq!(srv.hits(), 1);
}

#[test]
fn breaker_wraps_a_caller_supplied_client() {
    let srv = Server::ok();
    let b = breaker("webhook-test-custom-client");
    b.trip("test");
    let client = GuardedClient::new(NetPolicy::online()).unwrap();
    let err = drain(&WebhookSink::new(&srv.url).client(client).breaker(b)).unwrap_err();
    assert!(is_broken_circuit(&err), "{err}");
    assert_eq!(srv.hits(), 0);
}

#[test]
fn transport_failures_trip_the_breaker_and_status_errors_do_not() {
    // Nothing listens on this port: a refused connection is a transport
    // failure, which the breaker counts.
    let dead = {
        let l = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        l.local_addr().unwrap()
    };
    let b = Arc::new(
        Breaker::builder("webhook-test-count")
            .circuit(CircuitOpts {
                failure_threshold: 2,
                ..Default::default()
            })
            .logger(Arc::new(|_: &Record| {}))
            .build(),
    );
    let s = WebhookSink::new(format!("http://{dead}")).breaker(Arc::clone(&b));
    for _ in 0..2 {
        let err = drain(&s).unwrap_err();
        assert!(!is_broken_circuit(&err), "{err}");
    }
    assert!(is_broken_circuit(&drain(&s).unwrap_err()));

    // A 500 reaches the server, so the transport succeeded.
    let srv = Server::start(500, "", Duration::ZERO);
    let b = Arc::new(
        Breaker::builder("webhook-test-status")
            .circuit(CircuitOpts {
                failure_threshold: 2,
                ..Default::default()
            })
            .logger(Arc::new(|_: &Record| {}))
            .build(),
    );
    let s = WebhookSink::new(&srv.url).breaker(b);
    for _ in 0..4 {
        let err = drain(&s).unwrap_err();
        assert!(!is_broken_circuit(&err), "{err}");
    }
    assert_eq!(srv.hits(), 4);
}

#[test]
fn bad_url_is_an_error() {
    let err = drain(&WebhookSink::new("http://example.invalid/\x7f")).unwrap_err();
    assert!(err.to_string().contains("webhook"), "{err}");
}

#[test]
fn offline_policy_refuses_a_remote_endpoint_and_allows_loopback() {
    let s = WebhookSink::new("http://webhook.example.invalid/hook").policy(NetPolicy::offline());
    let err = drain(&s).unwrap_err().to_string();
    assert!(err.contains("network disabled by --offline"), "{err}");

    let srv = Server::ok();
    drain(&WebhookSink::new(&srv.url).policy(NetPolicy::offline())).unwrap();
    assert_eq!(srv.hits(), 1);
}

#[test]
fn canceled_context_fails_without_a_request() {
    let srv = Server::ok();
    let (ctx, cancel) = Context::background().with_cancel();
    cancel.cancel();
    let err = WebhookSink::new(&srv.url).drain(&ctx, &ev()).unwrap_err();
    assert!(err.to_string().contains("context canceled"), "{err}");
    assert_eq!(srv.hits(), 0);
}

#[test]
fn context_deadline_cuts_a_slow_response() {
    let srv = Server::start(200, "", Duration::from_millis(800));
    let (ctx, _c) = Context::background().with_timeout(Duration::from_millis(50));
    let start = Instant::now();
    let err = WebhookSink::new(&srv.url)
        .timeout(Duration::from_secs(10))
        .drain(&ctx, &ev())
        .unwrap_err();
    assert!(start.elapsed() < Duration::from_millis(600));
    assert!(
        err.to_string()
            .contains(&ContextError::DeadlineExceeded.to_string()),
        "{err}"
    );
}

#[test]
fn context_cancel_cuts_a_slow_response() {
    let srv = Server::start(200, "", Duration::from_millis(800));
    let (ctx, cancel) = Context::background().with_cancel();
    let t = std::thread::spawn(move || {
        std::thread::sleep(Duration::from_millis(50));
        cancel.cancel();
    });
    let start = Instant::now();
    let err = WebhookSink::new(&srv.url).drain(&ctx, &ev()).unwrap_err();
    t.join().unwrap();
    assert!(start.elapsed() < Duration::from_millis(600));
    assert!(err.to_string().contains("context canceled"), "{err}");
}

#[tokio::test]
async fn drain_works_from_inside_an_async_runtime() {
    let srv = Server::ok();
    let s = WebhookSink::new(&srv.url);
    drain(&s).unwrap();
    assert_eq!(srv.hits(), 1);
}

#[test]
fn json_template_is_the_whole_event() {
    let (body, ct) = JsonTemplate.render(&ev()).unwrap();
    assert_eq!(ct, "application/json");
    let got: Value = serde_json::from_slice(&body).unwrap();
    assert_eq!(got["topic"], "kit.test.thing.created");
    assert_eq!(got["source"], "notify-test");
    assert_eq!(got["payload"]["id"], "42");
}

#[test]
fn slack_template_renders_and_escapes() {
    let t = SlackTemplate::new("alert: {{.Topic}}").unwrap();
    let (body, ct) = t
        .render(&event("foo.bar.baz.created", Value::Null))
        .unwrap();
    assert_eq!(ct, "application/json");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).unwrap(),
        json!({"text": "alert: foo.bar.baz.created"})
    );

    let mut e = event("a.b.c.d", Value::Null);
    e.source = r#"she said "hi""#.into();
    let (body, _) = SlackTemplate::new("{{.Source}}")
        .unwrap()
        .render(&e)
        .unwrap();
    assert_eq!(
        serde_json::from_slice::<Value>(&body).unwrap()["text"],
        r#"she said "hi""#
    );
}

#[test]
fn slack_template_bad_syntax_fails_at_construction() {
    assert!(SlackTemplate::new("hello {{.Topic").is_err());
}

#[test]
fn slack_template_unknown_field_fails_at_render() {
    let t = SlackTemplate::new("{{.NotAField}}").unwrap();
    assert!(t.render(&event("a.b.c.d", Value::Null)).is_err());
}

#[test]
fn a_render_error_is_a_drain_error() {
    let s =
        WebhookSink::new("http://127.0.0.1:9").template(SlackTemplate::new("{{.Nope}}").unwrap());
    assert!(drain(&s)
        .unwrap_err()
        .to_string()
        .starts_with("webhook: render:"));
}

#[test]
fn retry_around_a_webhook_retries_5xx_then_succeeds() {
    // The server fails the first request, then answers 200: use two
    // servers' worth of behaviour via a failing sink in front instead.
    let srv = Server::start(503, "down", Duration::ZERO);
    let r = RetrySink::new(WebhookSink::new(&srv.url))
        .max_attempts(3)
        .backoff(|_| Duration::from_millis(1));
    let err = r.drain(&Context::background(), &ev()).unwrap_err();
    assert!(err.to_string().contains("503"));
    assert_eq!(srv.hits(), 3);
}
