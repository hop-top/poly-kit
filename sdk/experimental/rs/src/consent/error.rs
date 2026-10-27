//! Grant errors, refusals and their exit taxonomy.

use std::fmt;

/// A class of grant error. The strings are the contract; messages are
/// not.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum ErrorKind {
    InvalidCapability,
    InvalidSubject,
    InvalidDecision,
    InvalidDuration,
    InvalidExpiry,
    InvalidSource,
    UnsupportedScope,
    MalformedStore,
}

impl ErrorKind {
    /// The wire string (`invalid_capability`, ...).
    pub fn as_str(self) -> &'static str {
        match self {
            ErrorKind::InvalidCapability => "invalid_capability",
            ErrorKind::InvalidSubject => "invalid_subject",
            ErrorKind::InvalidDecision => "invalid_decision",
            ErrorKind::InvalidDuration => "invalid_duration",
            ErrorKind::InvalidExpiry => "invalid_expiry",
            ErrorKind::InvalidSource => "invalid_source",
            ErrorKind::UnsupportedScope => "unsupported_scope",
            ErrorKind::MalformedStore => "malformed_store",
        }
    }
}

impl fmt::Display for ErrorKind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

/// A grant validation or store error of one kind.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct GrantError {
    pub kind: ErrorKind,
    pub message: String,
}

impl GrantError {
    pub fn new(kind: ErrorKind, message: impl Into<String>) -> Self {
        GrantError {
            kind,
            message: message.into(),
        }
    }

    #[cfg(feature = "consent-file")]
    pub(crate) fn malformed(message: impl Into<String>) -> Self {
        GrantError::new(ErrorKind::MalformedStore, message)
    }
}

impl fmt::Display for GrantError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "consent: {}", self.kind)?;
        if !self.message.is_empty() {
            write!(f, ": {}", self.message)?;
        }
        Ok(())
    }
}

impl std::error::Error for GrantError {}

/// Why a check came out the way it did.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Reason {
    /// An allow grant holds.
    Allowed,
    /// A deny grant holds (for telemetry also an env opt-out).
    Denied,
    /// Nothing holds and nothing expired.
    Unset,
    /// The governing grant was an until grant past its expiry.
    Expired,
    /// The prompt was just answered deny. Refusals only.
    Declined,
}

impl Reason {
    pub fn as_str(self) -> &'static str {
        match self {
            Reason::Allowed => "allowed",
            Reason::Denied => "denied",
            Reason::Unset => "unset",
            Reason::Expired => "expired",
            Reason::Declined => "declined",
        }
    }
}

impl fmt::Display for Reason {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

/// Exit code of a standing deny (`UNAUTHORIZED`).
pub const EXIT_DENIED: i32 = 5;
/// Exit code of a refusal a prompt can clear (`CONSENT_REFUSED`).
pub const EXIT_REFUSED: i32 = 7;

/// A fail-closed refusal of one capability for one subject.
///
/// `denied` is a standing no: `UNAUTHORIZED`, exit 5, permanent; only
/// the user changing or revoking the grant clears it. `unset`,
/// `expired` and `declined` are `CONSENT_REFUSED`, exit 7, transient:
/// asking again where a prompt can run clears them.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct RefusedError {
    pub capability: String,
    pub subject: String,
    pub reason: Reason,
    /// The prompter failure, if one caused the refusal.
    pub cause: Option<String>,
}

impl RefusedError {
    /// Whether the refusal is a standing deny.
    pub fn permanent(&self) -> bool {
        self.reason == Reason::Denied
    }

    /// `UNAUTHORIZED` or `CONSENT_REFUSED`.
    pub fn code(&self) -> &'static str {
        if self.permanent() {
            "UNAUTHORIZED"
        } else {
            "CONSENT_REFUSED"
        }
    }

    /// 5 or 7.
    pub fn exit_code(&self) -> i32 {
        if self.permanent() {
            EXIT_DENIED
        } else {
            EXIT_REFUSED
        }
    }

    /// `permanent` or `transient`.
    pub fn transience(&self) -> &'static str {
        if self.permanent() {
            "permanent"
        } else {
            "transient"
        }
    }

    /// The refusal on the kit exit taxonomy, so a command can return it
    /// as is.
    #[cfg(feature = "output")]
    pub fn to_cli_error(&self) -> crate::output::CliError {
        use crate::output::CliError;
        let mut out = if self.permanent() {
            CliError::unauthorized(self.to_string())
        } else {
            CliError::consent_refused(self.to_string())
        };
        out.suggested_fix = if self.permanent() {
            format!(
                "{} was denied; only the user can change or revoke that decision",
                self.capability
            )
        } else {
            format!(
                "grant {} where a consent prompt can run, then retry",
                self.capability
            )
        };
        out
    }
}

impl fmt::Display for RefusedError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let who = if self.subject.is_empty() {
            "any subject"
        } else {
            &self.subject
        };
        write!(
            f,
            "consent: {} for {} refused: {}",
            self.capability, who, self.reason
        )?;
        if let Some(c) = &self.cause {
            write!(f, ": {c}")?;
        }
        Ok(())
    }
}

impl std::error::Error for RefusedError {}

/// Every consent failure. A refusal is only ever [`Error::Refused`];
/// store, validation and audit failures are never refusals and must
/// stop the caller just the same.
#[derive(Clone, Debug, PartialEq, Eq)]
#[non_exhaustive]
pub enum Error {
    Grant(GrantError),
    Refused(RefusedError),
    /// An auditor failed; on `use_grant` the use is refused with it.
    Audit(String),
    /// A file could not be written (or read outside the grants file).
    Io(String),
}

impl Error {
    /// The grant error kind, `None` for refusals, audit and I/O errors.
    pub fn kind(&self) -> Option<ErrorKind> {
        match self {
            Error::Grant(g) => Some(g.kind),
            _ => None,
        }
    }

    /// The refusal, when this is one.
    pub fn refusal(&self) -> Option<&RefusedError> {
        match self {
            Error::Refused(r) => Some(r),
            _ => None,
        }
    }
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::Grant(e) => e.fmt(f),
            Error::Refused(e) => e.fmt(f),
            Error::Audit(m) | Error::Io(m) => f.write_str(m),
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Error::Grant(e) => Some(e),
            Error::Refused(e) => Some(e),
            _ => None,
        }
    }
}

impl From<GrantError> for Error {
    fn from(e: GrantError) -> Self {
        Error::Grant(e)
    }
}

impl From<RefusedError> for Error {
    fn from(e: RefusedError) -> Self {
        Error::Refused(e)
    }
}
