//! Ports go/runtime/sync clock_test.go, wallclock_test.go, diff_test.go,
//! merge_test.go and wire_doc_test.go: the hybrid logical clock, the wall
//! clocks, the diff record, conflict resolution and the pinned wire shape.
#![cfg(feature = "sync")]

use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use hop_top_kit::sync::{
    compute_diff, last_write_wins, resolve_diff, Clock, Diff, FixedClock, MockWallClock, Operation,
    SyncMode, Timestamp,
};
use serde::{Deserialize, Serialize};
use serde_json::json;

fn ts(physical: i64, logical: u32, node: &str) -> Timestamp {
    Timestamp {
        physical,
        logical,
        node_id: node.into(),
    }
}

fn june_2024() -> SystemTime {
    // 2024-06-01T12:00:00Z
    UNIX_EPOCH + Duration::from_secs(1_717_243_200)
}

fn nanos(t: SystemTime) -> i64 {
    t.duration_since(UNIX_EPOCH).unwrap().as_nanos() as i64
}

// --- clock_test.go

#[test]
fn clock_now_is_monotonic() {
    let c = Clock::new("node1");
    let mut prev = c.now();
    for i in 0..100 {
        let cur = c.now();
        assert!(
            prev.before(&cur),
            "iteration {i}: {prev:?} not before {cur:?}"
        );
        prev = cur;
    }
}

#[test]
fn clock_now_sets_node_id() {
    assert_eq!(Clock::new("abc").now().node_id, "abc");
}

#[test]
fn clock_update_takes_max() {
    let c = Clock::new("local");
    let local = c.now();
    let remote = ts(local.physical + 1_000_000_000, 5, "remote");
    let merged = c.update(&remote);
    assert!(merged.physical >= remote.physical);
    assert!(local.before(&merged));
    assert_eq!(merged.node_id, "local");
}

#[test]
fn timestamp_before_orders_physical_then_logical_then_node() {
    let a = ts(1, 0, "a");
    assert!(a.before(&ts(2, 0, "a")), "by physical");
    assert!(a.before(&ts(1, 1, "a")), "by logical");
    assert!(a.before(&ts(1, 0, "b")), "by node id");
    assert!(!a.before(&a.clone()), "irreflexive");
    assert!(!ts(2, 0, "a").before(&a));
}

#[test]
fn timestamp_equal() {
    let a = ts(10, 3, "x");
    let mut b = ts(10, 3, "x");
    assert!(a.equal(&b));
    b.logical = 4;
    assert!(!a.equal(&b));
}

#[test]
fn fixed_wall_clock_pins_physical_and_bumps_logical() {
    let pinned = june_2024();
    let c = Clock::with_wall_clock("node", Arc::new(FixedClock::new(pinned)));

    let first = c.now();
    assert_eq!(first.physical, nanos(pinned));
    assert_eq!(first.logical, 0);
    for i in 1..=5u32 {
        let t = c.now();
        assert_eq!(t.physical, nanos(pinned), "call {i}");
        assert_eq!(t.logical, i, "call {i}");
    }
}

#[test]
fn mock_wall_clock_advance_resets_logical() {
    let start = june_2024();
    let m = Arc::new(MockWallClock::new(start));
    let c = Clock::with_wall_clock("node", m.clone());

    assert_eq!(c.now().physical, nanos(start));
    m.advance(Duration::from_secs(1));
    let second = c.now();
    assert_eq!(second.physical, nanos(start + Duration::from_secs(1)));
    assert_eq!(second.logical, 0);
}

#[test]
fn update_with_equal_physical_takes_max_logical_plus_one() {
    let at = june_2024();
    let c = Clock::with_wall_clock("n", Arc::new(FixedClock::new(at)));
    let local = c.now(); // (p, 0)
    let remote = ts(local.physical, 7, "r");
    let merged = c.update(&remote);
    assert_eq!((merged.physical, merged.logical), (local.physical, 8));
}

#[test]
fn update_remote_ahead_adopts_its_physical_and_logical_plus_one() {
    let c = Clock::with_wall_clock("n", Arc::new(FixedClock::new(june_2024())));
    let remote = ts(nanos(june_2024()) + 10, 2, "r");
    let merged = c.update(&remote);
    assert_eq!((merged.physical, merged.logical), (remote.physical, 3));
}

