//! ColumnSpec — column metadata used by formatters and `--cols` validation.
//!
//! Mirrors py/ts/php `ColumnSpec`. Go is data-driven and has no equivalent.

/// One column of a row payload.
///
/// `header` and `key` are required to be equal. Validation against `--cols`
/// and value lookup on the row are the same operation on the same name, so
/// a header/key split would be a capability no other runtime can mirror —
/// Go derives both from a single `table:""` struct tag and cannot express
/// one without the other. `key` is retained rather than removed to keep the
/// struct shape aligned with the py/ts/php ports, and is checked at
/// construction so the two can never drift.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ColumnSpec {
    /// User-visible label, matched against `--cols`, and the lookup name
    /// on the row. Always equal to [`ColumnSpec::key`].
    pub header: String,
    /// Lookup key on the row. Always equal to [`ColumnSpec::header`].
    pub key: String,
    /// Hide-on-overflow priority. Higher wins.
    ///
    /// Accepted and stored, but not acted on: hide-on-overflow is
    /// implemented in the Go runtime only. Payload-based SDKs carry the
    /// field so construction sites stay portable until it is ported.
    pub priority: i32,
}

impl ColumnSpec {
    /// Named-arg-friendly factory mirroring the py/ts/php construction sites.
    ///
    /// # Panics
    ///
    /// Panics when `header != key`. The two name the same thing; a mismatch
    /// is a construction-site bug, not a runtime condition to recover from.
    pub fn new(header: impl Into<String>, key: impl Into<String>, priority: i32) -> Self {
        let header = header.into();
        let key = key.into();
        assert!(
            header == key,
            "ColumnSpec header/key mismatch: header={header:?} key={key:?} \
             (header and key name the same column; they must be equal)"
        );
        Self {
            header,
            key,
            priority,
        }
    }
}
