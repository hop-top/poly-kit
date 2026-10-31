//! The email sink and the SMTP mailer: ports of the Go `email_test.go`,
//! `smtp_test.go` and `smtp_offline_test.go`, plus a full exchange
//! against a scripted loopback server.

#![cfg(feature = "notify-email")]

mod notify_support;

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use hop_top_kit::breaker::{Breaker, Record};
use hop_top_kit::bus::{BusError, Context, Sink};
use hop_top_kit::netpolicy::NetPolicy;
use hop_top_kit::notify::email::{EmailSink, Mailer, Message, SmtpMailer, DEFAULT_CONTENT_TYPE};
use hop_top_kit::notify::{is_broken_circuit, LiteralTemplate, TextTemplate};
use hop_top_kit::redact::Redactor;
use notify_support::event;
use notify_support::smtp::{Config, Server};
use serde_json::json;

fn ev() -> hop_top_kit::bus::Event {
    let mut e = event(
        "alerts.system.outage.fired",
        json!({"message": "hello world"}),
    );
    e.source = "test".into();
    e
}

#[derive(Clone, Default)]
struct Capture {
    calls: Arc<AtomicUsize>,
    last: Arc<Mutex<Option<Message>>>,
}

impl Mailer for Capture {
    fn send(&self, _: &Context, msg: &Message) -> Result<(), BusError> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        *self.last.lock().unwrap() = Some(msg.clone());
        Ok(())
    }
}

fn sink(m: impl Mailer + 'static) -> EmailSink {
    EmailSink::new(m)
        .recipients(["a@b.io"])
        .subject(LiteralTemplate::new("s"))
        .body(LiteralTemplate::new("b"))
}

fn drain(s: &EmailSink) -> Result<(), BusError> {
    s.drain(&Context::background(), &ev())
}

#[test]
fn drain_renders_and_sends() {
    let cap = Capture::default();
    let s = EmailSink::new(cap.clone())
        .from("ops@example.com")
        .recipients(["a@b.io", "c@d.io"])
        .subject(TextTemplate::parse("alert: {{.Topic}}").unwrap())
        .body(LiteralTemplate::new("body fixed"));
    drain(&s).unwrap();
    assert_eq!(cap.calls.load(Ordering::SeqCst), 1);
    assert_eq!(
        cap.last.lock().unwrap().clone().unwrap(),
        Message {
            from: "ops@example.com".into(),
            to: vec!["a@b.io".into(), "c@d.io".into()],
            content_type: DEFAULT_CONTENT_TYPE.into(),
            subject: "alert: alerts.system.outage.fired".into(),
            body: "body fixed".into(),
        }
    );
    s.close().unwrap();
}

#[test]
fn content_type_can_be_overridden() {
    let cap = Capture::default();
    drain(&sink(cap.clone()).content_type("text/html")).unwrap();
    assert_eq!(
        cap.last.lock().unwrap().clone().unwrap().content_type,
        "text/html"
    );
}

#[test]
fn no_recipients_is_a_drain_error() {
    let cap = Capture::default();
    let s = EmailSink::new(cap.clone())
        .subject(LiteralTemplate::new("s"))
        .body(LiteralTemplate::new("b"));
    assert!(drain(&s).is_err());
    assert_eq!(cap.calls.load(Ordering::SeqCst), 0);
}

#[test]
fn missing_templates_are_a_drain_error() {
    let cap = Capture::default();
    let s = EmailSink::new(cap.clone()).recipients(["a@b.io"]);
    assert!(drain(&s).is_err());
    assert_eq!(cap.calls.load(Ordering::SeqCst), 0);
}

#[test]
fn mailer_error_is_propagated() {
    let m = |_: &Context, _: &Message| Err(BusError::handler("transport flaky"));
    assert_eq!(drain(&sink(m)).unwrap_err().to_string(), "transport flaky");
}

#[test]
fn redactor_runs_on_subject_and_body() {
    let cap = Capture::default();
    let mut r = Redactor::new();
    r.add_rule("aws-access-key", "AKIA[0-9A-Z]{16}", "")
        .unwrap();
    let s = EmailSink::new(cap.clone())
        .recipients(["a@b.io"])
        .subject(LiteralTemplate::new("key AKIAIOSFODNN7EXAMPLE"))
        .body(LiteralTemplate::new("body AKIAIOSFODNN7EXAMPLE"))
        .redactor(Arc::new(r));
    drain(&s).unwrap();
    let m = cap.last.lock().unwrap().clone().unwrap();
    for t in [&m.subject, &m.body] {
        assert!(!t.contains("AKIAIOSFODNN7EXAMPLE"), "{t}");
        assert!(t.contains("REDACTED"), "{t}");
    }
}

#[test]
fn open_circuit_returns_the_refusal_unwrapped() {
    let cap = Capture::default();
    let b = Arc::new(
        Breaker::builder("email-trip")
            .logger(Arc::new(|_: &Record| {}))
            .build(),
    );
    b.trip("test");
    let err = drain(&sink(cap.clone()).breaker(b)).unwrap_err();
    assert!(is_broken_circuit(&err));
    assert_eq!(err.to_string(), hop_top_kit::notify::BROKEN_CIRCUIT);
    assert_eq!(cap.calls.load(Ordering::SeqCst), 0);
}

