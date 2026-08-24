//! Forward-looking parsing — the port of Go's `until.go`.

use chrono::{DateTime, Days, Utc};

use super::common::{
    days_until_weekday, parse_count_unit, parse_natural, parse_short, shift, weekday_from_name,
};
use super::{Direction, TimeError};

/// Parse a forward-looking date string relative to [`Utc::now`].
///
/// See [`parse_until_at`] for the supported formats.
///
/// # Errors
///
/// Returns [`TimeError`] when the input is empty or matches no supported form.
pub fn parse_until(s: &str) -> Result<DateTime<Utc>, TimeError> {
    parse_until_at(s, Utc::now())
}

/// Parse a forward-looking date string relative to the given reference time.
///
/// Supported formats:
///
/// - `"tomorrow"`
/// - `"in N day(s)/week(s)/month(s)/year(s)/hour(s)/minute(s)/second(s)"`
/// - Short relative: `"+Nd"`, `"+Nh"`, `"+Nm"`, `"+Nw"`, `"+NM"`, `"+Ny"`
///   (`m` = minutes, `M` = months)
/// - Weekday names: `"monday"`, `"friday"` — the **next** occurrence, so the
///   current weekday resolves to seven days out, not zero
/// - Natural language: `"next monday"`, `"next week"`
/// - Month names: `"May 1"`, `"1 May 2026"`
/// - ISO 8601 and variants: `"2026-05-01"`, `"2026-05-01T10:30:00Z"`,
///   `"2026-05-01 10:30:00"`, `"2026-05-01 10:30"`, `"2026-05-01T10:30"`
///
/// Counts must be positive; `"in 0 days"` and `"+0d"` are errors.
///
/// # Errors
///
/// Returns [`TimeError`] when the input is empty, carries a non-positive or
/// non-numeric count, names an unknown unit, or matches no supported form.
///
/// # Examples
///
/// ```
/// use chrono::{TimeZone, Utc};
/// use hop_top_kit::timeutil::parse_until_at;
///
/// let now = Utc.with_ymd_and_hms(2026, 4, 19, 12, 0, 0).unwrap();
/// let got = parse_until_at("in 3 days", now).unwrap();
/// assert_eq!(got, Utc.with_ymd_and_hms(2026, 4, 22, 12, 0, 0).unwrap());
/// ```
pub fn parse_until_at(s: &str, now: DateTime<Utc>) -> Result<DateTime<Utc>, TimeError> {
    let s = s.trim();
    if s.is_empty() {
        return Err(TimeError::Empty);
    }

    if s == "tomorrow" {
        return now
            .checked_add_days(Days::new(1))
            .ok_or_else(|| TimeError::OutOfRange(s.to_string()));
    }

    // "in N <unit>" — dispatched on the prefix, so a malformed tail is a hard
    // error here rather than falling through to the later branches.
    if let Some(body) = s.strip_prefix("in ") {
        let (n, unit) = parse_count_unit(body, s)?;
        return shift(now, n, unit, Direction::Forward, s);
    }

    // "+3d", "+24h", ... — a failure here falls through, matching Go.
    if let Some(body) = s.strip_prefix('+') {
        if let Ok((n, unit)) = parse_short(body) {
            return shift(now, n, unit, Direction::Forward, s);
        }
    }

    if let Some(weekday) = weekday_from_name(s) {
        let days = days_until_weekday(now, weekday);
        return now
            .checked_add_days(Days::new(days))
            .ok_or_else(|| TimeError::OutOfRange(s.to_string()));
    }

    parse_natural(s, now)
}
