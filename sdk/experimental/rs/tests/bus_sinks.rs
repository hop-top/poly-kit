//! Bus sinks: [`TeeBus`], [`JsonlSink`] and [`StdoutSink`].
//!
//! Ports from `go/runtime/bus`: every test in `sink_test.go`,
//! `jsonlsink_test.go` and `stdoutsink_test.go`, plus
//! `TestJSONLSinkFileMode` from `regression_test.go`
//! (`TestTruncateBehavior` is a unit test beside the private helper).
//! Each body runs under the `within` watchdog, so a deadlock fails the
//! test instead of hanging it.

#![cfg(feature = "bus")]

#[path = "bus_async/exec.rs"]
mod exec;

use std::io::{self, Write};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use exec::within;
use hop_top_kit::bus::{
    Adapter, BusError, Context, DynBus, Event, JsonlSink, Sink, StdoutSink, TeeBus,
};
use serde_json::{json, Value};

/// Records drained events; optionally fails every drain.
#[derive(Clone, Default)]
struct SpySink {
    events: Arc<Mutex<Vec<Event>>>,
    fail: Option<&'static str>,
    closed: Arc<Mutex<bool>>,
    close_err: Option<&'static str>,
}

impl SpySink {
    fn events(&self) -> Vec<Event> {
        self.events.lock().unwrap().clone()
    }
    fn closed(&self) -> bool {
        *self.closed.lock().unwrap()
    }
}

impl Sink for SpySink {
    fn drain(&self, _: &Context, e: &Event) -> Result<(), BusError> {
        self.events.lock().unwrap().push(e.clone());
        self.fail.map_or(Ok(()), |m| Err(BusError::handler(m)))
    }
    fn close(&self) -> Result<(), BusError> {
        *self.closed.lock().unwrap() = true;
        self.close_err.map_or(Ok(()), |m| Err(BusError::handler(m)))
    }
}

/// A writer the test can read back after the sink took ownership.
#[derive(Clone, Default)]
struct Buf(Arc<Mutex<Vec<u8>>>);

impl Buf {
    fn text(&self) -> String {
        String::from_utf8(self.0.lock().unwrap().clone()).unwrap()
    }
}

impl Write for Buf {
    fn write(&mut self, b: &[u8]) -> io::Result<usize> {
        self.0.lock().unwrap().extend_from_slice(b);
        Ok(b.len())
    }
    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

fn bg() -> Context {
    Context::background()
}

fn at(topic: &str, source: &str, ts: &str, payload: Value) -> Event {
    let mut e = Event::new(topic, source, payload);
    e.timestamp = ts.to_string();
    e
}

fn boxed(s: &SpySink) -> Box<dyn Sink> {
    Box::new(s.clone())
}

// ---- sink_test.go ----

#[test]
fn tee_bus_delivers_to_handlers_and_sinks() {
    within(|| {
        let sink = SpySink::default();
        let tb = TeeBus::new(DynBus::new(), vec![boxed(&sink)]);
        let got = Arc::new(Mutex::new(None::<String>));
        let g = Arc::clone(&got);
        tb.subscribe(
            "app.start",
            Box::new(move |_, e| {
                *g.lock().unwrap() = Some(e.topic.to_string());
                Ok(())
            }),
        );
        tb.publish(&bg(), &Event::new("app.start", "test", json!(null)))
            .unwrap();
        assert_eq!(got.lock().unwrap().as_deref(), Some("app.start"));
        let events = sink.events();
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].topic.as_str(), "app.start");
    });
}

#[test]
fn tee_bus_sink_error_does_not_block_handler() {
    within(|| {
        let bad = SpySink {
            fail: Some("sink failed"),
            ..SpySink::default()
        };
        let errs = Arc::new(Mutex::new(Vec::<String>::new()));
        let e2 = Arc::clone(&errs);
        let tb = TeeBus::new(DynBus::new(), vec![boxed(&bad)])
            .on_error(move |e| e2.lock().unwrap().push(e.to_string()));
        let handled = Arc::new(AtomicUsize::new(0));
        let h = Arc::clone(&handled);
        tb.subscribe(
            "x.y",
            Box::new(move |_, _| {
                h.fetch_add(1, Ordering::SeqCst);
                Ok(())
            }),
        );
        tb.publish(&bg(), &Event::new("x.y", "test", json!(null)))
            .expect("publish should succeed despite sink error");
        assert_eq!(handled.load(Ordering::SeqCst), 1, "handler was not called");
        assert_eq!(*errs.lock().unwrap(), vec!["sink failed".to_string()]);
    });
}

#[test]
fn tee_bus_multiple_sinks_all_receive() {
    within(|| {
        let sinks = [SpySink::default(), SpySink::default(), SpySink::default()];
        let tb = TeeBus::new(DynBus::new(), sinks.iter().map(boxed).collect());
        tb.publish(&bg(), &Event::new("multi.test", "test", json!("payload")))
            .unwrap();
        for (i, s) in sinks.iter().enumerate() {
            let events = s.events();
            assert_eq!(events.len(), 1, "sink[{i}]");
            assert_eq!(events[0].payload, json!("payload"), "sink[{i}]");
        }
    });
}

