// Cross-language parity contract test (tlc T-0753).
//
// Loads `contracts/typeid-v1/fixtures.json` from the repo root and
// asserts that the Rust SDK's `id::from_uuid` and `id::parse` agree with
// the canonical wire form shared by go/ts/py/php. A divergence here
// means either the upstream `mti` crate drifted or the contract was
// edited without updating all five SDKs.
//
// The whole file is gated on the `id` feature because `tests/` is
// compiled unconditionally and the `hop_top_kit::id` module only exists
// behind that feature flag.
#![cfg(feature = "id")]

use std::fs;
use std::path::{Path, PathBuf};

use hop_top_kit::id::{from_uuid, parse};
use serde::Deserialize;
use uuid::Uuid;

#[derive(Debug, Deserialize)]
struct ContractFile {
    version: String,
    spec: String,
    vectors: Vec<Vector>,
}

#[derive(Debug, Deserialize)]
struct Vector {
    name: String,
    prefix: String,
    uuid: String,
    typeid: String,
    #[serde(default)]
    skip_in: Vec<String>,
    #[serde(default)]
    #[allow(dead_code)]
    note: String,
}

fn locate_contract() -> PathBuf {
    // Walk up from CARGO_MANIFEST_DIR (sdk/experimental/rs) until we hit
    // the kit repo root that contains contracts/typeid-v1/fixtures.json.
    // Going through CARGO_MANIFEST_DIR is the only stable anchor under
    // both `cargo test` (CWD = manifest dir) and editor invocations.
    let manifest = env!("CARGO_MANIFEST_DIR");
    let mut dir: &Path = Path::new(manifest);
    for _ in 0..10 {
        let candidate = dir
            .join("contracts")
            .join("typeid-v1")
            .join("fixtures.json");
        if candidate.exists() {
            return candidate;
        }
        match dir.parent() {
            Some(parent) => dir = parent,
            None => break,
        }
    }
    panic!("contracts/typeid-v1/fixtures.json: not found walking up from {manifest}",);
}

fn load() -> ContractFile {
    let path = locate_contract();
    let raw = fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
    serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {}: {e}", path.display()))
}

#[test]
fn contract_metadata() {
    let cf = load();
    assert_eq!(cf.version, "v1", "contract version drift");
    assert_eq!(cf.spec, "jetify-typeid-v0.3", "contract spec drift");
    assert!(!cf.vectors.is_empty(), "contract has no vectors");
}

#[test]
fn contract_generation() {
    let cf = load();
    let mut ran = 0usize;
    let mut skipped = 0usize;
    for v in &cf.vectors {
        if v.skip_in.iter().any(|s| s == "rs") {
            skipped += 1;
            eprintln!("skip rs/{}: capability flag set", v.name);
            continue;
        }
        let uuid =
            Uuid::parse_str(&v.uuid).unwrap_or_else(|e| panic!("vector {} bad uuid: {e}", v.name));
        let got = from_uuid(&v.prefix, uuid)
            .unwrap_or_else(|e| panic!("from_uuid({:?}, {}): {e}", v.prefix, v.uuid));
        assert_eq!(
            got, v.typeid,
            "canonical typeid drift on vector {} (prefix={:?} uuid={})",
            v.name, v.prefix, v.uuid,
        );
        ran += 1;
    }
    assert!(ran > 0, "no vectors exercised (all skipped?)");
    eprintln!("contract_generation: {ran} ran, {skipped} skipped");
}

#[test]
fn contract_parse() {
    let cf = load();
    let mut ran = 0usize;
    for v in &cf.vectors {
        if v.skip_in.iter().any(|s| s == "rs") {
            eprintln!("skip rs/{}: capability flag set", v.name);
            continue;
        }
        let parsed = parse(&v.typeid).unwrap_or_else(|e| panic!("parse({}): {e}", v.typeid));
        assert_eq!(
            parsed.prefix, v.prefix,
            "prefix mismatch on vector {}",
            v.name
        );
        let want =
            Uuid::parse_str(&v.uuid).unwrap_or_else(|e| panic!("vector {} bad uuid: {e}", v.name));
        assert_eq!(parsed.uuid, want, "uuid mismatch on vector {}", v.name);
        ran += 1;
    }
    assert!(ran > 0, "no vectors exercised");
}
