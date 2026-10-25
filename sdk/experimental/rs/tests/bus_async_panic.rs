//! The panic contract for async handlers: a handler that panics ends its
//! task as the executor handles panics, its pool slot comes back, and
//! the thread it ran on is left with no task frame. Go has no
//! counterpart: there a panicking goroutine ends the process.

#![cfg(feature = "bus")]

#[path = "bus_async/exec.rs"]
mod exec;

use std::panic::{catch_unwind, AssertUnwindSafe};
use std::time::Duration;

use exec::{publish, threads, until, within, Manual};
use hop_top_kit::bus::{Bus, Context, SharedBus};

#[test]
fn a_panicking_async_handler_frees_its_slot() {
    let bus = SharedBus::new(Bus::builder().max_async(1).build());
    bus.subscribe_async("test.panic", threads(), |_, _| async { panic!("boom") });
    publish(&bus, "test.panic").unwrap();
    until("slot freed", || bus.async_in_flight() == 0);
    // The one slot admits the next publish.
    publish(&bus, "test.panic").unwrap();
    until("slot freed again", || bus.async_in_flight() == 0);
    let b = bus.clone();
    within(move || b.close_ctx(&Context::background())).expect("drained");
}

#[test]
fn a_handler_that_panics_when_called_leaves_no_task_frame() {
    let manual = Manual::default();
    let bus = SharedBus::default();
    bus.subscribe_async(
        "test.panic",
        manual.spawner(),
        |_, _| -> std::future::Ready<()> { panic!("boom") },
    );
    publish(&bus, "test.panic").unwrap();
    let res = catch_unwind(AssertUnwindSafe(|| manual.run()));
    assert!(res.is_err(), "the panic reaches the executor");
    assert_eq!(bus.async_in_flight(), 0, "slot freed on unwind");

    // A task queued on another subscription. Close on this thread must
    // wait for it: a task frame left behind by the panic would make
    // close count it as its own and return at once.
    bus.subscribe_async("test.other", manual.spawner(), |_, _| async {});
    publish(&bus, "test.other").unwrap();
    let (ctx, _c) = Context::background().with_timeout(Duration::from_millis(50));
    let err = bus.close_ctx(&ctx).expect_err("waits for the queued task");
    assert_eq!(err.to_string(), "context deadline exceeded");
    manual.run();
    let b = bus.clone();
    within(move || b.close_ctx(&Context::background())).expect("drained");
}