#[test]
fn tee_bus_close_closes_bus_and_all_sinks() {
    within(|| {
        let (s1, s2) = (SpySink::default(), SpySink::default());
        let tb = TeeBus::new(DynBus::new(), vec![boxed(&s1), boxed(&s2)]);
        tb.close(&bg()).unwrap();
        let err = tb
            .publish(&bg(), &Event::new("after.close", "test", json!(null)))
            .expect_err("expected error publishing after close");
        assert_eq!(err.to_string(), "bus: publish after close");
        assert!(s1.closed(), "sink 1 not closed");
        assert!(s2.closed(), "sink 2 not closed");
    });
}

#[test]
fn tee_bus_sink_receives_wildcard_topic_events() {
    within(|| {
        let sink = SpySink::default();
        let tb = TeeBus::new(DynBus::new(), vec![boxed(&sink)]);
        let count = Arc::new(AtomicUsize::new(0));
        let c = Arc::clone(&count);
        tb.subscribe(
            "app.#",
            Box::new(move |_, _| {
                c.fetch_add(1, Ordering::SeqCst);
                Ok(())
            }),
        );
        for t in ["app.start", "app.stop", "app.request.begin", "other.topic"] {
            tb.publish(&bg(), &Event::new(t, "test", json!(null)))
                .unwrap();
        }
        assert_eq!(count.load(Ordering::SeqCst), 3);
        assert_eq!(sink.events().len(), 4, "sink sees every published event");
    });
}

// ---- Rust pins on the tee's contract ----

#[test]
fn tee_bus_does_not_drain_a_vetoed_publish() {
    within(|| {
        let sink = SpySink::default();
        let tb = TeeBus::new(DynBus::new(), vec![boxed(&sink)]);
        tb.subscribe("a.b", Box::new(|_, _| Err(BusError::handler("veto"))));
        let err = tb
            .publish(&bg(), &Event::new("a.b", "test", json!(null)))
            .unwrap_err();
        assert_eq!(err.to_string(), "veto");
        assert!(sink.events().is_empty(), "drained after a failed publish");
    });
}

#[test]
fn tee_bus_close_bus_error_wins_else_first_sink_error() {
    within(|| {
        let ok = SpySink::default();
        let e1 = SpySink {
            close_err: Some("first"),
            ..SpySink::default()
        };
        let e2 = SpySink {
            close_err: Some("second"),
            ..SpySink::default()
        };
        let tb = TeeBus::new(DynBus::new(), vec![boxed(&ok), boxed(&e1), boxed(&e2)]);
        assert_eq!(tb.close(&bg()).unwrap_err().to_string(), "first");
        assert!(
            ok.closed() && e1.closed() && e2.closed(),
            "every sink closed"
        );

        // A bus whose close fails: its error wins over the sinks'.
        let ctx = Context::background();
        let (ctx, cancel) = ctx.with_cancel();
        let inner = DynBus::new();
        let slow = Arc::new(Mutex::new(()));
        let held = slow.lock().unwrap();
        let s2 = Arc::clone(&slow);
        inner.subscribe_async("a.b", exec::threads(), move |_, _| {
            let s = Arc::clone(&s2);
            async move {
                drop(s.lock());
            }
        });
        let tb = TeeBus::new(inner, vec![boxed(&e1)]);
        tb.publish(&bg(), &Event::new("a.b", "t", json!(null)))
            .unwrap();
        cancel.cancel();
        let err = tb.close(&ctx).unwrap_err();
        assert_eq!(err.to_string(), "context canceled");
        drop(held);
    });
}

#[test]
fn tee_bus_without_reporter_drops_sink_errors() {
    within(|| {
        let bad = SpySink {
            fail: Some("x"),
            ..SpySink::default()
        };
        let tb = TeeBus::new(DynBus::new(), vec![boxed(&bad)]);
        tb.publish(&bg(), &Event::new("a.b", "t", json!(null)))
            .unwrap();
        assert_eq!(bad.events().len(), 1);
    });
}

// ---- jsonlsink_test.go ----

#[test]
fn jsonl_sink_writes_valid_jsonl() {
    within(|| {
        let buf = Buf::default();
        let sink = JsonlSink::new(buf.clone());
        let events = [
            at(
                "llm.request",
                "client",
                "2025-01-15T10:30:00Z",
                json!({"model": "gpt-4"}),
            ),
            at("tool.exec", "runner", "2025-01-15T10:30:01Z", json!(42)),
        ];
        for e in &events {
            sink.drain(&bg(), e).unwrap();
        }
        sink.close().unwrap();
        let text = buf.text();
        let lines: Vec<&str> = text.trim().split('\n').collect();
        assert_eq!(lines.len(), 2);
        for (i, line) in lines.iter().enumerate() {
            let rec: Value = serde_json::from_str(line).unwrap_or_else(|e| panic!("line {i}: {e}"));
            for k in ["topic", "source", "timestamp"] {
                assert!(rec.get(k).is_some(), "line {i}: missing {k:?}");
            }
        }
        assert_eq!(
            lines[0],
            r#"{"topic":"llm.request","source":"client","timestamp":"2025-01-15T10:30:00Z","payload":{"model":"gpt-4"}}"#
        );
    });
}

