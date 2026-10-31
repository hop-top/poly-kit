//! Ports go/runtime/sync integration_test.go: two replicas converge
//! through memory transports. Go's version runs over a
//! `VersionedRepository`, which is not ported; plain repositories stand
//! in, and the same create-then-update round trip is asserted.
#![cfg(feature = "sync")]

#[path = "sync_support/mod.rs"]
mod support;

use std::sync::Arc;
use std::time::Duration;

use hop_top_kit::sync::{
    compute_diff, Diff, MemoryTransport, Operation, Remote, Replicator, SyncMode, Timestamp,
    Transport,
};

use support::{bg, Item, MemRepo};

fn puller(repo: &Arc<MemRepo>, name: &str, t: &Arc<MemoryTransport>) -> Replicator<Item> {
    Replicator::builder(repo.clone())
        .interval(Duration::from_secs(3600))
        .remote(Remote::new(name, t.clone()).mode(SyncMode::PullOnly))
        .build()
}

#[test]
fn two_replicas_converge_through_memory_transports() {
    let (repo_a, repo_b) = (MemRepo::new(), MemRepo::new());
    let a_to_b = Arc::new(MemoryTransport::new());
    let b_to_a = Arc::new(MemoryTransport::new());

    // Step 1: create on A, replicate to B.
    let e = Item::new("e1", "original");
    repo_a.seed(e.clone());
    let d = compute_diff(
        "e1",
        "item",
        None::<&Item>,
        Some(&e),
        Timestamp {
            physical: 100,
            logical: 0,
            node_id: "nodeA".into(),
        },
    )
    .unwrap();
    assert_eq!(d.operation, Operation::Create);
    a_to_b.push(&bg(), &[d]).unwrap();

    let rep_b = puller(&repo_b, "fromA", &a_to_b);
    rep_b.sync_once(&bg(), "fromA").unwrap();
    assert_eq!(repo_b.find("e1").unwrap().name, "original");

    // Step 2: update on B, replicate back to A.
    let updated = Item::new("e1", "modified-by-B");
    repo_b.seed(updated.clone());
    b_to_a
        .push(
            &bg(),
            &[Diff {
                operation: Operation::Update,
                after: Some(serde_json::to_vec(&updated).unwrap()),
                timestamp: Timestamp {
                    physical: 200,
                    logical: 0,
                    node_id: "nodeB".into(),
                },
                ..Diff::default()
            }],
        )
        .unwrap();
    let rep_a = puller(&repo_a, "fromB", &b_to_a);
    rep_a.sync_once(&bg(), "fromB").unwrap();

    // Step 3: both ends hold the same state.
    assert_eq!(repo_a.find("e1").unwrap(), repo_b.find("e1").unwrap());
    assert_eq!(repo_a.find("e1").unwrap().name, "modified-by-B");
}

#[test]
fn a_delete_diff_removes_the_entity_on_the_peer() {
    let repo = MemRepo::new();
    repo.seed(Item::new("gone", "x"));
    let t = Arc::new(MemoryTransport::new());
    t.push(
        &bg(),
        &[Diff {
            entity_id: "gone".into(),
            operation: Operation::Delete,
            timestamp: Timestamp {
                physical: 5,
                logical: 0,
                node_id: "n".into(),
            },
            ..Diff::default()
        }],
    )
    .unwrap();
    puller(&repo, "r", &t).sync_once(&bg(), "r").unwrap();
    assert!(repo.find("gone").is_none());
}
