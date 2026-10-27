//! wasm32-unknown-unknown smoke test for the breaker, run under Node by
//! wasm-bindgen-test-runner:
//!
//! ```sh
//! CARGO_TARGET_WASM32_UNKNOWN_UNKNOWN_RUNNER=wasm-bindgen-test-runner \
//!   cargo test --target wasm32-unknown-unknown --no-default-features \
//!   --features breaker --test breaker_wasm
//! ```
//!
//! No system clock and no threads: the caller's clock drives the
//! cooldown and events publish inline.

#![cfg(all(feature = "breaker", target_arch = "wasm32", target_os = "unknown"))]

use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime};

use hop_top_kit::breaker::{Breaker, CircuitOpts, Clock, Record, State};
use hop_top_kit::bus::Context;
use hop_top_kit::domain::{EventPublisher, Payload, PublishError};
use wasm_bindgen_test::wasm_bindgen_test;

struct Fake(Mutex<SystemTime>);

impl Clock for Fake {
    fn now(&self) -> SystemTime {
        *self.0.lock().unwrap()
    }
}

#[derive(Default)]
struct Topics(Mutex<Vec<String>>);

impl EventPublisher for Topics {
    fn publish(&self, _: &Context, topic: &str, _: &str, _: &Payload) -> Result<(), PublishError> {
        self.0.lock().unwrap().push(topic.to_string());
        Ok(())
    }
}

#[wasm_bindgen_test]
fn cooldown_runs_on_the_callers_clock_and_events_publish_inline() {
    let clock = Arc::new(Fake(Mutex::new(SystemTime::UNIX_EPOCH)));
    let topics = Arc::new(Topics::default());
    let b = Breaker::builder("wasm")
        .circuit(CircuitOpts {
            failure_threshold: 1,
            delay: Duration::from_secs(5),
            ..Default::default()
        })
        .clock(clock.clone())
        .publisher(topics.clone())
        .logger(Arc::new(|_: &Record| {}))
        .build();
    b.record(false, 0);
    assert_eq!(b.state(), State::Open);
    *clock.0.lock().unwrap() += Duration::from_secs(5);
    assert_eq!(b.state(), State::HalfOpen);
    assert_eq!(
        *topics.0.lock().unwrap(),
        ["kit.core.breaker.opened", "kit.core.breaker.half_opened"]
    );
}
