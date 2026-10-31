//! Ports go/runtime/sync replicator_test.go, status_test.go and
//! sync_log_test.go. Where Go sleeps through a tick, these drive the
//! pull and push by hand (`sync_once`), and the loop tests poll.
#![cfg(feature = "sync")]

#[path = "sync_support/mod.rs"]
mod support;

use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use hop_top_kit::bus::{DynBus, Event, Mode};
use hop_top_kit::sync::{
    Diff, FixedClock, MemoryTransport, MockWallClock, Operation, Remote, Replicator,
    ReplicatorBuilder, SyncError, SyncMode, Timestamp, Transport, TransportError,
    DEFAULT_SUBSCRIPTION_PREFIX, MAX_QUEUE_SIZE,
};
use serde_json::json;

use support::{bg, eventually, ErrRepo, Item, Lines, MemRepo};

const HOUR: Duration = Duration::from_secs(3600);

fn stamp(physical: i64) -> Timestamp {
    Timestamp {
        physical,
        logical: 0,
        node_id: "local".into(),
    }
}

fn create(id: &str, physical: i64) -> Diff {
    Diff {
        entity_id: id.into(),
        operation: Operation::Create,
        timestamp: stamp(physical),
        ..Diff::default()
    }
}

fn builder(repo: Arc<MemRepo>) -> ReplicatorBuilder<Item> {
    Replicator::builder(repo).interval(HOUR)
}

fn remote(name: &str, mt: &Arc<MemoryTransport>, mode: SyncMode) -> Remote {
    Remote::new(name, mt.clone()).mode(mode)
}

// --- replicator_test.go

