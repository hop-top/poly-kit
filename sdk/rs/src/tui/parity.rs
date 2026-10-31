//! Cross-language constants from `contracts/parity/parity.json`.
//!
//! Copied, not loaded: the file sits outside the published crate. The
//! `parity_contract` test fails when a copy drifts from the contract.

/// Braille dot spinner frames.
pub const SPINNER_FRAMES: [&str; 10] = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];

/// Delay between spinner frames, in milliseconds.
pub const SPINNER_INTERVAL_MS: u64 = 80;

pub(crate) const SYMBOL_INFO: &str = "ℹ";
pub(crate) const SYMBOL_SUCCESS: &str = "✓";
pub(crate) const SYMBOL_ERROR: &str = "●";
pub(crate) const SYMBOL_WARN: &str = "▲";

#[cfg(test)]
mod tests {
    use super::*;

    fn contract() -> serde_json::Value {
        let path = concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../contracts/parity/parity.json"
        );
        serde_json::from_str(&std::fs::read_to_string(path).expect("parity.json")).unwrap()
    }

    #[test]
    fn spinner_matches_contract() {
        let c = contract();
        let frames: Vec<&str> = c["spinner"]["frames"]
            .as_array()
            .unwrap()
            .iter()
            .map(|v| v.as_str().unwrap())
            .collect();
        assert_eq!(frames, SPINNER_FRAMES);
        assert_eq!(c["spinner"]["interval_ms"], SPINNER_INTERVAL_MS);
    }

    #[test]
    fn status_symbols_match_contract() {
        let s = &contract()["status"]["symbols"];
        assert_eq!(s["info"], SYMBOL_INFO);
        assert_eq!(s["success"], SYMBOL_SUCCESS);
        assert_eq!(s["error"], SYMBOL_ERROR);
        assert_eq!(s["warn"], SYMBOL_WARN);
    }
}
