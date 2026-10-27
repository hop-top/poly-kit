//! TypeID primitive — thin wrapper around the [`type_safe_id`] crate.
//!
//! Implements the cross-language kit API SHAPE defined in
//! `docs/announcements/2026-05-typeid-primitive.md` for Rust:
//!
//! - [`new`] — generate a `prefix_<26-char-base32>` string from a prefix.
//! - [`parse`] — round-trip an existing canonical string back to its parts.
//! - [`Typed<T>`] — compile-time-prefix-safe newtype, parameterised by a
//!   [`Prefix`] marker type carrying `const PREFIX: &'static str`.
//! - serde [`Serialize`] / [`Deserialize`] for [`Typed<T>`] — the wire form
//!   is the **bare typeid string**, never a struct/object.
//! - [`IdError`] — variants for invalid prefix, invalid suffix, and
//!   prefix-mismatch (typed parse).
//!
//! URI composition (`<scheme>://<entity-type>/<typeid>`) is **not** part of
//! this module. Adopters compose the canonical string with `hop-top-cite`
//! directly. This keeps the typeid primitive free of any transitive URI
//! dependency.
//!
//! # Spec
//!
//! Wraps the Jetify TypeID specification **v0.3.0** (UUIDv7 by default,
//! 26-char Crockford base32 suffix). See the document above for the rationale.
//!
//! The wrapper validates the prefix itself before delegating: the error
//! messages stay independent of the backing crate, and the backing crate's
//! error path is never reached with input it cannot slice safely (an
//! over-long prefix whose 63rd byte sits inside a multi-byte character).
//! Base32 encoding and decoding stay upstream.
//!
//! # Bus-factor warning
//!
//! The upstream [`type-safe-id`](https://github.com/conradludgate/type-safe-id)
//! crate is small, has a handful of contributors and releases rarely. Kit
//! accepts this risk with the following mitigations:
//!
//! 1. The dependency surface in this module is small and isolated, and no
//!    upstream type appears in this module's public API; if the crate goes
//!    unmaintained we can vendor it or swap to a replacement without
//!    touching adopters.
//! 2. Upstream activity is reviewed at every kit release. If there is no
//!    commit for 12 months and an open critical bug, the kit maintainers
//!    will publish a fork under the kit org (dual Apache-2.0 / MIT
//!    licensing is preserved by upstream).
//!
//! Carry this warning forward when reading or extending the module.
//!
//! # Example
//!
//! ```
//! use hop_top_kit::id::{new, parse};
//!
//! let id = new("task").unwrap();
//! assert!(id.starts_with("task_"));
//!
//! let parsed = parse(&id).unwrap();
//! assert_eq!(parsed.prefix, "task");
//! assert_eq!(parsed.uuid.get_version_num(), 7);
//! ```

use std::fmt;
use std::marker::PhantomData;
use std::str::FromStr;

use serde::{Deserialize, Deserializer, Serialize, Serializer};
use thiserror::Error;
use type_safe_id::{DynamicType, TypeSafeId};
use uuid::Uuid;

/// Errors produced by the kit typeid primitive.
///
/// Variants are intentionally cross-language: the same three failure modes
/// surface in the Go, Python, TypeScript and PHP bindings, so adopters that
/// dispatch on error kind can write parallel logic across SDKs.
#[derive(Debug, Clone, PartialEq, Eq, Error)]
pub enum IdError {
    /// The prefix did not match the Jetify TypeID grammar
    /// (lowercase ASCII letters and underscores, starting and ending with a
    /// letter, max 63 bytes).
    #[error("invalid typeid prefix: {0}")]
    InvalidPrefix(String),

    /// The suffix is not a valid 26-character Crockford base32 encoding of a
    /// UUID, or otherwise failed validation.
    #[error("invalid typeid suffix: {0}")]
    InvalidSuffix(String),

    /// A [`Typed<T>`] parse received a canonical typeid whose prefix did not
    /// match `T::PREFIX`.
    #[error("typeid prefix mismatch: expected `{expected}`, got `{got}`")]
    PrefixMismatch {
        /// The prefix expected by the typed wrapper (`T::PREFIX`).
        expected: String,
        /// The prefix carried in the parsed string.
        got: String,
    },
}

