//! wasm32-unknown-unknown smoke test for the notify core, run under
//! Node by wasm-bindgen-test-runner:
//!
//! ```sh
//! CARGO_TARGET_WASM32_UNKNOWN_UNKNOWN_RUNNER=wasm-bindgen-test-runner \
//!   cargo test --target wasm32-unknown-unknown --no-default-features \
//!   --features notify --test notify_wasm
//! ```
//!
//! No threads and no clock: the retry waits through the caller's
//! sleeper, and an event is stamped by hand.

#![cfg(all(feature = "notify", target_arch = "wasm32", target_os = "unknown"))]

use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime};

use hop_top_kit::bus::{BusError, Context, Event, Sink};
use hop_top_kit::notify::{severity_of, FilterSink, RetrySink, Severity, Template, TextTemplate};
use serde_json::json;
use wasm_bindgen_test::wasm_bindgen_test;

#[derive(Clone, Default)]
struct Flaky(Arc<Mutex<(usize, usize)>>);

impl Sink for Flaky {
    fn drain(&self, _: &Context, _: &Event) -> Result<(), BusError> {
        let mut st = self.0.lock().unwrap();
        st.0 += 1;
        if st.0 < 3 {
            Err(BusError::handler("transient"))
        } else {
            st.1 += 1;
            Ok(())
        }
    }
    fn close(&self) -> Result<(), BusError> {
        Ok(())
    }
}

fn event(topic: &str, payload: serde_json::Value) -> Event {
    Event::new_at(topic, "wasm", payload, SystemTime::UNIX_EPOCH)
}

#[wasm_bindgen_test]
fn severity_filter_and_retry_run_without_a_clock() {
    let e = event("billing.invoice.failed", json!({"severity": "critical"}));
    assert_eq!(severity_of(&e), Severity::Critical);

    let waits = Arc::new(Mutex::new(Vec::new()));
    let w = waits.clone();
    let flaky = Flaky::default();
    let sink = RetrySink::new(
        FilterSink::new(flaky.clone())
            .topic_pattern("billing.#")
            .min_severity(Severity::Error),
    )
    .max_attempts(3)
    .backoff(|a| Duration::from_millis(10 * (u64::from(a) + 1)))
    .sleeper(move |_, d| {
        w.lock().unwrap().push(d);
        Ok(())
    });
    sink.drain(&Context::background(), &e).unwrap();
    assert_eq!(*flaky.0.lock().unwrap(), (3, 1));
    assert_eq!(
        *waits.lock().unwrap(),
        [Duration::from_millis(10), Duration::from_millis(20)]
    );

    // A rejected event never reaches the inner sink.
    sink.drain(
        &Context::background(),
        &event("other.x.y.z", json!({"severity": "critical"})),
    )
    .unwrap();
    assert_eq!(flaky.0.lock().unwrap().0, 3);
}

#[wasm_bindgen_test]
fn templates_render() {
    let t = TextTemplate::parse("{{.Source}}/{{.Topic}}").unwrap();
    assert_eq!(
        t.render(&event("a.b.c.d", json!(null))).unwrap(),
        "wasm/a.b.c.d"
    );
}
