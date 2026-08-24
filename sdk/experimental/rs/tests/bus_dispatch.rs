//! In-memory dispatch. Ports the core (non-async) parts of
//! `go/runtime/bus/bus_test.go` plus the enforcement-mode cases from
//! `validate_test.go`.
//!
//! The Go async-handler tests (`TestPublish_AsyncHandler`,
//! `TestPublish_AsyncDoesNotBlockPublisher`, `TestClose_WaitsForAsync`,
//! `TestClose_RespectsDeadline`, `TestAsyncPool_BoundedGoroutines`,
//! `TestWithMaxAsync_*`) have no analogue here: dispatch is synchronous,
//! so there is no goroutine pool to bound and nothing in flight for
//! close to drain. See the `bus::mem` module docs for that decision.

#![cfg(feature = "bus")]

use std::cell::{Cell, RefCell};
use std::rc::Rc;

use hop_top_kit::bus::{Bus, BusError, Event, Mode, SharedBus};
use serde_json::json;

fn ev(topic: &str) -> Event {
    Event::new(topic, "src", json!(null))
}

#[test]
fn publish_sync_handler() {
    let mut bus = Bus::new();
    let got: Rc<RefCell<Option<Event>>> = Rc::new(RefCell::new(None));
    let sink = Rc::clone(&got);
    bus.subscribe("test.event", move |e| {
        *sink.borrow_mut() = Some(e.clone());
        Ok(())
    });

    let e = Event::new("test.event", "src", json!("hello"));
    bus.publish(&e).expect("publish");

    let got = got.borrow();
    let got = got.as_ref().expect("handler ran");
    assert_eq!(got.source, "src");
    assert_eq!(got.payload, json!("hello"));
}

#[test]
fn publish_sync_veto() {
    let mut bus = Bus::new();
    bus.subscribe("test.event", |_| Err(BusError::handler("nope")));

    let second_called = Rc::new(Cell::new(false));
    let flag = Rc::clone(&second_called);
    bus.subscribe("test.event", move |_| {
        flag.set(true);
        Ok(())
    });

    let err = bus.publish(&ev("test.event")).expect_err("expected veto");
    assert!(
        matches!(err, BusError::Handler(ref m) if m == "nope"),
        "{err}"
    );
    assert!(
        !second_called.get(),
        "second handler should not run after veto"
    );
}

#[test]
fn unsubscribe() {
    let mut bus = Bus::new();
    let count = Rc::new(Cell::new(0));
    let c = Rc::clone(&count);
    let id = bus.subscribe("test.event", move |_| {
        c.set(c.get() + 1);
        Ok(())
    });

    bus.publish(&ev("test.event")).expect("publish");
    assert_eq!(count.get(), 1);

    assert!(bus.unsubscribe(id), "unsubscribe should find the id");
    bus.publish(&ev("test.event")).expect("publish");
    assert_eq!(count.get(), 1, "count should not grow after unsubscribe");

    assert!(!bus.unsubscribe(id), "second unsubscribe is a no-op");
}

#[test]
fn publish_wildcard_subscribe() {
    let mut bus = Bus::new();
    let got: Rc<RefCell<Vec<String>>> = Rc::new(RefCell::new(Vec::new()));
    let sink = Rc::clone(&got);
    bus.subscribe("llm.*", move |e| {
        sink.borrow_mut().push(e.topic.to_string());
        Ok(())
    });

    for t in ["llm.request", "llm.response", "tool.exec"] {
        bus.publish(&ev(t)).expect("publish");
    }

    assert_eq!(*got.borrow(), vec!["llm.request", "llm.response"]);
}

#[test]
fn publish_hash_wildcard() {
    let mut bus = Bus::new();
    let got: Rc<RefCell<Vec<String>>> = Rc::new(RefCell::new(Vec::new()));
    let sink = Rc::clone(&got);
    bus.subscribe("llm.#", move |e| {
        sink.borrow_mut().push(e.topic.to_string());
        Ok(())
    });

    for t in ["llm.request", "llm.request.start", "llm", "tool.exec"] {
        bus.publish(&ev(t)).expect("publish");
    }

    assert_eq!(
        *got.borrow(),
        vec!["llm.request", "llm.request.start", "llm"]
    );
}

#[test]
fn publish_exact_does_not_match_other() {
    let mut bus = Bus::new();
    let called = Rc::new(Cell::new(false));
    let flag = Rc::clone(&called);
    bus.subscribe("llm.request", move |_| {
        flag.set(true);
        Ok(())
    });

    bus.publish(&ev("llm.response")).expect("publish");
    assert!(
        !called.get(),
        "exact subscribe should not match a different topic"
    );
}

#[test]
fn publish_delivers_in_subscription_order() {
    let mut bus = Bus::new();
    let order: Rc<RefCell<Vec<u8>>> = Rc::new(RefCell::new(Vec::new()));
    for i in 0..3u8 {
        let sink = Rc::clone(&order);
        bus.subscribe("test.event", move |_| {
            sink.borrow_mut().push(i);
            Ok(())
        });
    }
    bus.publish(&ev("test.event")).expect("publish");
    assert_eq!(*order.borrow(), vec![0, 1, 2]);
}

#[test]
fn close_rejects_publish() {
    let mut bus = Bus::new();
    bus.close();
    let err = bus.publish(&ev("test.event")).expect_err("expected error");
    assert!(matches!(err, BusError::Closed), "{err}");
    assert!(bus.is_closed());
}