#[test]
fn update_with_wall_ahead_of_both_resets_logical() {
    let m = Arc::new(MockWallClock::new(june_2024()));
    let c = Clock::with_wall_clock("n", m.clone());
    let first = c.now();
    m.advance(Duration::from_secs(5));
    let merged = c.update(&ts(first.physical - 1, 9, "r"));
    assert_eq!(merged.physical, nanos(june_2024() + Duration::from_secs(5)));
    assert_eq!(merged.logical, 0);
}

#[test]
fn clock_is_safe_across_threads() {
    let c = Arc::new(Clock::new("node"));
    let handles: Vec<_> = (0..50)
        .map(|_| {
            let c = c.clone();
            std::thread::spawn(move || {
                let mut last = c.now();
                for _ in 0..100 {
                    let n = c.now();
                    assert!(last.before(&n));
                    last = n;
                }
            })
        })
        .collect();
    for h in handles {
        h.join().unwrap();
    }
}

// --- wallclock_test.go

#[test]
fn system_wall_clock_is_non_decreasing_and_near_now() {
    use hop_top_kit::sync::{SystemWallClock, WallClock};
    let c = SystemWallClock;
    let mut prev = c.wall_time();
    for i in 0..1000 {
        let cur = c.wall_time();
        assert!(cur >= prev, "iteration {i}: went backward");
        prev = cur;
    }
    let drift = SystemTime::now()
        .duration_since(c.wall_time())
        .unwrap_or_default();
    assert!(drift < Duration::from_secs(1));
}

#[test]
fn fixed_clock_always_reports_the_same_time() {
    use hop_top_kit::sync::WallClock;
    let want = june_2024() + Duration::from_nanos(6);
    let c = FixedClock::new(want);
    for _ in 0..100 {
        assert_eq!(c.wall_time(), want);
    }
}

#[test]
fn mock_wall_clock_advance_shifts_by_the_exact_duration() {
    use hop_top_kit::sync::WallClock;
    let start = june_2024();
    let m = MockWallClock::new(start);
    assert_eq!(m.wall_time(), start);
    let mut cum = start;
    for d in [
        Duration::from_nanos(1),
        Duration::from_micros(1),
        Duration::from_millis(1),
        Duration::from_secs(1),
        Duration::from_secs(60),
        Duration::from_secs(3600),
        Duration::from_secs(86_400),
    ] {
        m.advance(d);
        cum += d;
        assert_eq!(m.wall_time(), cum, "after {d:?}");
    }
}

#[test]
fn mock_wall_clock_advance_zero_is_a_no_op() {
    use hop_top_kit::sync::WallClock;
    let m = MockWallClock::new(UNIX_EPOCH);
    m.advance(Duration::ZERO);
    assert_eq!(m.wall_time(), UNIX_EPOCH);
}

#[test]
fn mock_wall_clock_concurrent_advance_and_read() {
    use hop_top_kit::sync::WallClock;
    let m = Arc::new(MockWallClock::new(UNIX_EPOCH));
    let (writers, reads, writes) = (16u32, 1000, 100u32);
    let mut hs = Vec::new();
    for _ in 0..writers {
        let w = m.clone();
        hs.push(std::thread::spawn(move || {
            for _ in 0..writes {
                w.advance(Duration::from_nanos(1));
            }
        }));
        let r = m.clone();
        hs.push(std::thread::spawn(move || {
            for _ in 0..reads {
                let _ = r.wall_time();
            }
        }));
    }
    for h in hs {
        h.join().unwrap();
    }
    let got = m.wall_time().duration_since(UNIX_EPOCH).unwrap();
    assert_eq!(got, Duration::from_nanos(u64::from(writers * writes)));
}

// --- diff_test.go

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
struct Rec {
    id: String,
    name: String,
}

fn rec(name: &str) -> Rec {
    Rec {
        id: "1".into(),
        name: name.into(),
    }
}

