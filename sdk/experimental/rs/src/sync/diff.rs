//! The change record: one entity-level change as JSON snapshots. Go's
//! diff.go.
//!
//! # Wire shape
//!
//! Keys are `entity_id`, `entity_type`, `operation` (an integer),
//! `before`, `after`, `timestamp` and `node_id`. `before` and `after`
//! are Go `[]byte`, which `encoding/json` writes as standard base64 of
//! the snapshot's JSON, and omits when empty; they decode from base64,
//! `null` or absence alike. Snapshot bytes are `serde_json`'s, which
//! differ from Go's only in the `<`, `>`, `&`, U+2028 and U+2029
//! escapes Go adds.

use std::fmt;

use base64::engine::general_purpose::STANDARD;
use base64::Engine as _;
use serde::de::Error as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};

use super::clock::Timestamp;

/// The kind of entity change. Go's `Operation`; the integers are the
/// wire contract.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum Operation {
    /// Go's `OpCreate`, 0.
    #[default]
    Create = 0,
    /// Go's `OpUpdate`, 1.
    Update = 1,
    /// Go's `OpDelete`, 2.
    Delete = 2,
}

impl Serialize for Operation {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.serialize_u8(*self as u8)
    }
}

impl<'de> Deserialize<'de> for Operation {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        match u8::deserialize(d)? {
            0 => Ok(Operation::Create),
            1 => Ok(Operation::Update),
            2 => Ok(Operation::Delete),
            n => Err(D::Error::custom(format!("unknown sync operation {n}"))),
        }
    }
}

/// One entity-level change as JSON snapshots. Go's `Diff`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Diff {
    /// The changed entity's id.
    pub entity_id: String,
    /// What kind of entity it is; free-form, matched by remote filters.
    pub entity_type: String,
    /// Create, update or delete.
    pub operation: Operation,
    /// The JSON of the entity before the change, if any.
    #[serde(with = "snapshot", skip_serializing_if = "snapshot::is_empty")]
    pub before: Option<Vec<u8>>,
    /// The JSON of the entity after the change; `None` for a delete.
    #[serde(with = "snapshot", skip_serializing_if = "snapshot::is_empty")]
    pub after: Option<Vec<u8>>,
    /// When the change happened.
    pub timestamp: Timestamp,
    /// The node that made it.
    pub node_id: String,
}

mod snapshot {
    use super::*;

    pub fn is_empty(v: &Option<Vec<u8>>) -> bool {
        v.as_ref().is_none_or(Vec::is_empty)
    }

    pub fn serialize<S: Serializer>(v: &Option<Vec<u8>>, s: S) -> Result<S::Ok, S::Error> {
        match v {
            Some(b) => s.serialize_str(&STANDARD.encode(b)),
            None => s.serialize_none(),
        }
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<Option<Vec<u8>>, D::Error> {
        Option::<String>::deserialize(d)?
            .map(|s| STANDARD.decode(s).map_err(D::Error::custom))
            .transpose()
    }
}

/// Why a [`Diff`] could not be built.
#[derive(Debug)]
pub enum DiffError {
    /// Neither a before nor an after was given.
    BothNil,
    /// A snapshot did not serialize.
    Encode(serde_json::Error),
}

impl fmt::Display for DiffError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            DiffError::BothNil => f.write_str("sync: both before and after are nil"),
            DiffError::Encode(e) => e.fmt(f),
        }
    }
}

impl std::error::Error for DiffError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            DiffError::BothNil => None,
            DiffError::Encode(e) => Some(e),
        }
    }
}

/// Builds a [`Diff`] by serializing `before` and `after` to JSON. No
/// `before` makes a create, no `after` a delete, both an update; none
/// at all is [`DiffError::BothNil`]. The diff's node is `ts.node_id`.
/// Go's `ComputeDiff`.
///
/// Say the absent side's type with a turbofish: `None::<&Note>`.
pub fn compute_diff<B: Serialize, A: Serialize>(
    entity_id: &str,
    entity_type: &str,
    before: Option<&B>,
    after: Option<&A>,
    ts: Timestamp,
) -> Result<Diff, DiffError> {
    let operation = match (before, after) {
        (None, None) => return Err(DiffError::BothNil),
        (None, Some(_)) => Operation::Create,
        (Some(_), None) => Operation::Delete,
        (Some(_), Some(_)) => Operation::Update,
    };
    let before = before
        .map(|v| serde_json::to_vec(v).map_err(DiffError::Encode))
        .transpose()?;
    let after = after
        .map(|v| serde_json::to_vec(v).map_err(DiffError::Encode))
        .transpose()?;
    Ok(Diff {
        entity_id: entity_id.to_string(),
        entity_type: entity_type.to_string(),
        operation,
        before,
        after,
        node_id: ts.node_id.clone(),
        timestamp: ts,
    })
}
