//! The typed denial and the configuration/compile error.

use std::error::Error as StdError;
use std::fmt;

use crate::domain::quote::quote;

use super::config::Decision;

/// A policy refused the event. Go's `*PolicyDeniedError`.
///
/// Go's `Unwrap` returns `domain.ErrConflict`; the Rust domain has no
/// such sentinel, so [`is_conflict`](Self::is_conflict) is the check and
/// [`to_cli_error`](Self::to_cli_error) the CONFLICT envelope (exit 4).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PolicyDeniedError {
    /// The denying policy's name.
    pub policy_name: String,
    /// The topic the event was published on.
    pub topic: String,
    /// The policy's message, or the evaluation error text.
    pub message: String,
    /// Always [`Decision::Deny`].
    pub decision: Decision,
}

impl PolicyDeniedError {
    /// Always true: Go's `errors.Is(err, domain.ErrConflict)`.
    pub fn is_conflict(&self) -> bool {
        true
    }

    /// The CONFLICT envelope: Go's `AsCLIError`. Permanent, exit 4, the
    /// cause names the policy and topic, the fix names the policy.
    #[cfg(feature = "output")]
    pub fn to_cli_error(&self) -> crate::output::CliError {
        let mut env = crate::output::CliError::conflict(self.to_string());
        env.cause = format!(
            "policy {} denied topic {}",
            quote(&self.policy_name),
            quote(&self.topic)
        );
        env.suggested_fix = format!(
            "review policy {} in the loaded policy file, or supply input that satisfies it",
            quote(&self.policy_name)
        );
        env
    }
}

#[cfg(feature = "output")]
impl From<&PolicyDeniedError> for crate::output::CliError {
    fn from(e: &PolicyDeniedError) -> Self {
        e.to_cli_error()
    }
}

impl fmt::Display for PolicyDeniedError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        if self.message.is_empty() {
            write!(f, "policy {} denied", quote(&self.policy_name))
        } else {
            write!(
                f,
                "policy {} denied: {}",
                quote(&self.policy_name),
                self.message
            )
        }
    }
}

impl StdError for PolicyDeniedError {}

/// A rule file that does not parse, an expression that does not compile,
/// or an engine built without an evaluator. Carries Go's message text.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Error(pub(crate) String);

impl Error {
    pub(crate) fn new(message: impl Into<String>) -> Self {
        Error(message.into())
    }
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl StdError for Error {}

/// Whether `err` or anything in its source chain is a
/// [`PolicyDeniedError`]: Go's `errors.As(err, &pde)` for the cases the
/// denial is kept typed (the bus flattens it to text, see the module
/// docs).
pub fn is_policy_denied(err: &(dyn StdError + 'static)) -> bool {
    let mut cur = Some(err);
    while let Some(e) = cur {
        if e.is::<PolicyDeniedError>() {
            return true;
        }
        cur = e.source();
    }
    false
}
