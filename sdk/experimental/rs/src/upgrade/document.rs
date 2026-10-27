//! In-memory migration of one JSON document by an integer version field
//! inside it. Go `upgrade.MigrateDocument`; steps follow the YAML
//! `config::Migration` shape (`from`, `to`, `apply`,
//! `note`) plus an optional `verify`. No I/O: builds on wasm32.
//!
//! Key order is kept only when the build enables serde_json's
//! `preserve_order` (this crate's `output` feature does); otherwise keys
//! come back sorted, as in Go.

use std::borrow::Cow;
use std::fmt;
use std::sync::Arc;

use serde_json::{Map, Value};

use super::BoxError;

/// Transforms the document; gets its own copy and returns the result.
pub type DocApplyFn = Arc<dyn Fn(Value) -> Result<Value, BoxError> + Send + Sync>;
/// Checks a step's result before it is accepted.
pub type DocVerifyFn = Arc<dyn Fn(&Value) -> Result<(), BoxError> + Send + Sync>;

/// Moves a document from version `from` to `to` (`to > from`). Go `DocStep`.
#[derive(Clone)]
pub struct DocStep {
    pub from: i64,
    pub to: i64,
    pub apply: DocApplyFn,
    pub verify: Option<DocVerifyFn>,
    /// Short description for reports ("rename name to title").
    pub note: String,
}

impl DocStep {
    pub fn new(
        from: i64,
        to: i64,
        apply: impl Fn(Value) -> Result<Value, BoxError> + Send + Sync + 'static,
    ) -> DocStep {
        DocStep {
            from,
            to,
            apply: Arc::new(apply),
            verify: None,
            note: String::new(),
        }
    }

    pub fn with_verify(
        mut self,
        verify: impl Fn(&Value) -> Result<(), BoxError> + Send + Sync + 'static,
    ) -> DocStep {
        self.verify = Some(Arc::new(verify));
        self
    }

    pub fn with_note(mut self, note: impl Into<String>) -> DocStep {
        self.note = note.into();
        self
    }
}

impl fmt::Debug for DocStep {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("DocStep")
            .field("from", &self.from)
            .field("to", &self.to)
            .field("verify", &self.verify.is_some())
            .field("note", &self.note)
            .finish_non_exhaustive()
    }
}

/// Go `DocOptions`.
#[derive(Clone, Debug, Default)]
pub struct DocOptions {
    /// Dotted path of the version; `version` when empty.
    pub version_field: String,
    /// The version this build writes; 0 means the highest `to`.
    pub latest: i64,
    /// Read a missing or non-integer version as 0 instead of failing.
    pub missing_is_zero: bool,
}

/// One applied step. Go `DocApplied`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct DocApplied {
    pub from: i64,
    pub to: i64,
    pub note: String,
}

/// What a migration did. Go `DocReport`.
#[derive(Clone, Debug, PartialEq, Eq, Default)]
pub struct DocReport {
    pub from: i64,
    pub to: i64,
    pub applied: Vec<DocApplied>,
}

/// Go's error kinds (`ErrDocNewer`, ...; a failed verify is `ErrVerify`).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DocErrorKind {
    Newer,
    Version,
    Gap,
    Step,
    Verify,
    Invalid,
}

/// Why a document did not migrate. The texts match Go's.
#[derive(Debug, thiserror::Error)]
pub enum DocError {
    #[error("parse document: {0}")]
    Parse(String),
    #[error("document is not a JSON object")]
    NotObject,
    #[error("document has no integer version at {field:?}")]
    NoVersion { field: String },
    #[error("document version {found} is newer than the latest known {latest}")]
    Newer { found: i64, latest: i64 },
    #[error("no migration from version {from} (highest target is {latest}); chain has a gap")]
    Gap { from: i64, latest: i64 },
    #[error("migration {from} -> {to} does not move forward")]
    NotForward { from: i64, to: i64 },
    #[error("migration {from} -> {to} goes past the latest known {latest}")]
    PastLatest { from: i64, to: i64, latest: i64 },
    #[error("migration {from} -> {to}: {source}")]
    Step {
        from: i64,
        to: i64,
        source: BoxError,
    },
    #[error("migration {from} -> {to}: verify: {source}")]
    Verify {
        from: i64,
        to: i64,
        source: BoxError,
    },
}