#[test]
fn compute_diff_create() {
    let d = compute_diff("1", "test", None::<&Rec>, Some(&rec("foo")), ts(1, 0, "n1")).unwrap();
    assert_eq!(d.operation, Operation::Create);
    assert!(d.before.is_none());
    assert_eq!(
        d.after.as_deref(),
        Some(br#"{"id":"1","name":"foo"}"#.as_slice())
    );
    assert_eq!(
        (d.entity_id.as_str(), d.entity_type.as_str()),
        ("1", "test")
    );
    assert_eq!(d.node_id, "n1");
    assert_eq!(d.timestamp, ts(1, 0, "n1"));
}

#[test]
fn compute_diff_update() {
    let d = compute_diff(
        "1",
        "test",
        Some(&rec("foo")),
        Some(&rec("bar")),
        ts(2, 0, "n1"),
    )
    .unwrap();
    assert_eq!(d.operation, Operation::Update);
    assert!(d.before.is_some() && d.after.is_some());
}

#[test]
fn compute_diff_delete() {
    let d = compute_diff("1", "test", Some(&rec("foo")), None::<&Rec>, ts(3, 0, "n1")).unwrap();
    assert_eq!(d.operation, Operation::Delete);
    assert!(d.after.is_none());
}

#[test]
fn compute_diff_both_nil_is_an_error() {
    let err = compute_diff("1", "test", None::<&Rec>, None::<&Rec>, ts(4, 0, "n1")).unwrap_err();
    assert_eq!(err.to_string(), "sync: both before and after are nil");
}

#[test]
fn compute_diff_json_roundtrip() {
    let d = compute_diff(
        "1",
        "test",
        None::<&Rec>,
        Some(&rec("roundtrip")),
        ts(5, 0, "n1"),
    )
    .unwrap();
    let back: Diff = serde_json::from_slice(&serde_json::to_vec(&d).unwrap()).unwrap();
    assert_eq!(back, d);
}

// --- merge_test.go

fn diff_at(entity: &str, physical: i64, node: &str) -> Diff {
    Diff {
        entity_id: entity.into(),
        timestamp: ts(physical, 0, node),
        node_id: node.into(),
        ..Diff::default()
    }
}

#[test]
fn last_write_wins_picks_the_later() {
    let w = last_write_wins(diff_at("1", 10, "a"), diff_at("1", 20, "b"));
    assert_eq!(w.node_id, "b");
    let w = last_write_wins(diff_at("1", 20, "a"), diff_at("1", 10, "b"));
    assert_eq!(w.node_id, "a");
}

#[test]
fn last_write_wins_equal_timestamp_tiebreaks_on_node_id() {
    let mut local = diff_at("1", 10, "a");
    let remote = diff_at("1", 10, "b");
    assert_eq!(last_write_wins(local.clone(), remote.clone()).node_id, "b");

    local.timestamp.node_id = "z".into();
    local.node_id = "z".into();
    // a higher node id also wins from the local side
    assert_eq!(last_write_wins(local, remote).node_id, "z");
}

#[test]
fn last_write_wins_tiebreak_is_inclusive() {
    let t = ts(100, 1, "n");
    let local = Diff {
        entity_id: "local".into(),
        timestamp: t.clone(),
        node_id: "same".into(),
        ..Diff::default()
    };
    let remote = Diff {
        entity_id: "remote".into(),
        ..local.clone()
    };
    assert_eq!(last_write_wins(local, remote).entity_id, "local");

    let hi = Diff {
        entity_id: "hi".into(),
        timestamp: t.clone(),
        node_id: "b".into(),
        ..Diff::default()
    };
    let lo = Diff {
        entity_id: "lo".into(),
        timestamp: t,
        node_id: "a".into(),
        ..Diff::default()
    };
    assert_eq!(last_write_wins(lo.clone(), hi.clone()).entity_id, "hi");
    assert_eq!(last_write_wins(hi, lo).entity_id, "hi");
}

#[derive(Debug, PartialEq, Serialize, Deserialize, Default)]
struct Counter {
    count: i64,
}

#[test]
fn resolve_diff_applies_the_custom_merge() {
    let after = |n| Some(serde_json::to_vec(&Counter { count: n }).unwrap());
    let local = Diff {
        operation: Operation::Update,
        after: after(5),
        ..diff_at("1", 10, "a")
    };
    let remote = Diff {
        operation: Operation::Update,
        after: after(3),
        ..diff_at("1", 20, "b")
    };
    let merged = resolve_diff(&local, &remote, |l: Counter, r: Counter| {
        Ok(Counter {
            count: l.count + r.count,
        })
    })
    .unwrap();
    let got: Counter = serde_json::from_slice(merged.after.as_deref().unwrap()).unwrap();
    assert_eq!(got.count, 8);
    // the result is the later-timestamp diff with the merged payload
    assert_eq!(merged.node_id, "b");
}

#[test]
fn resolve_diff_missing_after_merges_the_default() {
    let local = Diff {
        after: Some(br#"{"count":4}"#.to_vec()),
        ..diff_at("1", 1, "a")
    };
    let remote = diff_at("1", 2, "b"); // delete: no After
    let merged = resolve_diff(&local, &remote, |l: Counter, r: Counter| {
        assert_eq!(r, Counter::default());
        Ok(l)
    })
    .unwrap();
    assert_eq!(merged.after.as_deref(), Some(br#"{"count":4}"#.as_slice()));
}

#[test]
fn resolve_diff_surfaces_decode_and_merge_errors() {
    let bad = Diff {
        after: Some(b"{nope".to_vec()),
        ..diff_at("1", 1, "a")
    };
    let ok = diff_at("1", 2, "b");
    assert!(resolve_diff(&bad, &ok, |l: Counter, _| Ok(l)).is_err());
    assert!(resolve_diff(&ok, &bad, |l: Counter, _| Ok(l)).is_err());
    let err = resolve_diff(&ok, &ok, |_: Counter, _: Counter| Err("no merge".into())).unwrap_err();
    assert_eq!(err.to_string(), "no merge");
}

// --- wire_doc_test.go

#[test]
fn sync_mode_wire_values() {
    assert_eq!(i32::from(SyncMode::Bidirectional), 0);
    assert_eq!(i32::from(SyncMode::PushOnly), 1);
    assert_eq!(i32::from(SyncMode::PullOnly), 2);
    assert_eq!(SyncMode::default(), SyncMode::Bidirectional);
}

#[test]
fn operation_wire_values() {
    assert_eq!(serde_json::to_value(Operation::Create).unwrap(), json!(0));
    assert_eq!(serde_json::to_value(Operation::Update).unwrap(), json!(1));
    assert_eq!(serde_json::to_value(Operation::Delete).unwrap(), json!(2));
    assert_eq!(
        serde_json::from_value::<Operation>(json!(2)).unwrap(),
        Operation::Delete
    );
    assert!(serde_json::from_value::<Operation>(json!(3)).is_err());
}

#[test]
fn timestamp_json_keys() {
    let v = serde_json::to_value(ts(1_713_520_000_000_000_000, 1, "abc123")).unwrap();
    assert_eq!(
        v,
        json!({"physical": 1_713_520_000_000_000_000i64, "logical": 1, "node_id": "abc123"})
    );
}

#[test]
fn diff_json_keys() {
    let d = Diff {
        entity_id: "e1".into(),
        entity_type: "notes".into(),
        operation: Operation::Update,
        before: Some(br#"{"a":1}"#.to_vec()),
        after: Some(br#"{"a":2}"#.to_vec()),
        timestamp: ts(1, 0, "n1"),
        node_id: "n1".into(),
    };
    let v = serde_json::to_value(&d).unwrap();
    for key in [
        "entity_id",
        "entity_type",
        "operation",
        "before",
        "after",
        "timestamp",
        "node_id",
    ] {
        assert!(v.get(key).is_some(), "Diff JSON missing {key}: {v}");
    }
}

/// Go's `[]byte` fields marshal as base64, so a Go peer sees the same
/// bytes; empty and absent snapshots are omitted (`omitempty`).
#[test]
fn diff_snapshots_are_base64_and_omitted_when_empty() {
    let d = Diff {
        after: Some(br#"{"a":2}"#.to_vec()),
        ..diff_at("e1", 1, "n1")
    };
    let v = serde_json::to_value(&d).unwrap();
    assert_eq!(v["after"], json!("eyJhIjoyfQ=="));
    assert!(v.get("before").is_none());

    let empty = Diff {
        before: Some(Vec::new()),
        ..diff_at("e1", 1, "n1")
    };
    assert!(serde_json::to_value(&empty)
        .unwrap()
        .get("before")
        .is_none());
}

#[test]
fn diff_decodes_what_go_writes() {
    let raw = json!({
        "entity_id": "e1", "entity_type": "notes", "operation": 1,
        "after": "eyJhIjoyfQ==", "timestamp": {"physical": 7, "logical": 2, "node_id": "n"},
        "node_id": "n"
    });
    let d: Diff = serde_json::from_value(raw).unwrap();
    assert_eq!(d.operation, Operation::Update);
    assert!(d.before.is_none());
    assert_eq!(d.after.as_deref(), Some(br#"{"a":2}"#.as_slice()));
    assert_eq!(d.timestamp, ts(7, 2, "n"));

    // null and absent decode to nil, as Go's encoding/json does
    let d: Diff = serde_json::from_value(json!({"entity_id": "e", "after": null})).unwrap();
    assert!(d.after.is_none());
}
