//! Locking with real threads: two migrators over one lockable driver.
//! Order is fixed by latches, not sleeps; the lock polls its context only
//! to notice a cancel, as a driver waiting on a database would.
//! Cross-language vectors live in `upgrade_conformance.rs`.

#![cfg(all(feature = "upgrade", unix))]

use std::path::Path;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::thread;
use std::time::Duration;

use hop_top_kit::bus::Context;
use hop_top_kit::upgrade::{
    BoxError, Locker, MigrateError, Migration, Migrator, SchemaDriver, Unlock,
};

/// Opens once; waiting for it gives up after ten seconds so a broken
/// ordering fails instead of hanging.
#[derive(Default)]
struct Latch {
    open: Mutex<bool>,
    cv: Condvar,
}

impl Latch {
    fn open(&self) {
        *self.open.lock().unwrap() = true;
        self.cv.notify_all();
    }

    fn wait(&self, what: &str) {
        let guard = self.open.lock().unwrap();
        let (open, timeout) = self
            .cv
            .wait_timeout_while(guard, Duration::from_secs(10), |open| !*open)
            .unwrap();
        assert!(
            *open && !timeout.timed_out(),
            "timed out waiting for {what}"
        );
    }
}

/// The store both migrators share: its version and one lock.
#[derive(Default)]
struct Gate {
    state: Mutex<GateState>,
    cv: Condvar,
    /// Opens when some `lock` call finds the lock held.
    queued: Latch,
    version_reads: AtomicUsize,
}

#[derive(Default)]
struct GateState {
    held: bool,
    version: String,
}

struct GateDriver(Arc<Gate>);

impl SchemaDriver for GateDriver {
    fn name(&self) -> &str {
        "gate"
    }

    fn version(&self) -> Result<String, BoxError> {
        self.0.version_reads.fetch_add(1, Ordering::SeqCst);
        Ok(self.0.state.lock().unwrap().version.clone())
    }

    fn set_version(&mut self, version: &str) -> Result<(), BoxError> {
        self.0.state.lock().unwrap().version = version.to_string();
        Ok(())
    }

    fn stores_version(&self) -> bool {
        true
    }

    fn as_locker(&self) -> Option<&dyn Locker> {
        Some(self)
    }

    fn backup(&self, _dest: &Path) -> Result<(), BoxError> {
        Ok(())
    }

    fn restore(&mut self, _src: &Path) -> Result<(), BoxError> {
        Ok(())
    }
}

impl Locker for GateDriver {
    fn lock(&self, ctx: &Context) -> Result<Unlock, BoxError> {
        let gate = &self.0;
        let mut st = gate.state.lock().unwrap();
        while st.held {
            gate.queued.open();
            if let Some(e) = ctx.err() {
                return Err(Box::new(e));
            }
            st = gate
                .cv
                .wait_timeout(st, Duration::from_millis(5))
                .unwrap()
                .0;
        }
        st.held = true;
        drop(st);
        let gate = gate.clone();
        Ok(Box::new(move || {
            gate.state.lock().unwrap().held = false;
            gate.cv.notify_all();
            Ok(())
        }))
    }
}

struct Run {
    migrator: Migrator,
    ups: Arc<AtomicUsize>,
}

/// A migrator over `gate` with one migration; with `hold`, its `up` tells
/// `entered` and waits for `release`.
fn run_over(gate: &Arc<Gate>, dir: &Path, hold: Option<(Arc<Latch>, Arc<Latch>)>) -> Run {
    let ups = Arc::new(AtomicUsize::new(0));
    let counted = ups.clone();
    let mut migrator = Migrator::new("locktool", "1.0.0").data_dir(dir);
    migrator.register(Migration::new("1.0.0", "gate", move |_| {
        counted.fetch_add(1, Ordering::SeqCst);
        if let Some((entered, release)) = &hold {
            entered.open();
            release.wait("release");
        }
        Ok(())
    }));
    migrator.add_driver(GateDriver(gate.clone()));
    Run { migrator, ups }
}

fn spawn(mut migrator: Migrator, ctx: Context) -> thread::JoinHandle<Result<(), MigrateError>> {
    thread::spawn(move || migrator.run(&ctx))
}

#[test]
fn second_migrator_waits_for_the_lock_then_finds_nothing_pending() {
    let tmp = tempfile::tempdir().unwrap();
    let gate = Arc::new(Gate::default());
    let (entered, release) = (Arc::new(Latch::default()), Arc::new(Latch::default()));
    let first = run_over(
        &gate,
        &tmp.path().join("a"),
        Some((entered.clone(), release.clone())),
    );
    let second = run_over(&gate, &tmp.path().join("b"), None);
    let (first_ups, second_ups) = (first.ups.clone(), second.ups.clone());

    let a = spawn(first.migrator, Context::background());
    entered.wait("the first run inside up");
    let b = spawn(second.migrator, Context::background());
    gate.queued.wait("the second run queued on the lock");

    assert_eq!(
        gate.version_reads.load(Ordering::SeqCst),
        1,
        "the second run read the version before it held the lock"
    );
    release.open();
    a.join().unwrap().unwrap();
    b.join().unwrap().unwrap();
    assert_eq!(first_ups.load(Ordering::SeqCst), 1);
    assert_eq!(second_ups.load(Ordering::SeqCst), 0);
    assert_eq!(gate.state.lock().unwrap().version, "1.0.0");
}

#[test]
fn canceled_while_waiting_fails_the_schema_before_anything_else() {
    let tmp = tempfile::tempdir().unwrap();
    let gate = Arc::new(Gate::default());
    let (entered, release) = (Arc::new(Latch::default()), Arc::new(Latch::default()));
    let first = run_over(
        &gate,
        &tmp.path().join("a"),
        Some((entered.clone(), release.clone())),
    );
    let second = run_over(&gate, &tmp.path().join("b"), None);
    let second_ups = second.ups.clone();

    let a = spawn(first.migrator, Context::background());
    entered.wait("the first run inside up");
    let (ctx, cancel) = Context::background().with_cancel();
    let b = spawn(second.migrator, ctx);
    gate.queued.wait("the second run queued on the lock");
    cancel.cancel();

    let err = b.join().unwrap().unwrap_err();
    assert!(
        matches!(err, MigrateError::Lock { .. }),
        "second run error = {err}"
    );
    assert_eq!(err.to_string(), "migrate gate: lock: context canceled");
    assert_eq!(second_ups.load(Ordering::SeqCst), 0);
    assert_eq!(gate.version_reads.load(Ordering::SeqCst), 1);
    release.open();
    a.join().unwrap().unwrap();
}
