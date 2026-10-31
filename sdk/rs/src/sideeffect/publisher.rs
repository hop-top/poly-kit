//! Mechanism tagging and the dry-run bus wrapper.

use std::sync::Arc;

use serde_json::Value;

use super::context::is_dry_run;
use super::seams::{Bus, PublishError};
use crate::bus::Context;

/// The canonical `mechanism` the dry-run publisher tags onto payloads.
/// Subscribers that want to filter dry-run events check this value.
pub const DRY_RUN_MECHANISM: &str = "dry_run";

const QUALIFIER_KEYS: [&str; 4] = ["reason", "mechanism", "property", "circumstance"];

/// Sets `mechanism` on a payload that carries
/// [`Qualifiers`](crate::bus::Qualifiers), and reports whether it does.
///
/// Rust has no field embedding, so a payload opts in as
/// [`qualifiers_from`](crate::bus::qualifiers_from) reads it: a JSON
/// object with qualifier keys at the top level (a flattened
/// `Qualifiers`), else a nested `qualifiers` object. A non-empty
/// existing `mechanism` is kept: the wrapper is not authoritative.
///
/// An all-empty flattened `Qualifiers` serialises to no keys, so it is
/// indistinguishable from a payload without one; use the nested form (it
/// serialises as `{}`), or set another qualifier, to opt in.
pub fn tag_mechanism(payload: &mut Value, mechanism: &str) -> bool {
    let Some(obj) = payload.as_object_mut() else {
        return false;
    };
    let target = if QUALIFIER_KEYS
        .iter()
        .any(|k| matches!(obj.get(*k), Some(Value::String(_))))
    {
        obj
    } else {
        match obj.get_mut("qualifiers").and_then(Value::as_object_mut) {
            Some(nested) => nested,
            None => return false,
        }
    };
    let unset = target
        .get("mechanism")
        .and_then(Value::as_str)
        .is_none_or(str::is_empty);
    if unset {
        target.insert("mechanism".into(), Value::String(mechanism.into()));
    }
    true
}

/// Wraps a [`Bus`] and, when the publish-time context is dry-run, tags
/// `mechanism: "dry_run"` onto payloads that carry qualifiers (see
/// [`tag_mechanism`]) before delegating. Otherwise it is a passthrough.
///
/// Unlike Go's reflective wrapper, which can only tag a payload passed by
/// pointer, the payload here is a value the wrapper owns, so tagging is
/// guaranteed.
#[derive(Default)]
pub struct DryRunPublisher {
    inner: Option<Arc<dyn Bus>>,
}

impl DryRunPublisher {
    /// Wraps `inner`. `None` is accepted; every publish then returns
    /// [`PublishError::NilInner`].
    pub fn new(inner: Option<Arc<dyn Bus>>) -> Self {
        DryRunPublisher { inner }
    }
}

impl Bus for DryRunPublisher {
    fn publish(
        &self,
        ctx: &Context,
        topic: &str,
        source: &str,
        mut payload: Value,
    ) -> Result<(), PublishError> {
        let inner = self.inner.as_ref().ok_or(PublishError::NilInner)?;
        if is_dry_run(ctx) {
            tag_mechanism(&mut payload, DRY_RUN_MECHANISM);
        }
        inner.publish(ctx, topic, source, payload)
    }
}
