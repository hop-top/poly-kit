//! Parity runner: drives one kit serve (KIT_ENGINE_PORT / KIT_ENGINE_TOKEN)
//! through the shared create/update/history/revert + branch-ops scenario and prints the
//! normalized JSON the cross-SDK harness compares.

use hop_top_kit_engine::{EngineError, KitEngine, PrunePolicy};
use serde_json::{json, Value};

fn main() {
    let rt = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("tokio runtime");
    if let Err(e) = rt.block_on(run()) {
        eprintln!("{e}");
        std::process::exit(1);
    }
}

/// HTTP status of a failed call; 0 on success.
fn status<T>(r: Result<T, EngineError>) -> u16 {
    match r {
        Ok(_) => 0,
        Err(EngineError::Status { status, .. }) => status,
        Err(EngineError::VersionConflict(_)) => 409,
        Err(e) => panic!("unexpected error: {e}"),
    }
}

async fn run() -> Result<(), Box<dyn std::error::Error>> {
    let port: u16 = std::env::var("KIT_ENGINE_PORT")?.parse()?;
    let token = std::env::var("KIT_ENGINE_TOKEN").ok();
    let engine = KitEngine::connect(port, token, None).await?;
    let notes = engine.collection::<Value>("notes");

    let created = notes.create(json!({"title": "v1"})).await?;
    let id = created.id.clone();
    notes.update(&id, json!({"title": "v2"})).await?;
    notes.update(&id, json!({"title": "v3"})).await?;
    let before = notes.history(&id).await?;
    let reverted = notes.revert(&id, 1).await?;
    let after = notes.history(&id).await?;

    // Branch ops on their own doc; errors normalize to the HTTP status.
    let seqs = |hs: Vec<hop_top_kit_engine::BranchHead>| {
        let mut s: Vec<u64> = hs.iter().map(|h| h.seq).collect();
        s.sort();
        s
    };
    let b = notes.create(json!({"title": "b1"})).await?.id;
    notes.update(&b, json!({"title": "b2"})).await?;
    notes.update(&b, json!({"title": "b3"})).await?;
    let fork_a = notes.fork(&b, 2).await?;
    let heads_after_fork = seqs(notes.branches(&b, false).await?);
    notes.abandon(&b, fork_a.seq).await?;
    let live_after_abandon = seqs(notes.branches(&b, true).await?);
    let abandon_last_live = status(notes.abandon(&b, 3).await);
    let fork_b = notes.fork(&b, 2).await?;
    let merged = notes
        .merge(&b, fork_b.seq, 3, json!({"title": "merged"}))
        .await?;
    let prune_no_policy = status(notes.prune(&b, PrunePolicy::default()).await);
    let pruned = notes
        .prune(
            &b,
            PrunePolicy {
                max_versions: Some(1),
                ..Default::default()
            },
        )
        .await?;
    let missing = "no-such-doc";
    let one = || PrunePolicy {
        max_versions: Some(1),
        ..Default::default()
    };
    let x = || json!({"title": "x"});
    let fork_keys = {
        let mut k: Vec<String> = match serde_json::to_value(&fork_a)? {
            Value::Object(m) => m.keys().cloned().collect(),
            _ => vec![],
        };
        k.sort();
        k
    };
    let fork_out_of_range = status(notes.fork(&b, 99).await);
    let merge_out_of_range = status(notes.merge(&b, 99, 1, x()).await);
    let fork_missing = status(notes.fork(missing, 1).await);
    let merge_missing = status(notes.merge(missing, 2, 1, x()).await);
    let prune_missing = status(notes.prune(missing, one()).await);
    let abandon_missing = status(notes.abandon(missing, 1).await);

    let mut keys: Vec<String> = match serde_json::to_value(&created)? {
        Value::Object(m) => m.keys().cloned().collect(),
        _ => vec![],
    };
    keys.sort();
    let title = |v: &Value| v["title"].clone();
    println!(
        "{}",
        json!({
            "forkSeq": fork_a.seq,
            "forkParents": fork_a.parent_ids.len(),
            "forkKeys": fork_keys,
            "headsAfterFork": heads_after_fork,
            "liveAfterAbandon": live_after_abandon,
            "abandonLastLive": abandon_last_live,
            "forkBSeq": fork_b.seq,
            "mergeSeq": merged.seq,
            "mergeParents": merged.parent_ids.len(),
            "pruneNoPolicy": prune_no_policy,
            "pruneRemoved": pruned.versions_removed.len(),
            "pruneBlobsFreed": pruned.blobs_freed,
            "pruneBytesFreed": pruned.bytes_freed,
            "forkOutOfRange": fork_out_of_range,
            "mergeOutOfRange": merge_out_of_range,
            "forkMissing": fork_missing,
            "mergeMissing": merge_missing,
            "pruneMissing": prune_missing,
            "abandonMissing": abandon_missing,
            "createdKeys": keys,
            "beforeVersions": before.iter().map(|v| v.version).collect::<Vec<_>>(),
            "beforeOps": before.iter().map(|v| &v.operation).collect::<Vec<_>>(),
            "beforeTitles": before.iter().map(|v| title(&v.data)).collect::<Vec<_>>(),
            "revertedTitle": title(&reverted.data),
            "afterVersions": after.iter().map(|v| v.version).collect::<Vec<_>>(),
            "afterOps": after.iter().map(|v| &v.operation).collect::<Vec<_>>(),
            "afterTitles": after.iter().map(|v| title(&v.data)).collect::<Vec<_>>(),
        })
    );
    Ok(())
}