#[test]
fn close_drops_subscriptions() {
    let mut bus = Bus::new();
    let called = Rc::new(Cell::new(false));
    let flag = Rc::clone(&called);
    bus.subscribe("test.event", move |_| {
        flag.set(true);
        Ok(())
    });
    bus.close();
    // Publishing is rejected outright; the handler is gone either way.
    assert!(bus.publish(&ev("test.event")).is_err());
    assert!(!called.get());
}

// --- enforcement modes -----------------------------------------------

#[test]
fn mode_display() {
    assert_eq!(Mode::Off.to_string(), "off");
    assert_eq!(Mode::Warn.to_string(), "warn");
    assert_eq!(Mode::Strict.to_string(), "strict");
}

#[test]
fn publish_mode_off_allows_invalid_topics() {
    let reported = Rc::new(Cell::new(0u32));
    let r = Rc::clone(&reported);
    let mut bus = Bus::builder()
        .enforce(Mode::Off)
        .invalid_topic_reporter(move |_| r.set(r.get() + 1))
        .build();

    let got = Rc::new(Cell::new(0u32));
    let g = Rc::clone(&got);
    bus.subscribe("bad.topic", move |_| {
        g.set(g.get() + 1);
        Ok(())
    });

    bus.publish(&ev("bad.topic")).expect("publish");
    assert_eq!(got.get(), 1, "delivered");
    assert_eq!(reported.get(), 0, "reporter must not fire in Off");
}

#[test]
fn publish_mode_warn_delivers_and_reports() {
    let reported = Rc::new(Cell::new(0u32));
    let was_invalid_topic = Rc::new(Cell::new(false));
    let r = Rc::clone(&reported);
    let w = Rc::clone(&was_invalid_topic);
    let mut bus = Bus::builder()
        .enforce(Mode::Warn)
        .invalid_topic_reporter(move |err| {
            r.set(r.get() + 1);
            w.set(err.is_invalid_topic());
        })
        .build();

    let got = Rc::new(Cell::new(0u32));
    let g = Rc::clone(&got);
    bus.subscribe("bad.topic", move |_| {
        g.set(g.get() + 1);
        Ok(())
    });

    bus.publish(&ev("bad.topic")).expect("publish");
    assert_eq!(got.get(), 1, "warn must still deliver");
    assert_eq!(reported.get(), 1);
    assert!(was_invalid_topic.get(), "reporter received an InvalidTopic");

    // Valid topic — the reporter must not fire again.
    bus.subscribe("a.b.c.d", |_| Ok(()));
    bus.publish(&ev("a.b.c.d")).expect("publish valid");
    assert_eq!(reported.get(), 1, "reporter calls after valid publish");
}

#[test]
fn publish_mode_strict_rejects_invalid() {
    let reported = Rc::new(Cell::new(0u32));
    let r = Rc::clone(&reported);
    let mut bus = Bus::builder()
        .enforce(Mode::Strict)
        .invalid_topic_reporter(move |_| r.set(r.get() + 1))
        .build();

    let got = Rc::new(Cell::new(0u32));
    let g = Rc::clone(&got);
    bus.subscribe("bad.topic", move |_| {
        g.set(g.get() + 1);
        Ok(())
    });

    let err = bus
        .publish(&ev("bad.topic"))
        .expect_err("expected error in Strict");
    assert!(err.is_invalid_topic(), "{err}");
    assert_eq!(got.get(), 0, "strict must veto delivery");
    assert_eq!(reported.get(), 1);

    // Valid topic — must succeed.
    let g = Rc::clone(&got);
    bus.subscribe("a.b.c.d", move |_| {
        g.set(g.get() + 1);
        Ok(())
    });
    bus.publish(&ev("a.b.c.d")).expect("publish valid");
    assert_eq!(got.get(), 1);
}

#[test]
fn publish_default_mode_is_warn() {
    // No explicit enforce → Warn → invalid topics are still delivered
    // (no reporter installed, so violations silently drop).
    let mut bus = Bus::new();
    assert_eq!(bus.enforce_mode(), Mode::Warn);

    let got = Rc::new(Cell::new(0u32));
    let g = Rc::clone(&got);
    bus.subscribe("bad.topic", move |_| {
        g.set(g.get() + 1);
        Ok(())
    });
    bus.publish(&ev("bad.topic")).expect("publish");
    assert_eq!(got.get(), 1, "default-mode delivered");
}

#[test]
fn strict_without_reporter_still_rejects() {
    // A strict bus with no reporter must reject invalid topics without
    // panicking.
    let bus = Bus::builder().enforce(Mode::Strict).build();
    let err = bus.publish(&ev("bad.topic")).expect_err("expected error");
    assert!(err.is_invalid_topic(), "{err}");
}

// --- SharedBus -------------------------------------------------------

#[test]
fn shared_bus_subscribe_publish_unsubscribe() {
    let bus = SharedBus::new(Bus::builder().enforce(Mode::Strict).build());

    let count = Rc::new(Cell::new(0u32));
    let c = Rc::clone(&count);
    let id = bus.subscribe("crm.sales.deal.*", move |_| {
        c.set(c.get() + 1);
        Ok(())
    });

    // A second handle observes the same subscriptions.
    let publisher = bus.clone();
    publisher
        .publish(&ev("crm.sales.deal.created"))
        .expect("publish");
    assert_eq!(count.get(), 1);

    assert!(bus.unsubscribe(id));
    publisher
        .publish(&ev("crm.sales.deal.created"))
        .expect("publish");
    assert_eq!(count.get(), 1);

    bus.close();
    assert!(publisher.is_closed());
    assert!(publisher.publish(&ev("crm.sales.deal.created")).is_err());
}
