//! Replays `contracts/kv-v1/ttl.json`, the put-if-absent-with-TTL
//! sequences the Go memory, sqlite and badger backends replay too.
#![cfg(feature = "kv")]

use std::fs;
use std::path::Path;
use std::thread::sleep;
use std::time::Duration;

use hop_top_kit::kv::{SqliteStore, Store, TtlInserter};
use serde_json::Value;

fn unhex(s: &str) -> Vec<u8> {
    assert!(s.len().is_multiple_of(2), "odd-length hex: {s:?}");
    (0..s.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&s[i..i + 2], 16).expect("hex digit"))
        .collect()
}

fn corpus() -> Value {
    let mut dir: &Path = Path::new(env!("CARGO_MANIFEST_DIR"));
    for _ in 0..10 {
        let p = dir.join("contracts/kv-v1/ttl.json");
        if p.exists() {
            let v: Value = serde_json::from_str(&fs::read_to_string(p).unwrap()).unwrap();
            assert_eq!(v["version"], "v1");
            return v;
        }
        dir = dir.parent().expect("contracts/kv-v1/ttl.json not found");
    }
    panic!("contracts/kv-v1/ttl.json not found");
}

fn replay(mut open: impl FnMut() -> SqliteStore) {
    let c = corpus();
    let scenarios = c["scenarios"].as_array().unwrap();
    assert!(!scenarios.is_empty());
    for sc in scenarios {
        let name = sc["name"].as_str().unwrap();
        let store = open();
        for (i, st) in sc["steps"].as_array().unwrap().iter().enumerate() {
            let key = st["key_hex"].as_str().map(unhex).unwrap_or_default();
            let val = st["value_hex"].as_str().map(unhex);
            match st["op"].as_str().unwrap() {
                "put_if_absent" => {
                    let ttl = Duration::from_millis(st["ttl_ms"].as_u64().unwrap());
                    let got = store
                        .put_if_absent_with_ttl(&key, &val.unwrap(), ttl)
                        .unwrap();
                    assert_eq!(got, st["inserted"].as_bool().unwrap(), "{name} step {i}");
                }
                "put" => store.put(&key, &val.unwrap()).unwrap(),
                "sleep_ms" => sleep(Duration::from_millis(st["ms"].as_u64().unwrap())),
                "get" => assert_eq!(store.get(&key).unwrap(), val, "{name} step {i}"),
                op => panic!("{name} step {i}: unknown op {op}"),
            }
        }
    }
}

#[test]
fn ttl_corpus_file_store() {
    let dir = tempfile::tempdir().unwrap();
    let mut n = 0;
    replay(|| {
        n += 1;
        SqliteStore::new(dir.path().join(format!("{n}.db")).to_str().unwrap()).unwrap()
    });
}
