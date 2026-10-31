//! wasm32-unknown-unknown smoke test for the sync core, run under Node by
//! wasm-bindgen-test-runner:
//!
//! ```sh
//! CARGO_TARGET_WASM32_UNKNOWN_UNKNOWN_RUNNER=wasm-bindgen-test-runner \
//!   cargo test --target wasm32-unknown-unknown --no-default-features \
//!   --features sync --test sync_wasm
//! ```
//!
//! No system clock and no threads: the caller's wall clock drives the
//! HLC and the status lag, and the caller drives each round with
//! `sync_once`.

#![cfg(all(feature = "sync", target_arch = "wasm32", target_os = "unknown"))]

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, UNIX_EPOCH};

use hop_top_kit::bus::Context;
use hop_top_kit::domain::{Entity, Query, RepoError, Repository};
use hop_top_kit::sync::{
    Diff, MemoryTransport, MockWallClock, Operation, Remote, Replicator, SyncMode, Timestamp,
    Transport,
};
use serde::{Deserialize, Serialize};
use wasm_bindgen_test::wasm_bindgen_test;

#[derive(Clone, Serialize, Deserialize)]
struct Item {
    id: String,
    name: String,
}

impl Entity for Item {
    fn id(&self) -> &str {
        &self.id
    }
}

#[derive(Default)]
struct Repo(Mutex<BTreeMap<String, Item>>);

impl Repository<Item> for Repo {
    fn create(&self, _: &Context, e: &Item) -> Result<(), RepoError> {
        self.0.lock().unwrap().insert(e.id.clone(), e.clone());
        Ok(())
    }
    fn get(&self, _: &Context, id: &str) -> Result<Item, RepoError> {
        self.0
            .lock()
            .unwrap()
            .get(id)
            .cloned()
            .ok_or(RepoError::NotFound)
    }
    fn list(&self, _: &Context, _: &Query) -> Result<Vec<Item>, RepoError> {
        Ok(self.0.lock().unwrap().values().cloned().collect())
    }
    fn update(&self, _: &Context, e: &Item) -> Result<(), RepoError> {
        match self.0.lock().unwrap().get_mut(&e.id) {
            Some(s) => {
                *s = e.clone();
                Ok(())
            }
            None => Err(RepoError::NotFound),
        }
    }
    fn delete(&self, _: &Context, id: &str) -> Result<(), RepoError> {
        self.0.lock().unwrap().remove(id);
        Ok(())
    }
}

#[wasm_bindgen_test]
fn a_round_runs_on_the_callers_clock_without_threads() {
    let ctx = Context::background();
    let repo = Arc::new(Repo::default());
    let wall = Arc::new(MockWallClock::new(UNIX_EPOCH + Duration::from_secs(5)));
    let mt = Arc::new(MemoryTransport::new());
    mt.push(
        &ctx,
        &[Diff {
            entity_id: "e1".into(),
            operation: Operation::Create,
            after: Some(br#"{"id":"e1","name":"pulled"}"#.to_vec()),
            timestamp: Timestamp {
                physical: 1,
                logical: 0,
                node_id: "peer".into(),
            },
            ..Diff::default()
        }],
    )
    .unwrap();

    let rep = Replicator::builder(repo.clone())
        .wall_clock(wall.clone())
        .remote(Remote::new("origin", mt.clone()).mode(SyncMode::Bidirectional))
        .build();
    rep.start(&ctx).unwrap();
    rep.enqueue(Diff {
        entity_id: "out".into(),
        ..Diff::default()
    });
    rep.sync_once(&ctx, "origin").unwrap();

    assert_eq!(repo.get(&ctx, "e1").unwrap().name, "pulled");
    assert!(mt.diffs().iter().any(|d| d.entity_id == "out"));
    wall.advance(Duration::from_secs(3));
    let st = rep.status();
    assert!(st[0].connected);
    assert_eq!(st[0].lag, Duration::from_secs(3));
    rep.stop();
}