const MAX_PREFIX_LEN: usize = 63;
const SUFFIX_LEN: usize = 26;
const BASE32_ALPHABET: &[u8] = b"0123456789abcdefghjkmnpqrstvwxyz";

/// Validate a non-empty prefix against the TypeID grammar, in byte terms,
/// before any of it reaches the backing crate.
fn check_prefix(prefix: &str) -> Result<(), IdError> {
    let bytes = prefix.as_bytes();
    let reason = if bytes.len() > MAX_PREFIX_LEN {
        Some("Input exceeds 63 characters")
    } else {
        match (bytes.first(), bytes.last()) {
            (Some(b'_'), _) => Some("Input cannot start with an underscore"),
            (Some(b), _) if !b.is_ascii_lowercase() => {
                Some("Input must start with a lowercase alphabetic character")
            }
            (None, _) => Some("Input must start with a lowercase alphabetic character"),
            (_, Some(b'_')) => Some("Input cannot end with an underscore"),
            (_, Some(b)) if !b.is_ascii_lowercase() => {
                Some("Input must end with a lowercase alphabetic character")
            }
            _ if bytes.iter().any(|&b| !(b.is_ascii_lowercase() || b == b'_')) => Some(
                "Input contains invalid characters: only lowercase ASCII letters and underscores are allowed",
            ),
            _ => None,
        }
    };
    reason.map_or(Ok(()), |r| Err(IdError::InvalidPrefix(r.to_owned())))
}

/// Explain why the backing crate rejected `suffix`.
fn suffix_error(suffix: &str) -> IdError {
    let reason = if suffix.len() != SUFFIX_LEN {
        "Suffix must be exactly 26 characters long"
    } else if suffix.bytes().any(|b| !BASE32_ALPHABET.contains(&b)) {
        "Suffix contains characters not in the base32 alphabet"
    } else {
        "First character of suffix must be '7' or less"
    };
    IdError::InvalidSuffix(format!("Invalid `TypeID` suffix: {reason}"))
}

/// The backing crate's prefix type for a prefix [`check_prefix`] accepted
/// (or the empty prefix).
fn dynamic_type(prefix: &str) -> Result<DynamicType, IdError> {
    if !prefix.is_empty() {
        check_prefix(prefix)?;
    }
    DynamicType::new(prefix).map_err(|e| IdError::InvalidPrefix(e.to_string()))
}

fn require_prefix(prefix: &str) -> Result<(), IdError> {
    if prefix.is_empty() {
        return Err(IdError::InvalidPrefix(
            "prefix must not be empty".to_owned(),
        ));
    }
    Ok(())
}

/// Result of parsing a canonical typeid string.
///
/// `uuid` is the raw 128-bit identifier decoded from the suffix. For typeids
/// generated by [`new`], this is a UUIDv7 (time-ordered).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Parsed {
    /// The prefix component (`""` for prefix-less typeids).
    pub prefix: String,
    /// The decoded UUID payload.
    pub uuid: Uuid,
}

/// Generate a new canonical typeid string for `prefix`, with a freshly-minted
/// UUIDv7 suffix.
///
/// Returns the bare typeid string (e.g. `task_01j…`). The suffix is always a
/// 26-char Crockford base32 encoding; the canonical form is
/// `<prefix>_<suffix>`.
///
/// # Errors
///
/// Returns [`IdError::InvalidPrefix`] if `prefix` does not match the Jetify
/// TypeID grammar (lowercase ASCII letters and underscores, starting and
/// ending with a letter, max 63 bytes). The empty prefix is **rejected**
/// here so callers always get a self-describing ID.
///
/// # Example
///
/// ```
/// use hop_top_kit::id::new;
///
/// let id = new("invoice").unwrap();
/// assert!(id.starts_with("invoice_"));
/// assert_eq!(id.len(), "invoice_".len() + 26);
/// ```
pub fn new(prefix: &str) -> Result<String, IdError> {
    require_prefix(prefix)?;
    Ok(TypeSafeId::new_with_type(dynamic_type(prefix)?).to_string())
}