#[test]
fn closed_circuit_records_the_outcome() {
    let b = Arc::new(
        Breaker::builder("email-ok")
            .logger(Arc::new(|_: &Record| {}))
            .build(),
    );
    drain(&sink(Capture::default()).breaker(Arc::clone(&b))).unwrap();
    assert_eq!(b.stats().counters["ops"], 1);
}

// SMTP.

fn msg() -> Message {
    Message {
        from: "a@b.io".into(),
        to: vec!["c@d.io".into()],
        content_type: String::new(),
        subject: "x".into(),
        body: "y".into(),
    }
}

fn mailer(port: u16) -> SmtpMailer {
    SmtpMailer::new("127.0.0.1", port).dial_timeout(Duration::from_secs(5))
}

#[test]
fn smtp_options_thread_through() {
    let _ = SmtpMailer::new("smtp.example.com", 587)
        .auth("user", "pass")
        .starttls(true)
        .from("noreply@example.com")
        .dial_timeout(Duration::from_secs(1))
        .policy(NetPolicy::offline());
}

#[test]
fn smtp_dial_failure_is_a_dial_error() {
    let port = {
        let l = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        l.local_addr().unwrap().port()
    };
    let err = mailer(port)
        .send_message(&Context::background(), &msg())
        .unwrap_err();
    assert!(err.is_dial() && !err.is_offline(), "{err}");
    assert!(
        err.to_string().starts_with("emailsink: smtp dial failed:"),
        "{err}"
    );
}

#[test]
fn smtp_empty_from_and_to_are_errors() {
    let m = SmtpMailer::new("127.0.0.1", 25);
    let mut no_from = msg();
    no_from.from.clear();
    assert!(m.send_message(&Context::background(), &no_from).is_err());
    let mut no_to = msg();
    no_to.to.clear();
    let m = m.from("ops@example.com");
    assert!(m.send_message(&Context::background(), &no_to).is_err());
}

#[test]
fn smtp_default_from_applies() {
    let srv = Server::start(Config::default());
    let mut m = msg();
    m.from.clear();
    mailer(srv.port)
        .from("ops@example.com")
        .send_message(&Context::background(), &m)
        .unwrap();
    let s = &srv.wait_sessions(1)[0];
    assert!(
        s.commands
            .contains(&"MAIL FROM:<ops@example.com>".to_string()),
        "{s:?}"
    );
}

#[test]
fn smtp_sends_a_message() {
    let srv = Server::start(Config::default());
    let mut m = msg();
    m.to = vec!["c@d.io".into(), "e@f.io".into()];
    m.subject = "alert".into();
    m.body = "line one\n.leading dot\nlast".into();
    mailer(srv.port)
        .send_message(&Context::background(), &m)
        .unwrap();
    let s = &srv.wait_sessions(1)[0];
    assert_eq!(
        s.commands,
        [
            "EHLO localhost",
            "MAIL FROM:<a@b.io>",
            "RCPT TO:<c@d.io>",
            "RCPT TO:<e@f.io>",
            "DATA",
            "QUIT"
        ]
    );
    // The server sees dot-stuffing (a doubled leading dot) and CRLF.
    assert_eq!(
        s.data,
        "From: a@b.io\r\nTo: c@d.io, e@f.io\r\nSubject: alert\r\n\
         Content-Type: text/plain; charset=utf-8\r\n\r\n\
         line one\r\n..leading dot\r\nlast\r\n"
    );
}

#[test]
fn smtp_authenticates_over_loopback_when_advertised() {
    let srv = Server::start(Config {
        auth: true,
        ..Config::default()
    });
    mailer(srv.port)
        .auth("user", "pass")
        .send_message(&Context::background(), &msg())
        .unwrap();
    let s = &srv.wait_sessions(1)[0];
    // base64("\0user\0pass")
    assert!(
        s.commands
            .contains(&"AUTH PLAIN AHVzZXIAcGFzcw==".to_string()),
        "{s:?}"
    );
}

#[test]
fn smtp_skips_auth_the_server_does_not_advertise() {
    let srv = Server::start(Config::default());
    mailer(srv.port)
        .auth("user", "pass")
        .send_message(&Context::background(), &msg())
        .unwrap();
    assert!(!srv.wait_sessions(1)[0]
        .commands
        .iter()
        .any(|c| c.starts_with("AUTH")));
}

#[test]
fn smtp_starttls_requires_the_extension() {
    let srv = Server::start(Config::default());
    let err = mailer(srv.port)
        .starttls(true)
        .send_message(&Context::background(), &msg())
        .unwrap_err();
    assert!(err.to_string().contains("STARTTLS"), "{err}");
    assert!(!srv.wait_sessions(1)[0]
        .commands
        .iter()
        .any(|c| c.starts_with("MAIL")));
}

