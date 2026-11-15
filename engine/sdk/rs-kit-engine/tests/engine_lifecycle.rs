//! Parity harness: spawns a real `kit serve`. Run with
//! `KIT_BIN=/path/to/kit cargo test --features parity`.
#![cfg(feature = "parity")]

use hop_top_kit_engine::{KitEngine, KitEngineOptions};

fn alive(pid: u32) -> bool {
    std::process::Command::new("kill")
        .args(["-0", &pid.to_string()])
        .status()
        .map(|s| s.success())
        .unwrap_or(false)
}

#[tokio::test]
async fn spawn_health_stop_without_leaks() {
    let bin = std::env::var("KIT_BIN").expect("KIT_BIN must name a kit binary");
    let data = std::env::temp_dir().join(format!("kit-engine-rs-lifecycle-{}", std::process::id()));
    std::fs::create_dir_all(&data).unwrap();
    let e = KitEngine::start(KitEngineOptions {
        bin_path: Some(bin.into()),
        app: Some("rs-lifecycle".into()),
        data: Some(data.display().to_string()),
        no_peer: true,
        no_sync: true,
        ..Default::default()
    })
    .await
    .unwrap();
    assert!(e.port > 0);
    assert!(alive(e.pid));
    let pid = e.pid;
    let probe = KitEngine::connect(e.port, e.token.clone(), e.shutdown_token.clone())
        .await
        .unwrap();
    assert_eq!(probe.pid, pid);
    e.stop().await.unwrap();
    assert!(!alive(pid), "engine {pid} leaked after stop");
    let _ = std::fs::remove_dir_all(&data);
}