/// Parse a canonical typeid string into its prefix and UUID components.
///
/// # Errors
///
/// - [`IdError::InvalidPrefix`] if the prefix fails the Jetify TypeID
///   grammar.
/// - [`IdError::InvalidSuffix`] if the suffix is not a valid 26-char
///   base32-encoded UUID.
///
/// # Example
///
/// ```
/// use hop_top_kit::id::{new, parse};
///
/// let id = new("user").unwrap();
/// let parsed = parse(&id).unwrap();
/// assert_eq!(parsed.prefix, "user");
/// ```
pub fn parse(s: &str) -> Result<Parsed, IdError> {
    let (prefix, suffix) = match s.rsplit_once('_') {
        Some((prefix, suffix)) => {
            check_prefix(prefix)?;
            (prefix, suffix)
        }
        None => ("", s),
    };
    let id = TypeSafeId::<DynamicType>::from_str(s).map_err(|e| match e {
        type_safe_id::Error::InvalidType | type_safe_id::Error::IncorrectType { .. } => {
            IdError::InvalidPrefix(e.to_string())
        }
        _ => suffix_error(suffix),
    })?;
    debug_assert_eq!(id.type_prefix(), prefix);
    Ok(Parsed {
        prefix: id.type_prefix().to_owned(),
        uuid: id.uuid(),
    })
}

/// Marker trait pinning a typeid prefix at the type level.
///
/// Implement this on a zero-sized marker struct per entity:
///
/// ```
/// use hop_top_kit::id::{Prefix, Typed};
///
/// #[derive(Debug, Clone, Copy, PartialEq, Eq)]
/// pub struct Task;
/// impl Prefix for Task {
///     const PREFIX: &'static str = "task";
/// }
///
/// pub type TaskId = Typed<Task>;
///
/// let id = TaskId::new().unwrap();
/// assert!(id.as_str().starts_with("task_"));
/// ```
pub trait Prefix {
    /// The canonical prefix this typed ID wears at runtime.
    const PREFIX: &'static str;
}

/// Compile-time-prefix-safe typeid newtype.
///
/// `Typed<T>` wraps the canonical typeid string and ties the prefix to the
/// type system through `T: Prefix`. Functions that take `Typed<Task>` will
/// only accept task IDs; the compiler enforces this regardless of the
/// underlying string.
///
/// # Serde wire form
///
/// `Typed<T>` serialises as the **bare typeid string** (e.g. `"task_01j…"`),
/// never as an object. Deserialisation validates both that the input parses
/// as a typeid **and** that its prefix equals `T::PREFIX`; otherwise an
/// [`IdError::PrefixMismatch`] is surfaced via serde.
///
/// # Example
///
/// ```
/// use hop_top_kit::id::{Prefix, Typed};
///
/// pub struct Invoice;
/// impl Prefix for Invoice {
///     const PREFIX: &'static str = "invoice";
/// }
///
/// let id: Typed<Invoice> = Typed::new().unwrap();
/// let round = Typed::<Invoice>::parse(id.as_str()).unwrap();
/// assert_eq!(id, round);
/// ```
pub struct Typed<T: Prefix>(String, PhantomData<T>);

impl<T: Prefix> Typed<T> {
    /// Generate a new `Typed<T>` with `T::PREFIX` and a fresh UUIDv7.
    ///
    /// # Errors
    ///
    /// Returns [`IdError::InvalidPrefix`] if `T::PREFIX` does not match the
    /// Jetify TypeID grammar. This is a "fix the marker type" error, not
    /// something callers can recover from at runtime.
    pub fn new() -> Result<Self, IdError> {
        let s = new(T::PREFIX)?;
        Ok(Self(s, PhantomData))
    }

