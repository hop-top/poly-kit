// Cross-language exit-taxonomy contract loader.
//
// Loads `contracts/exit-taxonomy-v1/taxonomy.json` — generated from
// `go/console/output/envelope`, the single source of truth — and asserts
// this Rust SDK's table agrees with it on every class, every exit number
// and every transience.
//
// A failure here means Go's taxonomy moved and this port did not follow.
// That is the exact drift this contract exists to catch: when Go gained
// CONSENT_REFUSED 7 and PREREQUISITE 70, all four SDK ports silently
// kept the old nine-class table for two releases, and
// `transience_for_code` returned "unknown" where Go returned
// "transient" — so four runtimes handed agents wrong retry guidance.
//
// Fix by adding the missing class to the port, not by editing the
// contract: the contract is regenerated from Go, never hand-edited.
//
// The whole file is gated on the `output` feature because `tests/` is
// compiled unconditionally and `hop_top_kit::output` only exists behind
// that flag.
#![cfg(feature = "output")]

use std::fs;
use std::path::{Path, PathBuf};

use hop_top_kit::output::{exit_classes, exit_code_for_class, transience_for_code};
use serde::Deserialize;

/// The slots envelope itself owns. The conformance-tree slots (66-69)
/// are recorded in the contract so a new allocation cannot double-book a
/// number, but no SDK port is expected to export them.
const ENVELOPE_OWNER: &str = "hop.top/kit/go/console/output/envelope";

#[derive(Debug, Deserialize)]
struct ContractFile {
    version: String,
    source: String,
    transiences: Vec<String>,
    classes: Vec<TaxonomyClass>,
    extension_band: Vec<BandSlot>,
}

#[derive(Debug, Deserialize)]
struct TaxonomyClass {
    class: String,
    exit: i32,
    transience: String,
}

#[derive(Debug, Deserialize)]
struct BandSlot {
    exit: i32,
    class: String,
    owner: String,
}

fn locate_contract() -> PathBuf {
    // Walk up from CARGO_MANIFEST_DIR (sdk/experimental/rs) until we hit
    // the kit repo root that contains the taxonomy contract. Going
    // through CARGO_MANIFEST_DIR is the only stable anchor under both
    // `cargo test` (CWD = manifest dir) and editor invocations.
    let manifest = env!("CARGO_MANIFEST_DIR");
    let mut dir: &Path = Path::new(manifest);
    for _ in 0..10 {
        let candidate = dir
            .join("contracts")
            .join("exit-taxonomy-v1")
            .join("taxonomy.json");
        if candidate.exists() {
            return candidate;
        }
        match dir.parent() {
            Some(parent) => dir = parent,
            None => break,
        }
    }
    panic!("contracts/exit-taxonomy-v1/taxonomy.json: not found walking up from {manifest}");
}

fn load() -> ContractFile {
    let path = locate_contract();
    let raw = fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
    serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {}: {e}", path.display()))
}

#[test]
fn contract_metadata() {
    let c = load();
    assert_eq!(c.version, "v1");
    assert_eq!(c.source, ENVELOPE_OWNER);
    assert!(!c.classes.is_empty());
}

/// Membership in both directions. A port carrying an extra class Go does
/// not define is as much a divergence as a port missing one: an adopter
/// branching on it gets a number no other runtime produces.
#[test]
fn declares_exactly_the_contract_classes() {
    let c = load();
    let want: Vec<&str> = c.classes.iter().map(|r| r.class.as_str()).collect();
    assert_eq!(exit_classes(), want);
}

#[test]
fn every_class_resolves_to_contract_exit_and_transience() {
    let c = load();
    for row in &c.classes {
        assert_eq!(
            exit_code_for_class(&row.class),
            Some(row.exit),
            "class {} must resolve to exit {}",
            row.class,
            row.exit
        );
        assert_eq!(
            transience_for_code(&row.class),
            row.transience,
            "class {} must be {}",
            row.class,
            row.transience
        );
    }
}

/// The transience vocabulary is itself a contract: an agent branching on
/// a fourth string has no defined behavior.
#[test]
fn answers_only_with_the_contract_transience_vocabulary() {
    let c = load();
    for row in &c.classes {
        let got = transience_for_code(&row.class);
        assert!(
            c.transiences.iter().any(|t| t == got),
            "class {} answered {got}, outside the contract vocabulary {:?}",
            row.class,
            c.transiences
        );
    }
}

/// An unknown class must resolve to nothing rather than to a fallback
/// number. A built-in fallback is what turns "kit added a class and this
/// port never copied it" into an assertion against exit 1 that reads as
/// a real failure rather than a stale table.
#[test]
fn does_not_invent_a_code_for_an_adopter_class() {
    assert_eq!(exit_code_for_class("ADOPTER_SPECIFIC"), None);
    assert_eq!(transience_for_code("ADOPTER_SPECIFIC"), "unknown");
}

#[test]
fn exports_every_band_slot_envelope_owns() {
    let c = load();
    let owned: Vec<&BandSlot> = c
        .extension_band
        .iter()
        .filter(|s| s.owner == ENVELOPE_OWNER)
        .collect();
    assert!(!owned.is_empty());
    for slot in owned {
        assert_eq!(
            exit_code_for_class(&slot.class),
            Some(slot.exit),
            "band slot {} must resolve to exit {}",
            slot.class,
            slot.exit
        );
    }
}

/// A port exporting LEAK_DETECTED would mint a number the conformance
/// tree owns, which is the collision the band record exists to stop.
#[test]
fn leaves_foreign_band_slots_unclaimed() {
    let c = load();
    let foreign: Vec<&BandSlot> = c
        .extension_band
        .iter()
        .filter(|s| s.owner != ENVELOPE_OWNER)
        .collect();
    assert!(!foreign.is_empty());
    for slot in foreign {
        assert_eq!(
            exit_code_for_class(&slot.class),
            None,
            "band slot {} is owned by {}, not this port",
            slot.class,
            slot.owner
        );
    }
}
