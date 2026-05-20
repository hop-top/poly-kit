//! ColumnSpec — column metadata used by formatters and `--cols` validation.
//!
//! Mirrors py/ts/php `ColumnSpec`. Go is data-driven and has no equivalent.

/// One column of a row payload.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ColumnSpec {
    /// User-visible label; also matched against `--cols`.
    pub header: String,
    /// Lookup key on the row.
    pub key: String,
    /// Hide-on-overflow priority. Higher wins.
    pub priority: i32,
}

impl ColumnSpec {
    /// Named-arg-friendly factory mirroring the py/ts/php construction sites.
    pub fn new(
        header: impl Into<String>,
        key: impl Into<String>,
        priority: i32,
    ) -> Self {
        Self {
            header: header.into(),
            key: key.into(),
            priority,
        }
    }
}