    /// Parse a canonical typeid string, requiring its prefix to equal
    /// `T::PREFIX`.
    ///
    /// # Errors
    ///
    /// - [`IdError::InvalidPrefix`] / [`IdError::InvalidSuffix`] if the
    ///   string is not a syntactically valid typeid.
    /// - [`IdError::PrefixMismatch`] if the prefix does not equal
    ///   `T::PREFIX`.
    pub fn parse(s: &str) -> Result<Self, IdError> {
        let parsed = parse(s)?;
        if parsed.prefix != T::PREFIX {
            return Err(IdError::PrefixMismatch {
                expected: T::PREFIX.to_owned(),
                got: parsed.prefix,
            });
        }
        Ok(Self(s.to_owned(), PhantomData))
    }

    /// Borrow the canonical typeid string.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }

    /// Consume `self` and return the canonical typeid string.
    #[must_use]
    pub fn into_inner(self) -> String {
        self.0
    }
}

impl<T: Prefix> fmt::Debug for Typed<T> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_tuple("Typed").field(&self.0).finish()
    }
}

impl<T: Prefix> fmt::Display for Typed<T> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl<T: Prefix> Clone for Typed<T> {
    fn clone(&self) -> Self {
        Self(self.0.clone(), PhantomData)
    }
}

impl<T: Prefix> PartialEq for Typed<T> {
    fn eq(&self, other: &Self) -> bool {
        self.0 == other.0
    }
}

impl<T: Prefix> Eq for Typed<T> {}

impl<T: Prefix> std::hash::Hash for Typed<T> {
    fn hash<H: std::hash::Hasher>(&self, state: &mut H) {
        self.0.hash(state);
    }
}

impl<T: Prefix> AsRef<str> for Typed<T> {
    fn as_ref(&self) -> &str {
        &self.0
    }
}

impl<T: Prefix> FromStr for Typed<T> {
    type Err = IdError;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Self::parse(s)
    }
}

impl<T: Prefix> Serialize for Typed<T> {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(&self.0)
    }
}

impl<'de, T: Prefix> Deserialize<'de> for Typed<T> {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        Self::parse(&s).map_err(serde::de::Error::custom)
    }
}

/// Build a canonical typeid string deterministically from an explicit
/// `prefix` and `uuid`. Useful for fixtures and cross-language parity
/// tests where both sides must agree on the encoded form for a pinned
/// input.
///
/// # Errors
///
/// Returns [`IdError::InvalidPrefix`] if `prefix` is empty or violates the
/// Jetify TypeID grammar. The empty-prefix rejection matches [`new`]: the
/// kit's Rust wrapper requires a self-describing prefix on every id.
///
/// # Example
///
/// ```
/// use hop_top_kit::id::{from_uuid, parse};
/// use uuid::Uuid;
///
/// let uuid = Uuid::parse_str("01940000-0000-7000-8000-000000000000").unwrap();
/// let s = from_uuid("task", uuid).unwrap();
/// assert_eq!(s, "task_01jg000000e008000000000000");
/// let parsed = parse(&s).unwrap();
/// assert_eq!(parsed.uuid, uuid);
/// ```
pub fn from_uuid(prefix: &str, uuid: Uuid) -> Result<String, IdError> {
    require_prefix(prefix)?;
    __test_only_from_uuid(prefix, uuid)
}

