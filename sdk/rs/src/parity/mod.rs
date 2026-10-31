//! Cross-language TUI parity constants, loaded from `parity.json`.
//!
//! The canonical file is `contracts/parity/parity.json`; a copy ships in
//! this directory because a crate cannot `include_str!` outside its own
//! tree. `tests/parity.rs` keeps the two byte-identical and fails when a
//! block is declared in the contract without a field here. A port of
//! `contracts/parity/parity.go` (`parity.Values`, `parity.Blocks`).

use std::collections::BTreeMap;
use std::sync::OnceLock;

use serde::Deserialize;

/// The embedded `parity.json`, byte-identical to the contract.
pub const RAW: &str = include_str!("parity.json");

/// Top-level `parity.json` keys this loader knows. Every content block
/// MUST appear here and have a field on [`Parity`]; keys starting with
/// `$` are JSON Schema metadata, not content.
pub const BLOCKS: &[&str] = &[
    "description",
    "status",
    "spinner",
    "anim",
    "help",
    "verbosity",
    "streams",
    "extends",
];

/// `status`: prefix symbols for status lines.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Status {
    pub symbols: BTreeMap<String, String>,
}

/// `spinner`: braille frames and timing.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Spinner {
    pub frames: Vec<String>,
    pub interval_ms: u64,
}

/// `anim`: gradient scramble animation constants.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Anim {
    pub runes: String,
    pub interval_ms: u64,
    pub default_width: u64,
}

/// One help section's display metadata.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct SectionConfig {
    pub title: String,
}

/// `help`: root `--help` layout.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Help {
    pub section_order: Vec<String>,
    pub sections: BTreeMap<String, SectionConfig>,
}

/// `verbosity`: stackable flag to log level.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Verbosity {
    pub flag: String,
    /// Stacked count (decimal string) to log level name.
    pub levels: BTreeMap<String, String>,
    pub quiet_override: String,
}

/// `streams`: named output streams.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Streams {
    pub flag: String,
    pub label_format: String,
    pub output: String,
}

/// The parsed content of `parity.json`.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Parity {
    pub description: String,
    pub status: Status,
    pub spinner: Spinner,
    pub anim: Anim,
    pub help: Help,
    pub verbosity: Verbosity,
    pub streams: Streams,
    pub extends: Vec<String>,
}

/// The parsed contract, loaded once. The data is embedded, so a parse
/// failure is a build defect and panics, as Go's `init` does.
pub fn values() -> &'static Parity {
    static VALUES: OnceLock<Parity> = OnceLock::new();
    VALUES.get_or_init(|| serde_json::from_str(RAW).expect("parity: parity.json must parse"))
}
