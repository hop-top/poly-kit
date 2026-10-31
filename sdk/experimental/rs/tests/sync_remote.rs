//! Ports go/runtime/sync remote_test.go and the memory-transport half of
//! transport_test.go.
#![cfg(feature = "sync")]

use std::sync::Arc;

use hop_top_kit::bus::Context;
use hop_top_kit::sync::{
    Diff, MemoryTransport, Operation, Remote, RemoteSet, SyncMode, Timestamp, Transport,
};

fn mem() -> Arc<MemoryTransport> {
    Arc::new(MemoryTransport::new())
}

fn remote(name: &str) -> Remote {
    Remote::new(name, mem())
}

fn at(physical: i64, logical: u32) -> Timestamp {
    Timestamp {
        physical,
        logical,
        node_id: "n1".into(),
    }
}

#[test]
fn remote_set_add_get_remove() {
    let rs = RemoteSet::new();
    rs.add(remote("origin")).unwrap();
    assert_eq!(rs.len(), 1);

    let got = rs.get("origin").expect("remote");
    assert_eq!(got.name, "origin");
    assert_eq!(got.mode, SyncMode::Bidirectional);

    rs.remove("origin").unwrap();
    assert_eq!(rs.len(), 0);
    assert!(rs.is_empty());
    assert!(rs.get("origin").is_none());
}

#[test]
fn remote_set_duplicate_name_is_an_error() {
    let rs = RemoteSet::new();
    rs.add(remote("dup")).unwrap();
    let err = rs.add(remote("dup")).unwrap_err();
    assert_eq!(err.to_string(), "sync: remote already exists: dup");
    assert_eq!(rs.len(), 1);
}

#[test]
fn remote_set_remove_not_found() {
    let err = RemoteSet::new().remove("nope").unwrap_err();
    assert_eq!(err.to_string(), "sync: remote not found: nope");
}

#[test]
fn remote_set_list_has_every_remote() {
    let rs = RemoteSet::new();
    rs.add(remote("a").mode(SyncMode::PushOnly)).unwrap();
    rs.add(remote("b").mode(SyncMode::PullOnly)).unwrap();
    let mut names: Vec<_> = rs.list().into_iter().map(|r| r.name).collect();
    names.sort();
    assert_eq!(names, ["a", "b"]);
}

#[test]
fn remote_set_concurrent_access() {
    let rs = Arc::new(RemoteSet::new());
    let hs: Vec<_> = (0..50u8)
        .map(|n| {
            let rs = rs.clone();
            std::thread::spawn(move || {
                let name = format!("r{n}");
                rs.add(remote(&name)).unwrap();
                rs.get(&name);
                rs.list();
                rs.len();
            })
        })
        .collect();
    for h in hs {
        h.join().unwrap();
    }
    assert_eq!(rs.len(), 50);
}

#[test]
fn remote_filter_is_applied() {
    let r = remote("filtered").filter(|d: &Diff| d.entity_type == "allowed");
    let f = r.filter.as_ref().expect("filter");
    assert!(f(&Diff {
        entity_type: "allowed".into(),
        ..Diff::default()
    }));
    assert!(!f(&Diff {
        entity_type: "blocked".into(),
        ..Diff::default()
    }));
}

#[test]
fn memory_transport_roundtrip() {
    let mt = MemoryTransport::new();
    let ctx = Context::background();
    let diffs = vec![
        Diff {
            entity_id: "e1".into(),
            operation: Operation::Create,
            timestamp: at(100, 1),
            ..Diff::default()
        },
        Diff {
            entity_id: "e2".into(),
            operation: Operation::Update,
            timestamp: at(200, 0),
            ..Diff::default()
        },
    ];
    mt.push(&ctx, &diffs).unwrap();

    assert_eq!(mt.pull(&ctx, &at(50, 0)).unwrap().len(), 2);
    let got = mt.pull(&ctx, &at(100, 1)).unwrap();
    assert_eq!(got.len(), 1);
    assert_eq!(got[0].entity_id, "e2");
    assert_eq!(mt.diffs(), diffs);
}

#[test]
fn memory_transport_ping_follows_alive() {
    let mt = MemoryTransport::new();
    let ctx = Context::background();
    mt.ping(&ctx).unwrap();
    mt.set_alive(false);
    assert_eq!(
        mt.ping(&ctx).unwrap_err().to_string(),
        "transport: not reachable"
    );
    mt.set_alive(true);
    mt.ping(&ctx).unwrap();
}