/// Internal helper retained for parity fixture verification.
/// Mirrors [`from_uuid`] but bypasses the kit's empty-prefix rejection so
/// the parity test suite can probe both paths from the same construction
/// site without needing two crates' worth of test plumbing.
#[doc(hidden)]
pub(crate) fn __test_only_from_uuid(prefix: &str, uuid: Uuid) -> Result<String, IdError> {
    Ok(TypeSafeId::from_type_and_uuid(dynamic_type(prefix)?, uuid).to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    // ------------------------------------------------------------------
    // Canonical fixtures — these UUID inputs are the cross-language parity
    // anchors. Do NOT change them; other SDKs (py/ts/php/go)
    // must consume the same triples and produce matching typeid strings.
    // ------------------------------------------------------------------
    const FIXTURE_TASK_UUID: &str = "01940000-0000-7000-8000-000000000000";
    const FIXTURE_INVOICE_UUID: &str = "01940000-0000-7000-8000-000000000001";
    const FIXTURE_USER_UUID: &str = "01940000-0000-7000-8000-0000000000ff";

    fn fixture_uuid(s: &str) -> Uuid {
        Uuid::parse_str(s).expect("fixture uuid must parse")
    }

    #[test]
    fn new_generates_prefixed_typeid() {
        let id = new("task").expect("valid prefix");
        assert!(id.starts_with("task_"), "got: {id}");
        // 26-char Crockford base32 suffix.
        assert_eq!(id.len(), "task_".len() + 26);
    }

    #[test]
    fn new_rejects_empty_prefix() {
        let err = new("").unwrap_err();
        assert!(matches!(err, IdError::InvalidPrefix(_)));
    }

    #[test]
    fn new_rejects_uppercase_prefix() {
        let err = new("Task").unwrap_err();
        assert!(matches!(err, IdError::InvalidPrefix(_)));
    }

    #[test]
    fn new_rejects_prefix_with_punctuation() {
        let err = new("task!").unwrap_err();
        assert!(matches!(err, IdError::InvalidPrefix(_)));
    }

    #[test]
    fn parse_round_trips_generated_id() {
        let id = new("invoice").unwrap();
        let parsed = parse(&id).unwrap();
        assert_eq!(parsed.prefix, "invoice");
        assert_eq!(parsed.uuid.get_version_num(), 7);
        // Recompose: prefix_<26-base32> should equal `id`.
        assert_eq!(from_uuid("invoice", parsed.uuid).unwrap(), id);
    }

    #[test]
    fn parse_rejects_invalid_suffix() {
        let err = parse("task_not-a-real-typeid").unwrap_err();
        assert!(matches!(err, IdError::InvalidSuffix(_)));
    }

    #[test]
    fn parse_rejects_invalid_prefix() {
        // Uppercase / punctuation prefixes are rejected by upstream.
        let err = parse("Bad_01h455vb4pex5vsknk084sn02q").unwrap_err();
        assert!(matches!(err, IdError::InvalidPrefix(_)), "got {err:?}");
    }

    #[test]
    fn fixtures_round_trip_through_pinned_uuid() {
        for (prefix, uuid_str) in [
            ("task", FIXTURE_TASK_UUID),
            ("invoice", FIXTURE_INVOICE_UUID),
            ("user", FIXTURE_USER_UUID),
        ] {
            let uuid = fixture_uuid(uuid_str);
            let canonical = __test_only_from_uuid(prefix, uuid).unwrap();
            assert!(
                canonical.starts_with(&format!("{prefix}_")),
                "fixture canonical form mismatch: got {canonical}"
            );
            let parsed = parse(&canonical).unwrap();
            assert_eq!(parsed.prefix, prefix);
            assert_eq!(parsed.uuid, uuid);
        }
    }

    // ------------------------------------------------------------------
    // Pinned canonical wire-form fixtures. The expected strings below are
    // the literal output of `mti` v1.1.x (the previous backing crate) for
    // the three pinned UUID inputs; they were captured once by calling `__test_only_from_uuid` and pasted
    // here verbatim. If an upstream change ever shifts the base32 encoding
    // (or anything else in the canonical form) this assertion fails loudly
    // and the cross-language parity matrix must be reviewed.
    // ------------------------------------------------------------------
    const FIXTURE_TASK_CANONICAL: &str = "task_01jg000000e008000000000000";
    const FIXTURE_INVOICE_CANONICAL: &str = "invoice_01jg000000e008000000000001";
    const FIXTURE_USER_CANONICAL: &str = "user_01jg000000e00800000000007z";

    #[test]
    fn fixtures_emit_stable_canonical_strings() {
        let cases = [
            ("task", FIXTURE_TASK_UUID, FIXTURE_TASK_CANONICAL),
            ("invoice", FIXTURE_INVOICE_UUID, FIXTURE_INVOICE_CANONICAL),
            ("user", FIXTURE_USER_UUID, FIXTURE_USER_CANONICAL),
        ];
        for (prefix, uuid_str, expected) in cases {
            let uuid = fixture_uuid(uuid_str);
            let canonical = __test_only_from_uuid(prefix, uuid).unwrap();
            assert_eq!(
                canonical, expected,
                "canonical typeid for ({prefix}, {uuid_str}) drifted from pinned wire form"
            );
            // Round-trip must yield the same UUID bytes.
            let parsed = parse(&canonical).unwrap();
            assert_eq!(parsed.uuid, uuid);
            assert_eq!(parsed.prefix, prefix);
        }
    }

    // ------------------------------------------------------------------
    // Typed<T> + serde
    // ------------------------------------------------------------------
    struct Task;
    impl Prefix for Task {
        const PREFIX: &'static str = "task";
    }

    struct Invoice;
    impl Prefix for Invoice {
        const PREFIX: &'static str = "invoice";
    }

    #[test]
    fn typed_new_wears_marker_prefix() {
        let id: Typed<Task> = Typed::new().unwrap();
        assert!(id.as_str().starts_with("task_"));
    }

    #[test]
    fn typed_parse_accepts_matching_prefix() {
        let id: Typed<Task> = Typed::new().unwrap();
        let s = id.as_str().to_owned();
        let round = Typed::<Task>::parse(&s).unwrap();
        assert_eq!(round, id);
    }

    #[test]
    fn typed_parse_rejects_prefix_mismatch() {
        let id: Typed<Task> = Typed::new().unwrap();
        let err = Typed::<Invoice>::parse(id.as_str()).unwrap_err();
        match err {
            IdError::PrefixMismatch { expected, got } => {
                assert_eq!(expected, "invoice");
                assert_eq!(got, "task");
            }
            other => panic!("expected PrefixMismatch, got {other:?}"),
        }
    }

    #[test]
    fn typed_from_str_delegates_to_parse() {
        let id: Typed<Task> = Typed::new().unwrap();
        let s = id.as_str().to_owned();
        let parsed: Typed<Task> = s.parse().unwrap();
        assert_eq!(parsed, id);
    }

    #[test]
    fn typed_serializes_as_bare_string() {
        let id: Typed<Task> = Typed::new().unwrap();
        let json = serde_json::to_string(&id).unwrap();
        // Bare string form: `"task_..."`. NOT a struct.
        assert!(json.starts_with('"') && json.ends_with('"'));
        assert!(json.contains("task_"));
        assert!(!json.contains('{'));
    }

    #[test]
    fn typed_deserializes_from_bare_string() {
        let id: Typed<Task> = Typed::new().unwrap();
        let json = serde_json::to_string(&id).unwrap();
        let round: Typed<Task> = serde_json::from_str(&json).unwrap();
        assert_eq!(round, id);
    }

    // ------------------------------------------------------------------
    // Grammar edge cases the contract fixtures do not cover. Accept and
    // reject sets, error kinds and message text match the wrapper's
    // behaviour before the backing crate changed.
    // ------------------------------------------------------------------
    const SUFFIX: &str = "01jg000000e008000000000000";

    fn prefix_err(input: &str) -> String {
        match parse(&format!("{input}_{SUFFIX}")) {
            Err(IdError::InvalidPrefix(msg)) => msg,
            other => panic!("{input:?}: expected InvalidPrefix, got {other:?}"),
        }
    }

    fn suffix_err(suffix: &str) -> String {
        match parse(&format!("task_{suffix}")) {
            Err(IdError::InvalidSuffix(msg)) => msg,
            other => panic!("{suffix:?}: expected InvalidSuffix, got {other:?}"),
        }
    }

    #[test]
    fn prefix_grammar_accepts_spec_forms() {
        let max = "a".repeat(63);
        for prefix in ["task", "my_app", "a__b", "x", max.as_str()] {
            let s = from_uuid(prefix, fixture_uuid(FIXTURE_TASK_UUID)).unwrap();
            assert_eq!(s, format!("{prefix}_{SUFFIX}"));
            assert_eq!(parse(&s).unwrap().prefix, prefix);
            assert!(new(prefix).is_ok(), "new({prefix:?})");
        }
    }

    #[test]
    fn parse_accepts_bare_suffix_as_empty_prefix() {
        let parsed = parse(SUFFIX).unwrap();
        assert_eq!(parsed.prefix, "");
        assert_eq!(parsed.uuid, fixture_uuid(FIXTURE_TASK_UUID));
    }

    #[test]
    fn prefix_grammar_rejections_and_messages() {
        let start = "Input must start with a lowercase alphabetic character";
        let end = "Input must end with a lowercase alphabetic character";
        let invalid =
            "Input contains invalid characters: only lowercase ASCII letters and underscores are allowed";
        let cases = [
            ("Task", start),
            ("", start),
            ("_task", "Input cannot start with an underscore"),
            ("1task", start),
            ("task2", end),
            ("task!", end),
            ("task_", "Input cannot end with an underscore"),
            ("t2a", invalid),
            ("tAsk", invalid),
            ("t\u{e2}sk", invalid),
        ];
        for (input, want) in cases {
            assert_eq!(prefix_err(input), want, "parse prefix {input:?}");
        }
        assert_eq!(
            new("Task").unwrap_err().to_string(),
            format!("invalid typeid prefix: {start}")
        );
        assert_eq!(
            new("task_").unwrap_err(),
            IdError::InvalidPrefix("Input cannot end with an underscore".to_owned())
        );
    }

    #[test]
    fn prefix_over_63_bytes_is_rejected() {
        let too_long = "Input exceeds 63 characters";
        assert_eq!(prefix_err(&"a".repeat(64)), too_long);
        assert!(matches!(
            new(&"a".repeat(64)),
            Err(IdError::InvalidPrefix(_))
        ));
    }

    #[test]
    fn prefix_with_multibyte_char_at_byte_63_is_rejected_not_panicking() {
        // 62 ASCII bytes then a 2-byte char: byte 63 falls inside it. The
        // backing crate slices `prefix[..63]` on its error path, which
        // panics on a non-char boundary unless the wrapper rejects first.
        let prefix = format!("{}\u{e9}", "a".repeat(62));
        assert_eq!(prefix.len(), 64);
        assert!(!prefix.is_char_boundary(63));
        assert_eq!(prefix_err(&prefix), "Input exceeds 63 characters");
        assert!(matches!(new(&prefix), Err(IdError::InvalidPrefix(_))));
        assert!(matches!(
            from_uuid(&prefix, fixture_uuid(FIXTURE_TASK_UUID)),
            Err(IdError::InvalidPrefix(_))
        ));
    }

    #[test]
    fn suffix_rejections_and_messages() {
        let length = "Invalid `TypeID` suffix: Suffix must be exactly 26 characters long";
        let alphabet =
            "Invalid `TypeID` suffix: Suffix contains characters not in the base32 alphabet";
        let first = "Invalid `TypeID` suffix: First character of suffix must be '7' or less";
        let cases = [
            ("", length),
            ("not-a-real-typeid", length),
            ("01jg000000e0080000000000000", length),
            ("01JG000000E008000000000000", alphabet),
            ("01jg000000e00800000000000u", alphabet),
            ("01jg000000e00800000000000l", alphabet),
            ("81jg000000e008000000000000", first),
            ("zzzzzzzzzzzzzzzzzzzzzzzzzz", first),
        ];
        for (suffix, want) in cases {
            assert_eq!(suffix_err(suffix), want, "suffix {suffix:?}");
        }
        assert_eq!(
            parse("task_not-a-real-typeid").unwrap_err().to_string(),
            format!("invalid typeid suffix: {length}")
        );
        // The largest valid suffix decodes to the all-ones UUID.
        assert_eq!(
            parse("task_7zzzzzzzzzzzzzzzzzzzzzzzzz").unwrap().uuid,
            Uuid::from_u128(u128::MAX)
        );
    }

    #[test]
    fn typed_deserialize_rejects_wrong_prefix() {
        let id: Typed<Task> = Typed::new().unwrap();
        let json = serde_json::to_string(&id).unwrap();
        let err = serde_json::from_str::<Typed<Invoice>>(&json).unwrap_err();
        assert!(err.to_string().contains("prefix mismatch"), "got: {err}");
    }
}
