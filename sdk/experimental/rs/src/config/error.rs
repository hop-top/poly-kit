//! Errors. Go returns wrapped errors matched with `errors.Is`/`As`; the
//! port returns one [`Error`] whose [`ErrorKind`] survives wrapping, and
//! whose text is Go's (`config get /p: yaml: line 1: ...`).

use std::fmt;

/// What went wrong, for matching. Wrapping keeps the kind of the
/// innermost cause, as `errors.Is` sees through `%w`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[non_exhaustive]
pub enum ErrorKind {
    /// Go `ErrKeyNotFound`.
    KeyNotFound,
    /// Go `ErrEmptyScope`.
    EmptyScope,
    /// Go `ErrNotFound`: no project marker while walking up.
    MarkerNotFound,
    /// Go `*ErrImmutableChanged`: a reload changed a field not marked
    /// reloadable. [`Error::immutable_paths`] lists them.
    ImmutableChanged,
    /// A file that must exist does not (`os.ErrNotExist`).
    NotFound,
    /// Other filesystem failure.
    Io,
    /// YAML syntax or decode failure.
    Yaml,
    /// Bad argument or state (empty key, malformed `-c` token, ...).
    Invalid,
    /// A migration chain failed.
    Migration,
    /// `set`/`unset` could not edit the file in place without dropping
    /// comments or anchors outside the edited entry; the file is left
    /// as it was. Go re-emits the document and never hits this; the
    /// port refuses rather than lose comments.
    EditUnsupported,
}

/// A config error.
#[derive(Clone, Debug)]
pub struct Error {
    kind: ErrorKind,
    msg: String,
    paths: Vec<String>,
}

impl Error {
    pub(crate) fn new(kind: ErrorKind, msg: impl Into<String>) -> Error {
        Error {
            kind,
            msg: msg.into(),
            paths: Vec::new(),
        }
    }

    pub(crate) fn invalid(msg: impl Into<String>) -> Error {
        Error::new(ErrorKind::Invalid, msg)
    }

    #[cfg(not(target_arch = "wasm32"))]
    pub(crate) fn immutable(paths: Vec<String>) -> Error {
        Error {
            kind: ErrorKind::ImmutableChanged,
            msg: format!(
                "config reload vetoed: immutable field(s) changed: {}",
                paths.join(", ")
            ),
            paths,
        }
    }

    /// Go's `fmt.Errorf("<prefix>: %w", err)`.
    pub(crate) fn wrap(mut self, prefix: impl fmt::Display) -> Error {
        self.msg = format!("{prefix}: {}", self.msg);
        self
    }

    /// The kind, for matching.
    pub fn kind(&self) -> ErrorKind {
        self.kind
    }

    /// Go `errors.Is(err, <sentinel>)`.
    pub fn is(&self, kind: ErrorKind) -> bool {
        self.kind == kind
    }

    /// For [`ErrorKind::ImmutableChanged`]: the dotted paths that
    /// changed, sorted. Empty otherwise.
    pub fn immutable_paths(&self) -> &[String] {
        &self.paths
    }
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.msg)
    }
}

impl std::error::Error for Error {}

impl From<super::yaml::ParseError> for Error {
    fn from(e: super::yaml::ParseError) -> Self {
        Error::new(ErrorKind::Yaml, e.to_string())
    }
}

impl From<super::yaml::de::DeError> for Error {
    fn from(e: super::yaml::de::DeError) -> Self {
        Error::new(ErrorKind::Yaml, e.0)
    }
}

impl From<super::yaml::ser::SerError> for Error {
    fn from(e: super::yaml::ser::SerError) -> Self {
        Error::new(ErrorKind::Yaml, e.0)
    }
}

/// Go-style text for an I/O error: `open <path>: no such file or
/// directory`. Missing files map to [`ErrorKind::NotFound`].
#[cfg(not(target_arch = "wasm32"))]
pub(crate) fn io_error(op: &str, path: &std::path::Path, e: &std::io::Error) -> Error {
    let kind = if e.kind() == std::io::ErrorKind::NotFound {
        ErrorKind::NotFound
    } else {
        ErrorKind::Io
    };
    let text = match e.kind() {
        std::io::ErrorKind::NotFound => "no such file or directory".to_string(),
        std::io::ErrorKind::PermissionDenied => "permission denied".to_string(),
        std::io::ErrorKind::IsADirectory => "is a directory".to_string(),
        std::io::ErrorKind::NotADirectory => "not a directory".to_string(),
        _ => strip_os_suffix(&e.to_string()),
    };
    Error::new(kind, format!("{op} {}: {text}", path.display()))
}

/// Rust appends ` (os error N)`; Go does not.
#[cfg(not(target_arch = "wasm32"))]
fn strip_os_suffix(s: &str) -> String {
    match s.rfind(" (os error ") {
        Some(i) => s[..i].to_lowercase(),
        None => s.to_string(),
    }
}

/// Schema errors raised by value validation (the `pkl` module), with
/// Go's texts.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SchemaError {
    /// Go `*ErrUnknownKey`.
    UnknownKey {
        key: String,
        valid_keys: Vec<String>,
    },
    /// Go `*ErrTypeMismatch`.
    TypeMismatch {
        key: String,
        expected: String,
        got: String,
    },
    /// Go `*ErrConstraintViolation`.
    ConstraintViolation {
        key: String,
        constraint: String,
        value: String,
    },
}

impl fmt::Display for SchemaError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        use super::goquote::quote;
        match self {
            SchemaError::UnknownKey { key, valid_keys } => write!(
                f,
                "unknown key {}; valid keys: {}",
                quote(key),
                valid_keys.join(", ")
            ),
            SchemaError::TypeMismatch { key, expected, got } => write!(
                f,
                "key {} expects {expected}, got {}",
                quote(key),
                quote(got)
            ),
            SchemaError::ConstraintViolation {
                key,
                constraint,
                value,
            } => write!(
                f,
                "key {} violates constraint {constraint}: {value}",
                quote(key)
            ),
        }
    }
}

impl std::error::Error for SchemaError {}
