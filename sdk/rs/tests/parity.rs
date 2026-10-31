//! Drift guards for the parity contract loader.
//!
//! Mirrors `TestParityNoUnloadedBlocks` / `TestParityLoadedBlocksNonZero`
//! in `contracts/parity/parity_test.go` and `sdk/py/tests/test_parity.py`.
//! `contracts/parity/parity.json` is a loaded contract, not
//! documentation: a block declared there but not modelled by
//! `hop_top_kit::parity` is invisible to every consumer.

#![cfg(feature = "output")]

use std::collections::BTreeSet;
use std::fs;
use std::path::PathBuf;

use hop_top_kit::parity::{self, BLOCKS, RAW};

fn canonical() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/parity/parity.json")
}

fn declared() -> BTreeSet<String> {
    let v: serde_json::Value = serde_json::from_str(&fs::read_to_string(canonical()).unwrap())
        .expect("parity.json parses");
    v.as_object()
        .unwrap()
        .keys()
        .filter(|k| !k.starts_with('$'))
        .cloned()
        .collect()
}

#[test]
fn vendored_copy_matches_canonical_bytes() {
    // The crate cannot include_str! outside its directory, so it ships a
    // copy. Byte-for-byte: formatting drift is drift.
    assert!(
        fs::read_to_string(canonical()).unwrap() == RAW,
        "src/parity/parity.json drifted from contracts/parity/parity.json; \
         cp contracts/parity/parity.json sdk/experimental/rs/src/parity/parity.json"
    );
}

#[test]
fn models_every_declared_block() {
    let known: BTreeSet<String> = BLOCKS.iter().map(|s| s.to_string()).collect();
    let unknown: Vec<_> = declared().difference(&known).cloned().collect();
    assert!(
        unknown.is_empty(),
        "parity.json declares block(s) {unknown:?} the Rust loader does not know; add a field \
         to parity::Parity and the name to parity::BLOCKS"
    );
}

#[test]
fn registry_has_no_stale_entries() {
    let stale: Vec<_> = BLOCKS
        .iter()
        .filter(|b| !declared().contains(**b))
        .collect();
    assert!(
        stale.is_empty(),
        "BLOCKS lists {stale:?}, no longer declared"
    );
}

#[test]
fn every_block_loads_a_value() {
    let p = parity::values();
    for kind in ["info", "success", "error", "warn"] {
        assert!(
            p.status.symbols.get(kind).is_some_and(|s| !s.is_empty()),
            "status.symbols[{kind}]"
        );
    }
    assert!(!p.spinner.frames.is_empty() && p.spinner.interval_ms > 0);
    assert!(!p.anim.runes.is_empty() && p.anim.interval_ms > 0 && p.anim.default_width > 0);
    assert!(!p.help.section_order.is_empty() && !p.help.sections.is_empty());
    for s in &p.help.section_order {
        assert!(p.help.sections.contains_key(s), "help.sections[{s}]");
    }
    assert!(
        !p.verbosity.flag.is_empty()
            && !p.verbosity.levels.is_empty()
            && !p.verbosity.quiet_override.is_empty()
    );
    assert!(
        !p.streams.flag.is_empty()
            && !p.streams.label_format.is_empty()
            && !p.streams.output.is_empty()
    );
    assert!(!p.description.is_empty() && !p.extends.is_empty());
}

#[test]
fn pinned_values() {
    let p = parity::values();
    assert_eq!(p.status.symbols["info"], "\u{2139}");
    assert_eq!(p.status.symbols["success"], "\u{2713}");
    assert_eq!(p.status.symbols["error"], "\u{25cf}");
    assert_eq!(p.status.symbols["warn"], "\u{25b2}");
    assert_eq!(p.verbosity.flag, "-V");
    assert_eq!(p.verbosity.levels["0"], "info");
    assert_eq!(p.verbosity.levels["1"], "debug");
    assert_eq!(p.verbosity.levels["2"], "trace");
    assert_eq!(p.verbosity.quiet_override, "warn");
    assert_eq!(p.streams.flag, "--stream");
    assert_eq!(p.streams.label_format, "[{name}]");
    assert_eq!(p.streams.output, "stderr");
}
