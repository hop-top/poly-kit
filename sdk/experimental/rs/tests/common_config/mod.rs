//! Shared fixtures for the reload tests (ported from reloadable_test.go).
#![allow(dead_code)]

use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use hop_top_kit::config::{Field, ReloadFields, ReloadPublisher};
use serde::{Deserialize, Serialize};

#[derive(Debug, Default, Clone, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SubCfg {
    pub threshold: i64,
    pub bind_host: String,
}

/// `endpoint` and `sub.threshold` reload; the rest is immutable.
#[derive(Debug, Default, Clone, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ReloadCfg {
    pub listen_addr: String,
    pub endpoint: String,
    pub sub: SubCfg,
}

impl ReloadFields for SubCfg {
    fn reload_fields() -> Vec<Field> {
        vec![
            Field::leaf("threshold").reloadable(),
            Field::leaf("bind_host"),
        ]
    }
}

impl ReloadFields for ReloadCfg {
    fn reload_fields() -> Vec<Field> {
        vec![
            Field::leaf("listen_addr"),
            Field::leaf("endpoint").reloadable(),
            Field::of::<SubCfg>("sub"),
        ]
    }
}

pub fn initial() -> ReloadCfg {
    ReloadCfg {
        listen_addr: ":8080".into(),
        endpoint: "https://api.example.com".into(),
        sub: SubCfg {
            threshold: 5,
            bind_host: "127.0.0.1".into(),
        },
    }
}

pub fn write_cfg(dir: &Path, c: &ReloadCfg) -> PathBuf {
    let p = dir.join("config.yaml");
    std::fs::write(
        &p,
        format!(
            "listen_addr: {}\nendpoint: {}\nsub:\n  threshold: {}\n  bind_host: {}\n",
            c.listen_addr, c.endpoint, c.sub.threshold, c.sub.bind_host
        ),
    )
    .unwrap();
    p
}

#[derive(Clone, Debug)]
pub struct Captured {
    pub topic: String,
    pub source: String,
    pub payload: serde_json::Value,
}

#[derive(Default)]
pub struct Recorder(pub Mutex<Vec<Captured>>);

impl ReloadPublisher for Recorder {
    fn publish(&self, topic: &str, source: &str, payload: serde_json::Value) -> Result<(), String> {
        self.0.lock().unwrap().push(Captured {
            topic: topic.into(),
            source: source.into(),
            payload,
        });
        Ok(())
    }
}

/// Waits for `n` events on `topic` (publishing is fire-and-forget).
pub fn wait_for(rec: &Arc<Recorder>, topic: &str, n: usize) -> Vec<Captured> {
    let deadline = Instant::now() + Duration::from_secs(2);
    loop {
        let got: Vec<Captured> = rec
            .0
            .lock()
            .unwrap()
            .iter()
            .filter(|e| e.topic == topic)
            .cloned()
            .collect();
        if got.len() >= n {
            return got;
        }
        assert!(
            Instant::now() < deadline,
            "timed out waiting for {n} on {topic}"
        );
        std::thread::sleep(Duration::from_millis(5));
    }
}