#[test]
fn single_remote_push() {
    let mt = Arc::new(MemoryTransport::new());
    let rep = builder(MemRepo::new())
        .remote(remote("origin", &mt, SyncMode::PushOnly))
        .build();
    rep.enqueue(Diff {
        after: Some(br#"{"id":"e1","name":"test"}"#.to_vec()),
        ..create("e1", 100)
    });

    rep.start(&bg()).unwrap();
    rep.sync_once(&bg(), "origin").unwrap();
    rep.stop();

    let diffs = mt.diffs();
    assert_eq!(diffs.len(), 1);
    assert_eq!(diffs[0].entity_id, "e1");
}

#[test]
fn single_remote_pull_creates_the_entity() {
    let repo = MemRepo::new();
    let mt = Arc::new(MemoryTransport::new());
    mt.push(
        &bg(),
        &[Diff {
            timestamp: Timestamp {
                physical: 50,
                logical: 0,
                node_id: "remote".into(),
            },
            after: Some(br#"{"id":"e1","name":"pulled"}"#.to_vec()),
            ..create("e1", 50)
        }],
    )
    .unwrap();
    let rep = builder(repo.clone())
        .remote(remote("origin", &mt, SyncMode::PullOnly))
        .build();

    rep.sync_once(&bg(), "origin").unwrap();
    assert_eq!(repo.find("e1").unwrap().name, "pulled");
}

#[test]
fn pull_advances_the_watermark_so_a_diff_applies_once() {
    let repo = MemRepo::new();
    let mt = Arc::new(MemoryTransport::new());
    let put = |name: &str, p: i64| {
        mt.push(
            &bg(),
            &[Diff {
                after: Some(format!(r#"{{"id":"e1","name":"{name}"}}"#).into_bytes()),
                ..create("e1", p)
            }],
        )
        .unwrap();
    };
    put("first", 10);
    let rep = builder(repo.clone())
        .remote(remote("origin", &mt, SyncMode::PullOnly))
        .build();
    rep.sync_once(&bg(), "origin").unwrap();
    assert_eq!(repo.find("e1").unwrap().name, "first");

    // A local edit must survive the next pull when nothing newer arrived.
    repo.seed(Item::new("e1", "local-edit"));
    rep.sync_once(&bg(), "origin").unwrap();
    assert_eq!(repo.find("e1").unwrap().name, "local-edit");

    put("second", 20);
    rep.sync_once(&bg(), "origin").unwrap();
    assert_eq!(repo.find("e1").unwrap().name, "second");
}

#[test]
fn pull_folds_remote_timestamps_into_the_clock() {
    let mt = Arc::new(MemoryTransport::new());
    mt.push(&bg(), &[create("e1", i64::MAX / 2)]).unwrap();
    let rep = builder(MemRepo::new())
        .remote(remote("origin", &mt, SyncMode::PullOnly))
        .build();
    rep.sync_once(&bg(), "origin").unwrap();
    assert!(rep.clock().now().physical >= i64::MAX / 2);
}

#[test]
fn multi_remote_each_gets_the_diff() {
    let (mt1, mt2) = (
        Arc::new(MemoryTransport::new()),
        Arc::new(MemoryTransport::new()),
    );
    let rep = builder(MemRepo::new())
        .remote(remote("r1", &mt1, SyncMode::PushOnly))
        .remote(remote("r2", &mt2, SyncMode::PushOnly))
        .build();
    rep.enqueue(create("e1", 1));
    rep.start(&bg()).unwrap();
    rep.sync_once(&bg(), "r1").unwrap();
    rep.sync_once(&bg(), "r2").unwrap();
    rep.stop();
    assert_eq!(mt1.diffs().len(), 1);
    assert_eq!(mt2.diffs().len(), 1);
}

#[test]
fn filter_limits_what_is_pushed() {
    let mt = Arc::new(MemoryTransport::new());
    let rem = remote("filtered", &mt, SyncMode::PushOnly).filter(|d: &Diff| d.entity_id == "e2");
    let rep = builder(MemRepo::new()).remote(rem).build();
    rep.enqueue(create("e1", 1));
    rep.enqueue(create("e2", 2));
    rep.sync_once(&bg(), "filtered").unwrap();

    let diffs = mt.diffs();
    assert_eq!(diffs.len(), 1);
    assert_eq!(diffs[0].entity_id, "e2");
    // the filtered-out diff is consumed, not retried
    assert_eq!(rep.status()[0].pending_diffs, 0);
}

#[test]
fn queue_is_bounded_and_drops_the_oldest() {
    let rep = builder(MemRepo::new()).build();
    for i in 0..10_100 {
        rep.enqueue(create(&format!("e{i}"), i));
    }
    assert_eq!(MAX_QUEUE_SIZE, 10_000);
    assert_eq!(rep.queue_len(), 10_000);
    let q = rep.queued();
    assert_eq!(q.len(), 10_000);
    assert_eq!(q[0].entity_id, "e100");
    assert_eq!(q[9_999].entity_id, "e10099");
}

#[test]
fn overflow_does_not_skip_a_diff_the_remote_has_not_seen() {
    // Dropping the oldest entry must not shift a remote's place in the
    // queue: it has seen e0..e2 and must still get e3 onward.
    // "slow" never syncs, so nothing is trimmed and the cursor of "r" sits
    // inside the queue when the overflow drops its front.
    let mt = Arc::new(MemoryTransport::new());
    let slow = Arc::new(MemoryTransport::new());
    let rep = builder(MemRepo::new())
        .remote(remote("r", &mt, SyncMode::PushOnly))
        .remote(remote("slow", &slow, SyncMode::PushOnly))
        .build();
    for i in 0..3 {
        rep.enqueue(create(&format!("e{i}"), i));
    }
    rep.sync_once(&bg(), "r").unwrap();
    assert_eq!(mt.diffs().len(), 3);

    for i in 3..(3 + MAX_QUEUE_SIZE as i64 + 1) {
        rep.enqueue(create(&format!("e{i}"), i));
    }
    rep.sync_once(&bg(), "r").unwrap();
    let ids: Vec<_> = mt.diffs().into_iter().map(|d| d.entity_id).collect();
    assert_eq!(ids.len(), 3 + MAX_QUEUE_SIZE);
    assert_eq!(ids[3], "e4", "e3 was dropped by overflow before the sync");
    assert_eq!(ids.last().unwrap(), &format!("e{}", 3 + MAX_QUEUE_SIZE));
}

#[test]
fn mode_pull_only_never_pushes() {
    let mt = Arc::new(MemoryTransport::new());
    let rep = builder(MemRepo::new())
        .remote(remote("pull-only", &mt, SyncMode::PullOnly))
        .build();
    rep.enqueue(create("e1", 1));
    rep.sync_once(&bg(), "pull-only").unwrap();
    assert!(mt.diffs().is_empty());
}

#[test]
fn mode_push_only_never_pulls() {
    let repo = MemRepo::new();
    let mt = Arc::new(MemoryTransport::new());
    mt.push(
        &bg(),
        &[Diff {
            after: Some(br#"{"id":"e9","name":"x"}"#.to_vec()),
            ..create("e9", 5)
        }],
    )
    .unwrap();
    let rep = builder(repo.clone())
        .remote(remote("push-only", &mt, SyncMode::PushOnly))
        .build();
    rep.sync_once(&bg(), "push-only").unwrap();
    assert!(repo.find("e9").is_none());
}

#[test]
fn bidirectional_pushes_and_pulls() {
    let repo = MemRepo::new();
    let mt = Arc::new(MemoryTransport::new());
    mt.push(
        &bg(),
        &[Diff {
            after: Some(br#"{"id":"in","name":"x"}"#.to_vec()),
            ..create("in", 5)
        }],
    )
    .unwrap();
    let rep = builder(repo.clone())
        .remote(remote("both", &mt, SyncMode::Bidirectional))
        .build();
    rep.enqueue(create("out", 6));
    rep.sync_once(&bg(), "both").unwrap();
    assert!(repo.find("in").is_some());
    assert!(mt.diffs().iter().any(|d| d.entity_id == "out"));
}

#[test]
fn sync_once_unknown_remote() {
    let rep = builder(MemRepo::new()).build();
    let err = rep.sync_once(&bg(), "ghost").unwrap_err();
    assert!(matches!(err, SyncError::RemoteNotFound(_)));
}

#[test]
fn a_failing_push_keeps_the_diff_for_the_next_round() {
    struct Flaky {
        inner: MemoryTransport,
        fail: Mutex<bool>,
    }
    impl Transport for Flaky {
        fn push(&self, c: &hop_top_kit::bus::Context, d: &[Diff]) -> Result<(), TransportError> {
            if *self.fail.lock().unwrap() {
                return Err(TransportError::new("push: status 500"));
            }
            self.inner.push(c, d)
        }
        fn pull(
            &self,
            c: &hop_top_kit::bus::Context,
            s: &Timestamp,
        ) -> Result<Vec<Diff>, TransportError> {
            self.inner.pull(c, s)
        }
        fn ping(&self, c: &hop_top_kit::bus::Context) -> Result<(), TransportError> {
            self.inner.ping(c)
        }
    }
    let flaky = Arc::new(Flaky {
        inner: MemoryTransport::new(),
        fail: Mutex::new(true),
    });
    let rep = builder(MemRepo::new())
        .remote(Remote::new("r", flaky.clone()).mode(SyncMode::PushOnly))
        .build();
    rep.enqueue(create("e1", 1));

    rep.sync_once(&bg(), "r").unwrap();
    let st = &rep.status()[0];
    assert!(st.connected, "ping succeeded");
    assert_eq!(
        st.last_error.as_ref().unwrap().to_string(),
        "push: status 500"
    );
    assert_eq!(st.pending_diffs, 1);

    *flaky.fail.lock().unwrap() = false;
    rep.sync_once(&bg(), "r").unwrap();
    assert_eq!(flaky.inner.diffs().len(), 1);
    let st = &rep.status()[0];
    assert!(st.last_error.is_none(), "cleared by the next ping");
    assert_eq!(st.pending_diffs, 0);
}

#[test]
fn the_queue_trims_once_every_remote_has_it() {
    let (a, b) = (
        Arc::new(MemoryTransport::new()),
        Arc::new(MemoryTransport::new()),
    );
    let rep = builder(MemRepo::new())
        .remote(remote("a", &a, SyncMode::PushOnly))
        .remote(remote("b", &b, SyncMode::PushOnly))
        .build();
    rep.enqueue(create("e1", 1));
    rep.sync_once(&bg(), "a").unwrap();
    assert_eq!(rep.queue_len(), 1, "b has not seen it");
    rep.sync_once(&bg(), "b").unwrap();
    assert_eq!(rep.queue_len(), 0);
}

// --- bus subscription

fn bus_rep(repo: Arc<MemRepo>, bus: &DynBus, prefix: Option<&str>) -> Replicator<Item> {
    let mut b = builder(repo).bus(bus.clone());
    if let Some(p) = prefix {
        b = b.subscription_prefix(p);
    }
    b.build()
}

fn publish(bus: &DynBus, topic: &str, id: &str) {
    bus.publish(&Event::new(topic, "test", json!({ "id": id })))
        .unwrap();
}

#[test]
fn default_prefix_subscription_dispatches_each_action() {
    assert_eq!(DEFAULT_SUBSCRIPTION_PREFIX, "kit.runtime.entity");
    let repo = MemRepo::new();
    repo.seed(Item::new("e1", "n1"));
    let bus = DynBus::new();
    let rep = bus_rep(repo, &bus, None);
    rep.start(&bg()).unwrap();

    publish(&bus, "kit.runtime.entity.created", "e1");
    publish(&bus, "kit.runtime.entity.updated", "e1");
    publish(&bus, "kit.runtime.entity.deleted", "e1");

    let q = rep.queued();
    assert_eq!(q.len(), 3);
    assert_eq!(q[0].operation, Operation::Create);
    assert_eq!(q[1].operation, Operation::Update);
    assert_eq!(q[2].operation, Operation::Delete);
    // create/update snapshot the live entity; delete carries none
    assert_eq!(
        q[0].after.as_deref(),
        Some(br#"{"id":"e1","name":"n1"}"#.as_slice())
    );
    assert!(q[2].after.is_none());
    assert!(q
        .iter()
        .all(|d| d.entity_id == "e1" && d.node_id == "local"));
    assert!(q[0].timestamp.before(&q[1].timestamp));
    rep.stop();
}

#[test]
fn custom_prefix_routes_only_its_own_topics() {
    let repo = MemRepo::new();
    repo.seed(Item::new("w1", "ws1"));
    let bus = DynBus::new();
    let rep = bus_rep(repo, &bus, Some("wsm.runtime.workspace"));
    rep.start(&bg()).unwrap();

    publish(&bus, "wsm.runtime.workspace.created", "w1");
    publish(&bus, "wsm.runtime.workspace.deleted", "w1");
    publish(&bus, "kit.runtime.entity.created", "ignored");

    let q = rep.queued();
    assert_eq!(q.len(), 2);
    assert_eq!(q[0].entity_id, "w1");
    assert_eq!(q[0].operation, Operation::Create);
    assert_eq!(q[1].operation, Operation::Delete);
    rep.stop();
}

#[test]
fn unknown_action_and_malformed_payloads_are_ignored() {
    let bus = DynBus::builder().enforce(Mode::Warn).build();
    let rep = bus_rep(MemRepo::new(), &bus, Some("myapp.runtime.thing"));
    rep.start(&bg()).unwrap();

    publish(&bus, "myapp.runtime.thing.archived", "x1");
    bus.publish(&Event::new("myapp.runtime.thing.created", "t", json!("id")))
        .unwrap();
    bus.publish(&Event::new(
        "myapp.runtime.thing.created",
        "t",
        json!({"id": ""}),
    ))
    .unwrap();
    bus.publish(&Event::new(
        "myapp.runtime.thing.created",
        "t",
        json!({"id": 7}),
    ))
    .unwrap();
    assert_eq!(rep.queue_len(), 0);
    rep.stop();
}

#[test]
fn stop_unsubscribes_from_the_bus() {
    let bus = DynBus::new();
    let rep = bus_rep(MemRepo::new(), &bus, None);
    rep.start(&bg()).unwrap();
    rep.stop();
    publish(&bus, "kit.runtime.entity.deleted", "e1");
    assert_eq!(rep.queue_len(), 0);
}

#[test]
fn invalid_subscription_prefixes_panic() {
    for p in [
        "",
        "too.few",
        "too.many.segments.here",
        "trailing.dot.",
        "empty..segment",
    ] {
        let r = std::panic::catch_unwind(|| {
            let _ = builder(MemRepo::new()).subscription_prefix(p);
        });
        assert!(r.is_err(), "{p:?} should panic");
    }
}

#[test]
fn sync_events_are_published_on_the_bus() {
    let bus = DynBus::builder().enforce(Mode::Off).build();
    let seen = Arc::new(Mutex::new(Vec::<(String, String, serde_json::Value)>::new()));
    let s = seen.clone();
    bus.subscribe("sync.remote.*", move |e| {
        s.lock()
            .unwrap()
            .push((e.topic.to_string(), e.source.clone(), e.payload.clone()));
        Ok(())
    });
    let mt = Arc::new(MemoryTransport::new());
    let rep = builder(MemRepo::new())
        .bus(bus.clone())
        .remote(remote("origin", &mt, SyncMode::Bidirectional))
        .build();
    rep.sync_once(&bg(), "origin").unwrap();
    mt.set_alive(false);
    rep.sync_once(&bg(), "origin").unwrap();

    let got = seen.lock().unwrap().clone();
    let topics: Vec<_> = got.iter().map(|g| g.0.as_str()).collect();
    assert_eq!(
        topics,
        [
            "sync.remote.connected",
            "sync.remote.synced",
            "sync.remote.error"
        ]
    );
    assert_eq!(got[0].1, "sync.replicator.origin");
    assert_eq!(got[0].2, json!({"remote": "origin"}));
}

// --- start / stop / add_remote

#[test]
fn start_loops_sync_on_the_interval_and_stop_joins_them() {
    let repo = MemRepo::new();
    let mt = Arc::new(MemoryTransport::new());
    mt.push(
        &bg(),
        &[Diff {
            after: Some(br#"{"id":"e1","name":"pulled"}"#.to_vec()),
            ..create("e1", 50)
        }],
    )
    .unwrap();
    let rep = Replicator::builder(repo.clone())
        .interval(Duration::from_millis(20))
        .remote(remote("origin", &mt, SyncMode::Bidirectional))
        .build();
    rep.enqueue(create("out", 60));

    rep.start(&bg()).unwrap();
    assert!(eventually(|| repo.find("e1").is_some()));
    assert!(eventually(|| mt
        .diffs()
        .iter()
        .any(|d| d.entity_id == "out")));
    rep.stop();

    // after stop, no loop touches the repo
    repo.seed(Item::new("e1", "after-stop"));
    std::thread::sleep(Duration::from_millis(80));
    assert_eq!(repo.find("e1").unwrap().name, "after-stop");
    rep.stop(); // idempotent
}

#[test]
fn cancelling_the_context_ends_the_loops() {
    let mt = Arc::new(MemoryTransport::new());
    let rep = Replicator::builder(MemRepo::new())
        .interval(Duration::from_millis(10))
        .remote(remote("origin", &mt, SyncMode::PushOnly))
        .build();
    let (ctx, cancel) = bg().with_cancel();
    rep.start(&ctx).unwrap();
    cancel.cancel();
    std::thread::sleep(Duration::from_millis(60));
    rep.enqueue(create("late", 1));
    std::thread::sleep(Duration::from_millis(60));
    assert!(mt.diffs().is_empty(), "loop exited with its context");
    rep.stop();
}

#[test]
fn add_remote_needs_a_started_replicator() {
    let rep = builder(MemRepo::new()).build();
    let mt = Arc::new(MemoryTransport::new());
    let err = rep
        .add_remote(remote("late", &mt, SyncMode::PushOnly))
        .unwrap_err();
    assert_eq!(err.to_string(), "sync: replicator not started");
}

#[test]
fn add_remote_starts_its_loop_and_remove_remote_stops_it() {
    let rep = Replicator::builder(MemRepo::new())
        .interval(Duration::from_millis(10))
        .build();
    rep.start(&bg()).unwrap();
    let mt = Arc::new(MemoryTransport::new());
    rep.add_remote(remote("late", &mt, SyncMode::PushOnly))
        .unwrap();
    assert!(rep
        .add_remote(remote("late", &mt, SyncMode::PushOnly))
        .is_err());

    rep.enqueue(create("e1", 1));
    assert!(eventually(|| mt.diffs().len() == 1));

    rep.remove_remote("late").unwrap();
    assert!(rep.remove_remote("late").is_err());
    std::thread::sleep(Duration::from_millis(40));
    rep.enqueue(create("e2", 2));
    std::thread::sleep(Duration::from_millis(60));
    assert_eq!(mt.diffs().len(), 1, "a removed remote is no longer synced");
    rep.stop();
}

// --- status_test.go

#[test]
fn status_connected_after_a_sync() {
    let mt = Arc::new(MemoryTransport::new());
    let rep = builder(MemRepo::new())
        .remote(remote("origin", &mt, SyncMode::Bidirectional))
        .build();
    rep.start(&bg()).unwrap();
    rep.sync_once(&bg(), "origin").unwrap();
    let st = rep.status();
    assert_eq!(st.len(), 1);
    assert_eq!(st[0].name, "origin");
    assert!(st[0].connected);
    assert!(st[0].last_error.is_none());
    assert!(st[0].last_sync.is_some());
    rep.stop();
}

#[test]
fn status_pending_count_before_any_sync() {
    let mt = Arc::new(MemoryTransport::new());
    let rep = builder(MemRepo::new())
        .remote(remote("origin", &mt, SyncMode::PushOnly))
        .build();
    rep.enqueue(create("e1", 1));
    rep.enqueue(create("e2", 2));
    rep.start(&bg()).unwrap();
    let st = rep.status();
    assert_eq!(st.len(), 1);
    assert_eq!(st[0].pending_diffs, 2);
    assert!(!st[0].connected);
    assert!(st[0].last_sync.is_none());
    assert_eq!(st[0].lag, Duration::ZERO);
    rep.stop();
}

#[test]
fn status_tracks_an_unreachable_remote() {
    let mt = Arc::new(MemoryTransport::new());
    mt.set_alive(false);
    let rep = builder(MemRepo::new())
        .remote(remote("origin", &mt, SyncMode::Bidirectional))
        .build();
    rep.start(&bg()).unwrap();
    rep.sync_once(&bg(), "origin").unwrap();
    let st = rep.status();
    assert!(!st[0].connected);
    assert_eq!(
        st[0].last_error.as_ref().unwrap().to_string(),
        "transport: not reachable"
    );
    assert!(
        st[0].last_sync.is_none(),
        "an unreachable remote never synced"
    );
    rep.stop();
}

#[test]
fn status_lag_runs_on_the_injected_wall_clock() {
    let start = UNIX_EPOCH + Duration::from_secs(1_717_243_200);
    let wall = Arc::new(MockWallClock::new(start));
    let mt = Arc::new(MemoryTransport::new());
    let rep = builder(MemRepo::new())
        .wall_clock(wall.clone())
        .remote(remote("origin", &mt, SyncMode::Bidirectional))
        .build();
    rep.start(&bg()).unwrap();
    rep.sync_once(&bg(), "origin").unwrap();
    assert_eq!(rep.status()[0].last_sync, Some(start));
    assert_eq!(rep.status()[0].lag, Duration::ZERO);

    wall.advance(Duration::from_secs(90));
    assert_eq!(rep.status()[0].lag, Duration::from_secs(90));
    rep.stop();
}

#[test]
fn status_is_ordered_by_remote_name() {
    let mt = Arc::new(MemoryTransport::new());
    let rep = builder(MemRepo::new())
        .remote(remote("b", &mt, SyncMode::PushOnly))
        .remote(remote("a", &mt, SyncMode::PushOnly))
        .build();
    rep.start(&bg()).unwrap();
    let names: Vec<_> = rep.status().into_iter().map(|s| s.name).collect();
    assert_eq!(names, ["a", "b"]);
    rep.stop();
}

// --- sync_log_test.go

#[test]
fn apply_delete_error_goes_to_the_logger() {
    let lines = Lines::new();
    let rep: Replicator<Item> = Replicator::builder(Arc::new(ErrRepo {
        delete_err: Some("boom delete"),
        ..ErrRepo::default()
    }))
    .logger(lines.clone())
    .build();
    rep.apply_diff(
        &bg(),
        &Diff {
            operation: Operation::Delete,
            ..create("e-del", 1)
        },
    );
    let out = lines.all();
    assert!(out.contains("sync: apply delete failed"), "{out}");
    assert!(out.contains("entity_id=e-del"), "{out}");
    assert!(out.contains("err=boom delete"), "{out}");
}

#[test]
fn apply_create_error_goes_to_the_logger_after_update_misses() {
    let lines = Lines::new();
    let rep: Replicator<Item> = Replicator::builder(Arc::new(ErrRepo {
        update_not_found: true,
        create_err: Some("boom create"),
        ..ErrRepo::default()
    }))
    .logger(lines.clone())
    .build();
    rep.apply_diff(
        &bg(),
        &Diff {
            after: Some(br#"{"id":"e-new","name":"x"}"#.to_vec()),
            ..create("e-new", 1)
        },
    );
    let out = lines.all();
    assert!(out.contains("sync: apply create failed"), "{out}");
    assert!(
        out.contains("e-new") && out.contains("boom create"),
        "{out}"
    );
}

#[test]
fn apply_falls_back_to_create_on_any_update_error() {
    // Go tries Create after any Update error, not only not-found.
    let lines = Lines::new();
    let rep: Replicator<Item> = Replicator::builder(Arc::new(ErrRepo {
        update_err: Some("locked"),
        ..ErrRepo::default()
    }))
    .logger(lines.clone())
    .build();
    rep.apply_diff(
        &bg(),
        &Diff {
            after: Some(br#"{"id":"e1","name":"x"}"#.to_vec()),
            ..create("e1", 1)
        },
    );
    assert_eq!(lines.all(), "", "create succeeded");
}

#[test]
fn apply_decode_error_goes_to_the_logger() {
    let lines = Lines::new();
    let rep: Replicator<Item> = Replicator::builder(MemRepo::new())
        .logger(lines.clone())
        .build();
    rep.apply_diff(
        &bg(),
        &Diff {
            operation: Operation::Update,
            after: Some(b"{not-json".to_vec()),
            ..create("e-bad", 1)
        },
    );
    let out = lines.all();
    assert!(out.contains("sync: unmarshal diff failed"), "{out}");
    assert!(out.contains("e-bad"), "{out}");
}

#[test]
fn apply_errors_publish_sync_apply_error() {
    let bus = DynBus::builder().enforce(Mode::Off).build();
    let seen = Arc::new(Mutex::new(Vec::<serde_json::Value>::new()));
    let s = seen.clone();
    bus.subscribe("sync.apply.error", move |e| {
        s.lock().unwrap().push(e.payload.clone());
        Ok(())
    });
    let rep: Replicator<Item> = Replicator::builder(MemRepo::new())
        .bus(bus)
        .logger(Lines::new())
        .build();
    rep.apply_diff(
        &bg(),
        &Diff {
            after: Some(b"{".to_vec()),
            ..create("e-bad", 1)
        },
    );
    assert_eq!(*seen.lock().unwrap(), [json!({"remote": "e-bad"})]);
}

#[test]
fn a_default_logger_is_always_set() {
    // No logger given: building and applying a failing diff must not panic.
    let rep: Replicator<Item> = Replicator::builder(MemRepo::new()).build();
    rep.apply_diff(
        &bg(),
        &Diff {
            after: Some(b"{".to_vec()),
            ..create("x", 1)
        },
    );
}

// --- clock wiring

#[test]
fn the_hlc_uses_the_wall_clock_given_to_the_builder() {
    let at = UNIX_EPOCH + Duration::from_secs(10);
    let rep: Replicator<Item> = Replicator::builder(MemRepo::new())
        .wall_clock(Arc::new(FixedClock::new(at)))
        .build();
    assert_eq!(rep.clock().now().physical, 10_000_000_000);
    assert_eq!(rep.clock().now().node_id, "local");
}

#[test]
fn an_explicit_hlc_wins() {
    let clock = Arc::new(hop_top_kit::sync::Clock::with_wall_clock(
        "node-7",
        Arc::new(FixedClock::new(SystemTime::UNIX_EPOCH)),
    ));
    let rep: Replicator<Item> = Replicator::builder(MemRepo::new()).clock(clock).build();
    assert_eq!(rep.clock().now().node_id, "node-7");
}

#[test]
fn a_zero_interval_is_refused_at_build() {
    let r = std::panic::catch_unwind(|| {
        let _ = Replicator::<Item>::builder(MemRepo::new())
            .interval(Duration::ZERO)
            .build();
    });
    assert!(r.is_err());
}

#[test]
fn replicator_is_send_and_sync() {
    fn assert<T: Send + Sync>() {}
    assert::<Replicator<Item>>();
}