#[test]
fn smtp_refused_recipient_names_the_command() {
    let srv = Server::start(Config {
        reject_rcpt: Some("c@d.io".into()),
        ..Config::default()
    });
    let err = mailer(srv.port)
        .send_message(&Context::background(), &msg())
        .unwrap_err();
    let t = err.to_string();
    assert!(t.contains("RCPT TO") && t.contains("550"), "{t}");
}

#[test]
fn smtp_refuses_header_injection_before_dialing() {
    let srv = Server::start(Config::default());
    for edit in [
        |m: &mut Message| m.subject = "hi\r\nBcc: x@y.io".into(),
        |m: &mut Message| m.to = vec!["c@d.io>\r\nRCPT TO:<x@y.io".into()],
        |m: &mut Message| m.from = "a@b.io\nX: y".into(),
    ] {
        let mut m = msg();
        edit(&mut m);
        let err = mailer(srv.port)
            .send_message(&Context::background(), &m)
            .unwrap_err();
        assert!(err.to_string().contains("line break"), "{err}");
    }
    assert!(srv.sessions().is_empty());
}

#[test]
fn smtp_mailer_is_a_mailer_for_the_sink() {
    let srv = Server::start(Config::default());
    drain(&sink(mailer(srv.port)).from("ops@example.com")).unwrap();
    let s = &srv.wait_sessions(1)[0];
    assert!(s.data.contains("Subject: s\r\n"), "{s:?}");
    assert!(s.data.ends_with("\r\n\r\nb\r\n"), "{s:?}");
}

#[test]
fn offline_refuses_a_remote_send_before_any_dial() {
    let srv = Server::start(Config::default());
    // A remote name on the live listener's port: the policy refuses on
    // the address alone, so a blocked dial cannot pass for one that
    // merely failed to resolve.
    let err = SmtpMailer::new("kit-offline-probe.invalid", srv.port)
        .from("ops@example.com")
        .policy(NetPolicy::offline())
        .send_message(&Context::background(), &msg())
        .unwrap_err();
    assert!(err.is_offline() && err.is_dial(), "{err}");
    assert!(
        err.to_string().contains("network disabled by --offline"),
        "{err}"
    );
    assert!(
        srv.wait_sessions(1).is_empty(),
        "reached a listener while offline"
    );
}

#[test]
fn offline_allows_loopback() {
    let srv = Server::start(Config::default());
    mailer(srv.port)
        .policy(NetPolicy::offline())
        .send_message(&Context::background(), &msg())
        .unwrap();
    assert_eq!(srv.wait_sessions(1).len(), 1);
}

#[test]
fn online_dials_normally() {
    let srv = Server::start(Config::default());
    SmtpMailer::new("localhost", srv.port)
        .dial_timeout(Duration::from_secs(5))
        .send_message(&Context::background(), &msg())
        .unwrap();
    assert_eq!(srv.wait_sessions(1).len(), 1);
}

#[test]
fn canceled_context_fails_the_dial() {
    let srv = Server::start(Config::default());
    let (ctx, cancel) = Context::background().with_cancel();
    cancel.cancel();
    let err = mailer(srv.port).send_message(&ctx, &msg()).unwrap_err();
    assert!(
        err.is_dial() && err.to_string().contains("context canceled"),
        "{err}"
    );
    assert!(srv.sessions().is_empty());
}

fn body_on_the_wire(body: &str) -> (String, Vec<String>) {
    let srv = Server::start(Config::default());
    let mut m = msg();
    m.body = body.into();
    mailer(srv.port)
        .send_message(&Context::background(), &m)
        .unwrap();
    let s = srv.wait_sessions(1).remove(0);
    let (_, wire) = s.data.split_once("\r\n\r\n").expect("header end");
    (wire.to_string(), s.commands)
}

#[test]
fn smtp_bare_cr_dot_cr_cannot_end_the_message() {
    // "\r.\r\n" read as a terminator by a lenient server would smuggle
    // the MAIL FROM after it.
    let (wire, cmds) = body_on_the_wire("x\r.\r\nMAIL FROM:<evil@x.io>");
    assert_eq!(wire, "x\r\n..\r\nMAIL FROM:<evil@x.io>\r\n");
    assert_eq!(
        cmds.iter().filter(|c| c.starts_with("MAIL")).count(),
        1,
        "{cmds:?}"
    );
}

#[test]
fn smtp_bare_lf_dot_lf_is_stuffed() {
    let (wire, _) = body_on_the_wire("a\n.\nb");
    assert_eq!(wire, "a\r\n..\r\nb\r\n");
}

#[test]
fn smtp_bare_cr_is_a_line_break() {
    let (wire, _) = body_on_the_wire("a\rb\r.c");
    assert_eq!(wire, "a\r\nb\r\n..c\r\n");
}

#[test]
fn smtp_crlf_is_not_doubled() {
    let (wire, _) = body_on_the_wire("a\r\n.b\r\n\r\n.");
    assert_eq!(wire, "a\r\n..b\r\n\r\n..\r\n");
}
