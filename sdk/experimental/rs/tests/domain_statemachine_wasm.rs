//! wasm32-unknown-unknown smoke test for the state machine, run under
//! Node by wasm-bindgen-test-runner:
//!
//! ```sh
//! CARGO_TARGET_WASM32_UNKNOWN_UNKNOWN_RUNNER=wasm-bindgen-test-runner \
//!   cargo test --target wasm32-unknown-unknown --no-default-features \
//!   --features domain --test domain_statemachine_wasm
//! ```
//!
//! No threads: without a spawner the post-transition event runs inline;
//! with a `spawn_local` spawner it runs once the test yields.

#![cfg(all(feature = "domain", target_arch = "wasm32", target_os = "unknown"))]

use std::future::Future;
use std::pin::Pin;
use std::sync::{Arc, Mutex};
use std::task::{Context as TaskCx, Poll};

use hop_top_kit::bus::{Context, Spawn, Task};
use hop_top_kit::domain::{EventPublisher, Payload, PublishError, Rules, State, StateMachine};
use wasm_bindgen_test::wasm_bindgen_test;

#[derive(Default)]
struct Topics(Mutex<Vec<String>>);

impl Topics {
    fn get(&self) -> Vec<String> {
        self.0.lock().unwrap().clone()
    }
}

impl EventPublisher for Topics {
    fn publish(&self, _: &Context, topic: &str, _: &str, _: &Payload) -> Result<(), PublishError> {
        self.0.lock().unwrap().push(topic.to_string());
        Ok(())
    }
}

fn rules() -> Rules {
    [(State::from("open"), vec![State::from("closed")])]
        .into_iter()
        .collect()
}

const BOTH: [&str; 2] = [
    "kit.runtime.state.pre_transitioned",
    "kit.runtime.state.post_transitioned",
];

/// Yields to the executor once.
struct YieldNow(bool);

impl Future for YieldNow {
    type Output = ();

    fn poll(mut self: Pin<&mut Self>, cx: &mut TaskCx<'_>) -> Poll<()> {
        if self.0 {
            return Poll::Ready(());
        }
        self.0 = true;
        cx.waker().wake_by_ref();
        Poll::Pending
    }
}

#[wasm_bindgen_test]
fn without_a_spawner_the_post_event_runs_inline() {
    let topics = Arc::new(Topics::default());
    let sm = StateMachine::new(rules(), Some(topics.clone()));
    sm.transition(&Context::background(), "open", "closed", false)
        .unwrap();
    assert_eq!(topics.get(), BOTH);
}

#[wasm_bindgen_test]
async fn a_spawn_local_spawner_defers_the_post_event() {
    let topics = Arc::new(Topics::default());
    let spawner: Arc<dyn Spawn> = Arc::new(|t: Task| wasm_bindgen_futures::spawn_local(t));
    let sm = StateMachine::new(rules(), Some(topics.clone())).with_spawner(spawner);
    sm.transition(&Context::background(), "open", "closed", false)
        .unwrap();
    assert_eq!(topics.get(), BOTH[..1]);

    // The bound stands in for a timeout.
    for _ in 0..1000 {
        if topics.get().len() == 2 {
            break;
        }
        YieldNow(false).await;
    }
    assert_eq!(topics.get(), BOTH);
}