#[test]
fn jsonl_sink_file_appends_does_not_truncate() {
    within(|| {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("events.jsonl");
        for topic in ["a", "b"] {
            let s = JsonlSink::open(&path).unwrap();
            s.drain(&bg(), &at(topic, "s", "2025-01-15T10:30:00Z", json!(null)))
                .unwrap();
            s.close().unwrap();
        }
        let data = std::fs::read_to_string(&path).unwrap();
        assert_eq!(data.trim().split('\n').count(), 2, "{data}");
    });
}

#[test]
fn jsonl_sink_close_flushes() {
    within(|| {
        let buf = Buf::default();
        let sink = JsonlSink::new(buf.clone());
        let _ = sink.drain(&bg(), &Event::new("x", "s", json!(null)));
        let before = buf.text();
        sink.close().unwrap();
        let after = buf.text();
        assert!(!after.is_empty(), "expected data after close");
        assert!(after.len() >= before.len(), "close should not lose data");
        let _: Value = serde_json::from_str(after.trim()).expect("one JSON line");
    });
}

#[test]
fn jsonl_sink_nil_payload() {
    within(|| {
        let buf = Buf::default();
        let sink = JsonlSink::new(buf.clone());
        sink.drain(&bg(), &at("t", "s", "2025-01-15T10:30:00Z", json!(null)))
            .unwrap();
        sink.close().unwrap();
        let rec: Value = serde_json::from_str(buf.text().trim()).unwrap();
        assert!(rec.get("payload").is_none(), "null payload is omitted");
    });
}

#[test]
fn jsonl_sink_drain_after_close() {
    within(|| {
        let sink = JsonlSink::new(Buf::default());
        sink.close().unwrap();
        let err = sink
            .drain(&bg(), &Event::new("t", "s", json!(null)))
            .expect_err("expected error draining after close");
        assert_eq!(err.to_string(), "file already closed");
        assert!(sink.close().is_ok(), "close is idempotent");
    });
}

#[cfg(unix)]
#[test]
fn jsonl_sink_file_mode() {
    use std::os::unix::fs::PermissionsExt;
    within(|| {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("events.jsonl");
        let sink = JsonlSink::open(&path).unwrap();
        let mode = std::fs::metadata(&path).unwrap().permissions().mode() & 0o777;
        sink.close().unwrap();
        assert_eq!(mode, 0o600, "file mode = {mode:o}, want 600");
    });
}

// ---- stdoutsink_test.go ----

const TS: &str = "2025-03-15T10:30:45Z";

fn stdout_line(topic: &str, source: &str, payload: Value) -> String {
    let buf = Buf::default();
    let sink = StdoutSink::with_writer(buf.clone());
    sink.drain(&bg(), &at(topic, source, TS, payload)).unwrap();
    buf.text()
}

#[test]
fn stdout_sink_output_format() {
    within(|| {
        assert_eq!(
            stdout_line("llm.request", "llm.client", json!("hello world")),
            "[2025-03-15T10:30:45] llm.request llm.client: hello world\n"
        );
    });
}

#[test]
fn stdout_sink_payload_truncation() {
    within(|| {
        let out = stdout_line("big.payload", "test", json!("x".repeat(200)));
        let want = format!("{}...\n", "x".repeat(120));
        assert!(out.ends_with(&want), "{out:?}");
        assert_eq!(
            out,
            format!("[2025-03-15T10:30:45] big.payload test: {want}")
        );
    });
}

#[test]
fn stdout_sink_nil_payload() {
    within(|| {
        assert_eq!(
            stdout_line("test.nil", "src", json!(null)),
            "[2025-03-15T10:30:45] test.nil src: <nil>\n"
        );
    });
}

#[test]
fn stdout_sink_empty_source() {
    within(|| {
        assert_eq!(
            stdout_line("test.empty", "", json!(42)),
            "[2025-03-15T10:30:45] test.empty : 42\n"
        );
    });
}

#[test]
fn stdout_sink_close_noop() {
    within(|| {
        assert!(StdoutSink::new().close().is_ok());
    });
}

#[test]
fn stdout_sink_keeps_the_event_zone() {
    within(|| {
        let buf = Buf::default();
        let sink = StdoutSink::with_writer(buf.clone());
        let e = at("a.b", "s", "2025-03-15T10:30:45.123456+02:00", json!(true));
        sink.drain(&bg(), &e).unwrap();
        assert_eq!(buf.text(), "[2025-03-15T10:30:45] a.b s: true\n");
    });
}
