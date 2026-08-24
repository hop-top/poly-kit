//! [`Qualifiers`] — the four payload-side semantic axes the topic does
//! not encode.
//!
//! Mirrors `go/runtime/bus/qualifiers.go`. See ADR 0017 in the Go tree
//! for why these axes stay out of the topic string (cardinality,
//! subscriber pattern stability, metric series cap).

use serde::{Deserialize, Serialize};
use serde_json::Value;

/// Carries the four semantic axes the bus topic does not encode:
///
/// - **Reason** — why the event happened (cause)
/// - **Mechanism** — how it happened (transport / pathway)
/// - **Property** — which attribute changed or applied
/// - **Circumstance** — during what context / conditions
///
/// All four fields are optional; an empty `Qualifiers` serialises to
/// `{}`.
///
/// Go opts a payload in by embedding `bus.Qualifiers` and extracts it
/// reflectively via `QualifiersFrom`. Rust has no field embedding, so the
/// equivalent is a flattened field plus [`qualifiers_from`], which reads
/// the qualifier keys back out of a serialised payload:
///
/// ```
/// # use hop_top_kit::bus::Qualifiers;
/// # use serde::Serialize;
/// #[derive(Serialize)]
/// struct SnapshotReloadFailed {
///     #[serde(flatten)]
///     qualifiers: Qualifiers,
///     snapshot_id: String,
/// }
/// ```
///
/// A nested field (`qualifiers: Qualifiers` without `flatten`) is also
/// recognised by [`qualifiers_from`], matching the Go helper's support
/// for both anonymous and named embeds.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Qualifiers {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub reason: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub mechanism: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub property: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub circumstance: String,
}

impl Qualifiers {
    /// Reports whether no qualifier field is set.
    pub fn is_zero(&self) -> bool {
        self.reason.is_empty()
            && self.mechanism.is_empty()
            && self.property.is_empty()
            && self.circumstance.is_empty()
    }
}

/// Extracts a [`Qualifiers`] from a payload value.
///
/// Looks for the qualifier keys at the top level of the payload object
/// (the `#[serde(flatten)]` shape), then for a nested `qualifiers`
/// object. Returns `None` when the payload is not a JSON object or
/// carries no qualifier keys at all.
///
/// Does NOT recurse beyond the two shapes above: adopters that nest
/// payload types should place qualifiers at the top level of the
/// published struct. This keeps the helper cheap and predictable.
pub fn qualifiers_from(payload: &Value) -> Option<Qualifiers> {
    let obj = payload.as_object()?;

    let flat = extract(obj);
    if let Some(q) = flat {
        return Some(q);
    }

    // Nested `qualifiers` object (Go's named-embed equivalent). An
    // explicitly present but empty object still counts as an opt-in.
    let nested = obj.get("qualifiers")?.as_object()?;
    Some(extract(nested).unwrap_or_default())
}

/// Reads the four qualifier keys out of a JSON object, returning `None`
/// when none of them are present.
fn extract(obj: &serde_json::Map<String, Value>) -> Option<Qualifiers> {
    let mut q = Qualifiers::default();
    let mut found = false;
    for (key, slot) in [
        ("reason", &mut q.reason),
        ("mechanism", &mut q.mechanism),
        ("property", &mut q.property),
        ("circumstance", &mut q.circumstance),
    ] {
        if let Some(Value::String(s)) = obj.get(key) {
            *slot = s.clone();
            found = true;
        }
    }
    if found {
        Some(q)
    } else {
        None
    }
}