impl DocError {
    pub fn kind(&self) -> DocErrorKind {
        match self {
            DocError::Newer { .. } => DocErrorKind::Newer,
            DocError::NoVersion { .. } => DocErrorKind::Version,
            DocError::Gap { .. } => DocErrorKind::Gap,
            DocError::Step { .. } => DocErrorKind::Step,
            DocError::Verify { .. } => DocErrorKind::Verify,
            DocError::Parse(_)
            | DocError::NotObject
            | DocError::NotForward { .. }
            | DocError::PastLatest { .. } => DocErrorKind::Invalid,
        }
    }
}

fn field(opts: &DocOptions) -> &str {
    if opts.version_field.is_empty() {
        "version"
    } else {
        &opts.version_field
    }
}

fn read_version(doc: &Value, path: &str) -> Option<i64> {
    path.split('.')
        .try_fold(doc, |cur, seg| cur.as_object()?.get(seg))?
        .as_i64()
}

fn write_version(doc: &mut Value, path: &str, version: i64) {
    let mut segs: Vec<&str> = path.split('.').collect();
    let last = segs.pop().unwrap_or(path);
    let mut cur = doc;
    for seg in segs {
        let obj = match cur {
            Value::Object(m) => m,
            other => {
                *other = Value::Object(Map::new());
                other.as_object_mut().expect("just made an object")
            }
        };
        cur = obj.entry(seg).or_insert_with(|| Value::Object(Map::new()));
        if !cur.is_object() {
            *cur = Value::Object(Map::new());
        }
    }
    if let Value::Object(m) = cur {
        m.insert(last.to_string(), Value::from(version));
    }
}

/// Migrates `doc` in place. On error `doc` is exactly as it was; steps
/// work on a copy that replaces it only once the whole chain succeeded.
pub fn migrate_value(
    doc: &mut Value,
    steps: &[DocStep],
    opts: &DocOptions,
) -> Result<DocReport, DocError> {
    if !doc.is_object() {
        return Err(DocError::NotObject);
    }
    let path = field(opts);
    let mut cur = match read_version(doc, path) {
        Some(v) => v,
        None if opts.missing_is_zero => 0,
        None => {
            return Err(DocError::NoVersion {
                field: path.to_string(),
            })
        }
    };
    let latest = if opts.latest == 0 {
        steps.iter().map(|s| s.to).max().unwrap_or(0)
    } else {
        opts.latest
    };
    for s in steps {
        if s.to <= s.from {
            return Err(DocError::NotForward {
                from: s.from,
                to: s.to,
            });
        }
        if s.to > latest {
            return Err(DocError::PastLatest {
                from: s.from,
                to: s.to,
                latest,
            });
        }
    }
    let mut report = DocReport {
        from: cur,
        to: cur,
        applied: Vec::new(),
    };
    if cur > latest {
        return Err(DocError::Newer { found: cur, latest });
    }
    if cur == latest {
        return Ok(report);
    }
    let mut work = doc.clone();
    while cur < latest {
        // The chain follows versions, not the order steps were given in.
        let Some(step) = steps.iter().find(|s| s.from == cur) else {
            return Err(DocError::Gap { from: cur, latest });
        };
        let (from, to) = (step.from, step.to);
        let mut out = (step.apply)(work).map_err(|source| DocError::Step { from, to, source })?;
        if let Some(verify) = &step.verify {
            verify(&out).map_err(|source| DocError::Verify { from, to, source })?;
        }
        write_version(&mut out, path, to);
        work = out;
        cur = to;
        report.to = to;
        report.applied.push(DocApplied {
            from,
            to,
            note: step.note.clone(),
        });
    }
    *doc = work;
    Ok(report)
}

/// [`migrate_value`] on document text. An already current document comes
/// back as the input itself (borrowed, byte for byte); otherwise it is
/// re-encoded.
pub fn migrate_document<'a>(
    doc: &'a str,
    steps: &[DocStep],
    opts: &DocOptions,
) -> Result<(Cow<'a, str>, DocReport), DocError> {
    let mut value: Value = serde_json::from_str(doc).map_err(|e| DocError::Parse(e.to_string()))?;
    let report = migrate_value(&mut value, steps, opts)?;
    if report.applied.is_empty() {
        return Ok((Cow::Borrowed(doc), report));
    }
    Ok((Cow::Owned(value.to_string()), report))
}
