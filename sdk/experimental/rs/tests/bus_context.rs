//! Context propagation through the bus.
//!
//! Go threads a `context.Context` through `Publish` and every handler;
//! the network adapter relays a peer's origin through a context value.
//! `bus::Context` is the Rust form: cancellation, a deadline and typed
//! values, with Go's error texts.

#![cfg(feature = "bus")]

use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use hop_top_kit::bus::{Bus, BusError, Context, ContextError, Event, SharedBus};
use serde_json::json;

fn ev(topic: &str) -> Event {
    Event::new(topic, "src", json!(null))
}

#[derive(Clone, Debug, PartialEq)]
struct Origin(&'static str);

#[test]
fn context_error_texts_match_go() {
    assert_eq!(ContextError::Canceled.to_string(), "context canceled");
    assert_eq!(
        ContextError::DeadlineExceeded.to_string(),
        "context deadline exceeded"
    );
    let e: BusError = ContextError::Canceled.into();
    assert_eq!(e.to_string(), "context canceled");
}

#[test]
fn background_is_never_done() {
    let ctx = Context::background();
    assert_eq!(ctx.err(), None);
    assert_eq!(ctx.deadline(), None);
    assert!(ctx.value::<Origin>().is_none());
}

#[test]
fn cancel_reaches_children_not_parents() {
    let parent = Context::background().with_value(Origin("p"));
    let (ctx, cancel) = parent.with_cancel();
    let child = ctx.with_value(7u32);
    assert_eq!(child.err(), None);

    cancel.cancel();
    cancel.cancel(); // idempotent, as Go's CancelFunc
    assert_eq!(ctx.err(), Some(ContextError::Canceled));
    assert_eq!(child.err(), Some(ContextError::Canceled));
    assert_eq!(parent.err(), None);
}

#[test]
fn deadline_reached_is_exceeded() {
    let (ctx, _cancel) = Context::background().with_timeout(Duration::ZERO);
    assert_eq!(ctx.err(), Some(ContextError::DeadlineExceeded));

    let (ctx, _cancel) = Context::background().with_deadline(Instant::now());
    assert_eq!(ctx.err(), Some(ContextError::DeadlineExceeded));
}

#[test]
fn deadline_not_reached_is_live() {
    let (ctx, cancel) = Context::background().with_timeout(Duration::from_secs(3600));
    assert_eq!(ctx.err(), None);
    assert!(ctx.deadline().is_some());
    cancel.cancel();
    assert_eq!(ctx.err(), Some(ContextError::Canceled));
}

#[test]
fn cancel_after_deadline_keeps_deadline_exceeded() {
    // Go's timer fires at the deadline, so a later cancel finds the
    // context already done with DeadlineExceeded.
    let (ctx, cancel) = Context::background().with_timeout(Duration::ZERO);
    cancel.cancel();
    assert_eq!(ctx.err(), Some(ContextError::DeadlineExceeded));
}

#[test]
fn deadline_is_the_earliest_in_the_chain() {
    let soon = Instant::now() + Duration::from_secs(60);
    let later = soon + Duration::from_secs(60);
    let (outer, _c1) = Context::background().with_deadline(soon);
    let (inner, _c2) = outer.with_deadline(later);
    assert_eq!(inner.deadline(), Some(soon));
}

#[test]
fn values_are_typed_and_the_nearest_wins() {
    let ctx = Context::background()
        .with_value(Origin("outer"))
        .with_value(1u32);
    assert_eq!(ctx.value::<Origin>(), Some(&Origin("outer")));
    assert_eq!(ctx.value::<u32>(), Some(&1));
    assert!(ctx.value::<String>().is_none());

    let shadowed = ctx.with_value(Origin("inner"));
    assert_eq!(shadowed.value::<Origin>(), Some(&Origin("inner")));
    // The parent is unchanged.
    assert_eq!(ctx.value::<Origin>(), Some(&Origin("outer")));
}

#[test]
fn shared_publish_with_canceled_context_returns_go_error_and_skips_handlers() {
    let bus = SharedBus::default();
    let ran = Arc::new(Mutex::new(0));
    let r = Arc::clone(&ran);
    bus.subscribe("test.event", move |_| {
        *r.lock().unwrap() += 1;
        Ok(())
    });

    let (ctx, cancel) = Context::background().with_cancel();
    cancel.cancel();
    let err = bus
        .publish_ctx(&ctx, &ev("test.event"))
        .expect_err("canceled");
    assert!(
        matches!(err, BusError::Context(ContextError::Canceled)),
        "{err}"
    );
    assert_eq!(err.to_string(), "context canceled");
    assert_eq!(*ran.lock().unwrap(), 0);
}

#[test]
fn plain_publish_with_expired_deadline_returns_go_error() {
    let mut bus = Bus::new();
    bus.subscribe("test.event", |_| panic!("must not run"));
    let (ctx, _cancel) = Context::background().with_timeout(Duration::ZERO);
    let err = bus
        .publish_ctx(&ctx, &ev("test.event"))
        .expect_err("expired");
    assert_eq!(err.to_string(), "context deadline exceeded");
}

#[test]
fn closed_takes_precedence_over_a_canceled_context() {
    // Go's Publish checks the topic, then the closed flag; the context
    // check sits after both, so every Go-visible outcome is unchanged.
    let bus = SharedBus::default();
    bus.close();
    let (ctx, cancel) = Context::background().with_cancel();
    cancel.cancel();
    let err = bus
        .publish_ctx(&ctx, &ev("test.event"))
        .expect_err("closed");
    assert!(matches!(err, BusError::Closed), "{err}");
}

#[test]
fn handlers_receive_the_publish_context() {
    let bus = SharedBus::default();
    let seen = Arc::new(Mutex::new(None));
    let s = Arc::clone(&seen);
    bus.subscribe_ctx("test.event", move |ctx, _| {
        *s.lock().unwrap() = ctx.value::<Origin>().cloned();
        Ok(())
    });

    let ctx = Context::background().with_value(Origin("peer-1"));
    bus.publish_ctx(&ctx, &ev("test.event")).expect("publish");
    assert_eq!(*seen.lock().unwrap(), Some(Origin("peer-1")));

    // `publish` is `publish_ctx` with the background context.
    bus.publish(&ev("test.event")).expect("publish");
    assert_eq!(*seen.lock().unwrap(), None);
}

#[test]
fn plain_bus_handlers_receive_the_publish_context() {
    let mut bus = Bus::new();
    let seen = Arc::new(Mutex::new(None));
    let s = Arc::clone(&seen);
    bus.subscribe_ctx("test.event", move |ctx, _| {
        *s.lock().unwrap() = ctx.value::<Origin>().cloned();
        Ok(())
    });
    let ctx = Context::background().with_value(Origin("local"));
    bus.publish_ctx(&ctx, &ev("test.event")).expect("publish");
    assert_eq!(*seen.lock().unwrap(), Some(Origin("local")));
}

#[test]
fn nested_publish_relays_a_derived_context() {
    // Go's network adapter re-publishes with
    // `context.WithValue(ctx, networkOriginKey, origin)`.
    let bus = SharedBus::default();
    let seen = Arc::new(Mutex::new(Vec::new()));
    let inner = bus.clone();
    bus.subscribe_ctx("test.bus.outer.ran", move |ctx, _| {
        inner.publish_ctx(&ctx.with_value(Origin("relay")), &ev("test.bus.inner.ran"))
    });
    let s = Arc::clone(&seen);
    bus.subscribe_ctx("test.bus.inner.ran", move |ctx, _| {
        s.lock()
            .unwrap()
            .push((ctx.value::<Origin>().cloned(), ctx.value::<u32>().copied()));
        Ok(())
    });

    let ctx = Context::background().with_value(9u32);
    bus.publish_ctx(&ctx, &ev("test.bus.outer.ran"))
        .expect("publish");
    assert_eq!(*seen.lock().unwrap(), [(Some(Origin("relay")), Some(9))]);
}

#[test]
fn cancel_during_dispatch_does_not_stop_later_handlers() {
    // Go checks nothing between handlers; each handler may consult the
    // context itself.
    let bus = SharedBus::default();
    let (ctx, cancel) = Context::background().with_cancel();
    bus.subscribe("test.event", move |_| {
        cancel.cancel();
        Ok(())
    });
    let seen = Arc::new(Mutex::new(None));
    let s = Arc::clone(&seen);
    bus.subscribe_ctx("test.event", move |ctx, _| {
        *s.lock().unwrap() = Some(ctx.err());
        Ok(())
    });
    bus.publish_ctx(&ctx, &ev("test.event")).expect("publish");
    assert_eq!(*seen.lock().unwrap(), Some(Some(ContextError::Canceled)));
}
