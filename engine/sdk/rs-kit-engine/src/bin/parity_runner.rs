//! Parity runner: drives one kit serve (KIT_ENGINE_PORT / KIT_ENGINE_TOKEN)
//! through the shared create/update/history/revert scenario and prints the
//! normalized JSON the cross-SDK harness compares.

use hop_top_kit_engine::KitEngine;
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

    let mut keys: Vec<String> = match serde_json::to_value(&created)? {
        Value::Object(m) => m.keys().cloned().collect(),
        _ => vec![],
    };
    keys.sort();
    let title = |v: &Value| v["title"].clone();
    println!(
        "{}",
        json!({
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
