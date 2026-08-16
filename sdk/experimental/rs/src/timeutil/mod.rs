//! Relative-date parsing — forward (`until`) and backward (`since`).
//!
//! Mirrors the surface shipped in kit-go `go/core/util/{until,since}.go`:
//!
//! - [`parse_until`] / [`parse_until_at`] — forward-looking (`"tomorrow"`,
//!   `"in 3 days"`, `"+3d"`, `"friday"`, `"next monday"`).
//! - [`parse_since`] / [`parse_since_at`] — backward-looking (`"yesterday"`,
//!   `"3 days ago"`, `"3d"`, `"last week"`).
//!
//! The `*_at` variants take an explicit reference instant; the bare ones use
//! [`Utc::now`]. Keeping the split is what makes the test suite deterministic.
//!
//! Natural-language phrases (`"next monday"`, `"last week"`, `"1 May 2026"`)
//! are delegated to the [`interim`] crate — a maintained fork of
//! `chrono-english`. The deterministic forms are parsed natively and tried
//! first; see the seam table below.
//!
//! # Calendar arithmetic: month overflow clamps
//!
//! Adding a month to a day-of-month that does not exist in the target month
//! *clamps* to that month's last valid day: `2026-01-31` plus one month is
//! `2026-02-28`, not `2026-03-03`.
//!
//! Rationale: for a date utility whose inputs are user-facing phrases like
//! `"in 1 month"`, landing in the *named* target month is the least surprising
//! outcome. A user who writes `"+1M"` on January 31st means "some time in
//! February", not "early March"; silently skipping the requested month is a
//! bug from the caller's point of view. Clamping is also what most calendar
//! libraries and human intuition converge on.
//!
//! Go's `time.AddDate` normalises rather than clamps, so the canonical Go
//! implementation is being changed to clamp as well — the two runtimes agree
//! by construction, not by coincidence. This is a behavioural change on the
//! Go side, not a Rust-only divergence.
//!
//! The behaviour is pinned by [`tests::month_arithmetic_clamps_it_does_not_normalise`]
//! so it cannot drift silently.
//!
//! # Supported formats
//!
//! See the doc comments on [`parse_until_at`] and [`parse_since_at`].

//! # Which forms are native, and which come from [`interim`]
//!
//! The deterministic forms are parsed **natively and tried first**, so their
//! results are exact and independent of `interim`'s grammar. `interim` is
//! consulted only as the final fallback, for the genuinely hard part:
//!
//! | Form | Owner | Why |
//! |---|---|---|
//! | `"tomorrow"` / `"yesterday"` | native | trivial, and must preserve time-of-day |
//! | `"in N <unit>"` | native | `interim` rejects the `in` prefix outright |
//! | `"N <unit> ago"` | native | exact integer arithmetic, positive-count guard |
//! | `"+N<unit>"` / `"N<unit>"` short | native | `interim` rejects `+3d`; `m`/`M` case split is ours |
//! | Weekday names (`"friday"`) | native | `interim` agrees on the date but normalises to midnight; Go preserves time-of-day |
//! | ISO 8601 / absolute | native | fixed layout table, ported from Go's `isoLayouts` |
//! | `"next monday"`, `"last week"`, `"1 May 2026"` | **`interim`** | real natural language — not hand-rolled |
//!
//! `interim` is configured with [`Dialect::Us`]; the UK dialect reads
//! `"next monday"` as the Monday of *next* week and does not match the Go
//! reference. Only its `chrono_0_4` feature is enabled, so its single
//! meaningful transitive dependency is the `logos` lexer.
//!
//! [`Dialect::Us`]: interim::Dialect::Us

mod common;
mod since;
mod until;

use thiserror::Error;

pub use since::{parse_since, parse_since_at};
pub use until::{parse_until, parse_until_at};

/// Errors returned by the relative-date parsers.
#[derive(Debug, Error, PartialEq, Eq, Clone)]
pub enum TimeError {
    /// The input was empty or contained only whitespace.
    #[error("timeutil: empty input")]
    Empty,

    /// A relative expression was missing its unit, e.g. `"in 3"` or `"3 ago"`.
    #[error("timeutil: invalid relative format {0:?}")]
    InvalidRelative(String),

    /// The count was not a positive integer, e.g. `"in 0 days"` or `"-3 days ago"`.
    #[error("timeutil: invalid count {0:?}")]
    InvalidCount(String),

    /// The unit was not one of second/minute/hour/day/week/month/year.
    #[error("timeutil: unknown unit {0:?}")]
    UnknownUnit(String),

    /// The input did not match any supported format.
    #[error("timeutil: unrecognized format {0:?}")]
    Unrecognized(String),

    /// The computed instant fell outside the range chrono can represent.
    #[error("timeutil: date out of range for {0:?}")]
    OutOfRange(String),
}

/// The calendar units understood by both the long (`"in 3 days"`) and short
/// (`"+3d"`) relative forms.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum Unit {
    Second,
    Minute,
    Hour,
    Day,
    Week,
    Month,
    Year,
}

impl Unit {
    /// Resolve a spelled-out unit name, singular-ising a trailing `"s"`.
    ///
    /// Mirrors the Go `strings.TrimSuffix(parts[1], "s")` normalisation, so
    /// `"days"` and `"day"` both resolve, and — as in Go — the degenerate
    /// `"s"` trims to `""` and fails to resolve.
    pub(crate) fn from_word(word: &str) -> Option<Self> {
        let singular = word.strip_suffix('s').unwrap_or(word);
        match singular {
            "second" => Some(Self::Second),
            "minute" => Some(Self::Minute),
            "hour" => Some(Self::Hour),
            "day" => Some(Self::Day),
            "week" => Some(Self::Week),
            "month" => Some(Self::Month),
            "year" => Some(Self::Year),
            _ => None,
        }
    }

    /// Resolve a single-character suffix from the short form.
    ///
    /// **Case matters**: `'m'` is minutes, `'M'` is months. Reversing these
    /// silently produces dates that are wrong by orders of magnitude, so the
    /// distinction is pinned by dedicated tests.
    pub(crate) fn from_suffix(suffix: char) -> Option<Self> {
        match suffix {
            's' => Some(Self::Second),
            'm' => Some(Self::Minute),
            'h' => Some(Self::Hour),
            'd' => Some(Self::Day),
            'w' => Some(Self::Week),
            'M' => Some(Self::Month),
            'y' => Some(Self::Year),
            _ => None,
        }
    }
}

/// Which way a relative expression moves from the reference instant.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum Direction {
    Forward,
    Backward,
}
