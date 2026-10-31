//! The prompter seam: the caller's UI decides.

use std::time::SystemTime;

use super::error::Reason;
use super::grant::{Duration, Effect};

/// What a [`Prompter`] is asked to decide.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Prompt {
    pub capability: String,
    pub subject: String,
    pub prompt_version: i64,
    /// `Unset` or `Expired`: why there is no standing decision.
    pub reason: Reason,
}

/// The user's decision. `expires_at` is set for `Until` only.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Answer {
    pub decision: Effect,
    pub duration: Duration,
    pub expires_at: Option<SystemTime>,
}

/// The caller's UI: a TTY prompt, a dialog, a shell sheet. Return an
/// error when you cannot ask (no TTY, headless, dismissed); the request
/// then fails closed. `Grants::request` holds no lock while it runs, so
/// a prompter may call `Grants::check`.
pub trait Prompter: Send + Sync {
    fn ask(&self, prompt: &Prompt) -> Result<Answer, String>;
}

impl<F> Prompter for F
where
    F: Fn(&Prompt) -> Result<Answer, String> + Send + Sync,
{
    fn ask(&self, prompt: &Prompt) -> Result<Answer, String> {
        self(prompt)
    }
}
