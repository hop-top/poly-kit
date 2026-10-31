//! Breaker behaviour the shared corpus cannot pin: rate periods rolling
//! over (Go's limiter reads failsafe-go's wall clock), concurrency, the
//! default publish thread, payload bytes and wiring panics.
#![cfg(feature = "breaker")]

#[path = "breaker_support/mod.rs"]
mod support;

use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant};

use hop_top_kit::breaker::{
    Action, Breaker, BrokenCircuit, CircuitOpts, State, TransitionPayload, TrippedPayload,
};
use hop_top_kit::domain::Payload;

use support::{Events, FakeClock, Logs};

fn quiet(name: &str, clock: Arc<FakeClock>) -> hop_top_kit::breaker::Builder {
    Breaker::builder(name).clock(clock).logger(Logs::new())
}

#[test]
fn breaker_is_send_and_sync() {
    fn assert<T: Send + Sync>() {}
    assert::<Breaker>();
}

#[test]
fn rate_refills_at_each_period_from_construction() {
    let clock = FakeClock::new();
    let b = quiet("rate", clock.clone())
        .max_per_interval(2, Duration::from_secs(60))
        .build();
    clock.advance(Duration::from_secs(30));
    assert!(b.allow().is_ok());
    assert!(b.allow().is_ok());
    assert_eq!(b.allow(), Err(BrokenCircuit));
    clock.advance(Duration::from_secs(30) - Duration::from_nanos(1));
    assert_eq!(b.allow(), Err(BrokenCircuit), "still the first period");
    clock.advance(Duration::from_nanos(1));
    assert!(b.allow().is_ok(), "period boundary refills");
    assert!(b.allow().is_ok());
    assert_eq!(b.allow(), Err(BrokenCircuit));
    clock.advance(Duration::from_secs(600));
    assert!(
        b.allow().is_ok(),
        "skipped periods refill once, no carry-over"
    );
    assert!(b.allow().is_ok());
    assert_eq!(b.allow(), Err(BrokenCircuit));
}

#[test]
fn rate_refused_probe_returns_its_permit() {
    let clock = FakeClock::new();
    let b = quiet("probe-release", clock.clone())
        .circuit(CircuitOpts {
            failure_threshold: 1,
            success_threshold: 2,
            delay: Duration::from_secs(10),
        })
        .max_per_interval(1, Duration::from_secs(3600))
        .build();
    b.record(false, 0);
    clock.advance(Duration::from_secs(10));
    assert!(b.allow().is_ok(), "probe 1: circuit and rate admit");
    assert_eq!(b.allow(), Err(BrokenCircuit), "probe 2: rate refuses");
    clock.advance(Duration::from_secs(3600));
    assert!(b.allow().is_ok(), "the refused probe gave its permit back");
    assert_eq!(b.allow(), Err(BrokenCircuit), "both permits now out");
    b.record(true, 0);
    b.record(true, 0);
    assert_eq!(b.state(), State::Closed);
}

#[test]
fn degrade_behaves_like_halt() {
    let b = quiet("degrade", FakeClock::new())
        .on_trip(Action::Degrade)
        .build();
    b.trip("ops");
    assert_eq!(b.allow(), Err(BrokenCircuit));
}

#[test]
fn concurrent_callers_keep_the_counters_exact() {
    let b = Arc::new(
        quiet("concurrent", FakeClock::new())
            .circuit(CircuitOpts {
                failure_threshold: 1_000_000,
                ..Default::default()
            })
            .build(),
    );
    let handles: Vec<_> = (0..8)
        .map(|i| {
            let b = Arc::clone(&b);
            thread::spawn(move || {
                for _ in 0..500 {
                    b.allow().unwrap();
                    b.record(i % 2 == 0, 3);
                }
            })
        })
        .collect();
    for h in handles {
        h.join().unwrap();
    }
    let st = b.stats();
    assert_eq!(st.counters["ops"], 4000);
    assert_eq!(st.counters["bytes"], 12000);
    assert_eq!(b.state(), State::Closed);
}

#[test]
fn default_publish_runs_off_the_caller() {
    let events = Events::new();
    let b = quiet("thread-publish", FakeClock::new())
        .publisher(events.clone())
        .build();
    b.trip("ops");
    let deadline = Instant::now() + Duration::from_secs(2);
    while events.all().len() < 2 && Instant::now() < deadline {
        thread::sleep(Duration::from_millis(2));
    }
    let mut topics: Vec<String> = events.all().into_iter().map(|e| e.0).collect();
    topics.sort();
    assert_eq!(
        topics,
        ["kit.core.breaker.opened", "kit.core.breaker.tripped"]
    );
}

#[test]
fn payloads_encode_as_go() {
    let tripped = Payload::BreakerTripped(TrippedPayload {
        reason: "<ops>".into(),
        from: State::HalfOpen,
        to: State::Open,
    });
    assert_eq!(
        tripped.to_json(),
        r#"{"Reason":"\u003cops\u003e","From":2,"To":1}"#
    );
    let moved = Payload::BreakerTransition(TransitionPayload {
        from: State::Open,
        to: State::Closed,
        trigger: "auto".into(),
    });
    assert_eq!(moved.to_json(), r#"{"From":1,"To":0,"Trigger":"auto"}"#);
    let back: TransitionPayload = serde_json::from_str(&moved.to_json()).unwrap();
    assert_eq!(back.to, State::Closed);
}

#[test]
fn system_clock_is_the_default() {
    let b = Breaker::builder("system").logger(Logs::new()).build();
    b.trip("ops");
    assert!(b.stats().last_trip_at.is_some());
}

#[test]
#[should_panic(expected = "breaker: name is required")]
fn empty_name_panics() {
    let _ = Breaker::builder("").build();
}

#[test]
#[should_panic(expected = "breaker/policy: max must be > 0")]
fn zero_budget_panics() {
    let _ = Breaker::builder("zero").max_ops(0).build();
}

#[test]
#[should_panic(expected = "breaker: rate period must be > 0")]
fn zero_rate_period_panics() {
    let _ = Breaker::builder("zero-period")
        .max_per_interval(1, Duration::ZERO)
        .build();
}
