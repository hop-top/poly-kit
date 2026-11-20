//! Backward-looking parsing — the port of Go's `since.go`.

use jiff::Timestamp;

use super::common::{parse_count_unit, parse_natural, parse_short, shift};
use super::{Direction, TimeError, Unit};

/// Parse a `git --since`/`--after` compatible string relative to [`Timestamp::now`].
///
/// See [`parse_since_at`] for the supported formats.
///
/// # Errors
///
/// Returns [`TimeError`] when the input is empty or matches no supported form.
pub fn parse_since(s: &str) -> Result<Timestamp, TimeError> {
    parse_since_at(s, Timestamp::now())
}

/// Parse a datetime string relative to the given reference time.
///
/// Supported formats:
///
/// - `"yesterday"`
/// - `"N day(s)/week(s)/month(s)/year(s)/hour(s)/minute(s)/second(s) ago"`
/// - Short relative: `"Nd"`, `"Nh"`, `"Nm"`, `"Nw"`, `"NM"`, `"Ny"`
///   (`m` = minutes, `M` = months)
/// - Natural language: `"next monday"`, `"last week"`, `"2 weeks ago"`
/// - Month names: `"May 1"`, `"1 May 2026"`
/// - ISO 8601 and variants: `"2026-04-15"`, `"2026-04-15T10:30:00Z"`,
///   `"2026-04-15 10:30:00"`, `"2026-04-15 10:30"`, `"2026-04-15T10:30"`
///
/// Counts must be positive; `"0 days ago"` is an error.
///
/// # Errors
///
/// Returns [`TimeError`] when the input is empty, carries a non-positive or
/// non-numeric count, names an unknown unit, or matches no supported form.
///
/// # Examples
///
/// ```
/// use hop_top_kit::timeutil::parse_since_at;
///
/// let now: jiff::Timestamp = "2026-04-19T12:00:00Z".parse().unwrap();
/// let got = parse_since_at("3 days ago", now).unwrap();
/// assert_eq!(got, "2026-04-16T12:00:00Z".parse().unwrap());
/// ```
pub fn parse_since_at(s: &str, now: Timestamp) -> Result<Timestamp, TimeError> {
    let s = s.trim();
    if s.is_empty() {
        return Err(TimeError::Empty);
    }

    if s == "yesterday" {
        return shift(now, 1, Unit::Day, Direction::Backward, s);
    }

    // "N <unit> ago" — dispatched on the suffix, so a malformed head is a hard
    // error here rather than falling through to the later branches.
    if let Some(body) = s.strip_suffix(" ago") {
        let (n, unit) = parse_count_unit(body, s)?;
        return shift(now, n, unit, Direction::Backward, s);
    }

    // Short relative: "7d", "24h", ... — a failure here falls through.
    if let Ok((n, unit)) = parse_short(s) {
        return shift(now, n, unit, Direction::Backward, s);
    }

    parse_natural(s, now)
}
