//! Shared internals: calendar arithmetic, ISO parsing, and the heuristics
//! that gate the natural-language fallback.

use chrono::{DateTime, Datelike, Days, Duration, Months, NaiveDate, NaiveDateTime, Utc};
use interim::{parse_date_string, Dialect};

use super::{Direction, TimeError, Unit};

/// Apply `n` units of `unit` to `base` in the given direction.
///
/// Sub-day units use exact [`Duration`] arithmetic; day and week units use
/// [`Days`]; month and year units use [`Months`], which **clamps** overflow
/// (see the module-level docs for why clamping is the chosen semantics).
pub(crate) fn shift(
    base: DateTime<Utc>,
    n: u32,
    unit: Unit,
    dir: Direction,
    input: &str,
) -> Result<DateTime<Utc>, TimeError> {
    let out_of_range = || TimeError::OutOfRange(input.to_string());
    let n64 = i64::from(n);

    let result = match unit {
        Unit::Second | Unit::Minute | Unit::Hour => {
            let span = match unit {
                Unit::Second => Duration::try_seconds(n64),
                Unit::Minute => Duration::try_minutes(n64),
                _ => Duration::try_hours(n64),
            }
            .ok_or_else(out_of_range)?;
            match dir {
                Direction::Forward => base.checked_add_signed(span),
                Direction::Backward => base.checked_sub_signed(span),
            }
        }
        Unit::Day | Unit::Week => {
            let days = if unit == Unit::Week {
                n64.checked_mul(7).ok_or_else(out_of_range)?
            } else {
                n64
            };
            let days = Days::new(u64::try_from(days).map_err(|_| out_of_range())?);
            match dir {
                Direction::Forward => base.checked_add_days(days),
                Direction::Backward => base.checked_sub_days(days),
            }
        }
        Unit::Month | Unit::Year => {
            let months = if unit == Unit::Year {
                n.checked_mul(12).ok_or_else(out_of_range)?
            } else {
                n
            };
            let months = Months::new(months);
            match dir {
                Direction::Forward => base.checked_add_months(months),
                Direction::Backward => base.checked_sub_months(months),
            }
        }
    };

    result.ok_or_else(out_of_range)
}

/// Parse the count/unit pair shared by `"in N unit"` and `"N unit ago"`.
///
/// The count must be a **positive** integer: zero and negatives are rejected,
/// matching the Go `n <= 0` guard in both `parseForward` and `parseRelative`.
pub(crate) fn parse_count_unit(body: &str, original: &str) -> Result<(u32, Unit), TimeError> {
    let (count, unit_word) = body
        .split_once(' ')
        .ok_or_else(|| TimeError::InvalidRelative(original.to_string()))?;

    let n: i64 = count
        .parse()
        .map_err(|_| TimeError::InvalidCount(count.to_string()))?;
    let n = u32::try_from(n)
        .ok()
        .filter(|&n| n > 0)
        .ok_or_else(|| TimeError::InvalidCount(count.to_string()))?;

    let unit =
        Unit::from_word(unit_word).ok_or_else(|| TimeError::UnknownUnit(unit_word.to_string()))?;

    Ok((n, unit))
}

/// Parse the compact `"<N><suffix>"` form, e.g. `"7d"` / `"3M"` / `"30m"`.
///
/// `'m'` is minutes and `'M'` is months — see [`Unit::from_suffix`].
pub(crate) fn parse_short(body: &str) -> Result<(u32, Unit), TimeError> {
    let mut chars = body.chars();
    let suffix = chars
        .next_back()
        .ok_or_else(|| TimeError::Unrecognized(body.to_string()))?;
    let digits = chars.as_str();

    if digits.is_empty() {
        return Err(TimeError::Unrecognized(body.to_string()));
    }

    let n: i64 = digits
        .parse()
        .map_err(|_| TimeError::InvalidCount(digits.to_string()))?;
    let n = u32::try_from(n)
        .ok()
        .filter(|&n| n > 0)
        .ok_or_else(|| TimeError::InvalidCount(digits.to_string()))?;

    let unit =
        Unit::from_suffix(suffix).ok_or_else(|| TimeError::Unrecognized(body.to_string()))?;

    Ok((n, unit))
}

/// Absolute layouts tried in order, mirroring the Go `isoLayouts` table.
///
/// Layouts carrying an explicit offset are tried as [`DateTime`]; the rest are
/// parsed as naive local wall-clock time and interpreted as UTC, matching Go's
/// `time.Parse` behaviour for layouts without a zone.
const OFFSET_LAYOUTS: &[&str] = &[
    "%Y-%m-%dT%H:%M:%S%.f%:z",
    "%Y-%m-%dT%H:%M:%S%#z",
    "%Y-%m-%dT%H:%M%:z",
    "%Y-%m-%dT%H:%M%#z",
];

const NAIVE_LAYOUTS: &[&str] = &[
    "%Y-%m-%dT%H:%M:%S%.f",
    "%Y-%m-%dT%H:%M",
    "%Y-%m-%d %H:%M:%S%.f",
    "%Y-%m-%d %H:%M",
];

/// Parse an absolute ISO-8601-ish timestamp.
///
/// Handles the offset-bearing forms (`Z`, `+05:00`) as well as the
/// offset-less forms, which are interpreted as UTC. Date-only input
/// (`"2026-05-01"`) resolves to midnight UTC.
pub(crate) fn parse_iso(s: &str) -> Result<DateTime<Utc>, TimeError> {
    for layout in OFFSET_LAYOUTS {
        if let Ok(dt) = DateTime::parse_from_str(s, layout) {
            return Ok(dt.with_timezone(&Utc));
        }
    }

    for layout in NAIVE_LAYOUTS {
        if let Ok(naive) = NaiveDateTime::parse_from_str(s, layout) {
            return naive
                .and_local_timezone(Utc)
                .single()
                .ok_or_else(|| TimeError::Unrecognized(s.to_string()));
        }
    }

    if let Ok(date) = NaiveDate::parse_from_str(s, "%Y-%m-%d") {
        return date
            .and_hms_opt(0, 0, 0)
            .and_then(|naive| naive.and_local_timezone(Utc).single())
            .ok_or_else(|| TimeError::Unrecognized(s.to_string()));
    }

    Err(TimeError::Unrecognized(s.to_string()))
}

/// Month names and abbreviations recognised by [`looks_like_date`].
const MONTHS: &[&str] = &[
    "jan",
    "feb",
    "mar",
    "apr",
    "may",
    "jun",
    "jul",
    "aug",
    "sep",
    "oct",
    "nov",
    "dec",
    "january",
    "february",
    "march",
    "april",
    "june",
    "july",
    "august",
    "september",
    "october",
    "november",
    "december",
];

/// Relative phrases recognised by [`looks_like_date`].
const RELATIVE_PHRASES: &[&str] = &["next ", "last ", "this ", "day", "week", "month", "year"];

/// Heuristic gate on the natural-language fallback, ported from Go's
/// `looksLikeDate`.
///
/// Returns `true` when the string carries a month name, any date-ish
/// punctuation or digit, or a relative phrase. Note the longer month names are
/// redundant with their own three-letter prefixes — the Go list is kept
/// verbatim so the predicate stays literally comparable.
pub(crate) fn looks_like_date(s: &str) -> bool {
    let lower = s.to_lowercase();

    if MONTHS.iter().any(|m| lower.contains(m)) {
        return true;
    }
    if s.contains(['-', '/', ':']) || s.chars().any(|c| c.is_ascii_digit()) {
        return true;
    }
    RELATIVE_PHRASES.iter().any(|p| lower.contains(p))
}

/// Try the natural-language parser, then fall back to absolute ISO parsing.
///
/// Two guards are ported verbatim from Go, and both matter:
///
/// 1. The natural-language parser is only consulted when the input contains a
///    space **and** passes [`looks_like_date`].
/// 2. A result equal to the reference instant is treated as a **failure**.
///    `interim`, like `tj/go-naturaldate`, echoes the reference instant back
///    for inputs it does not really understand (`"now"`, `"today"`,
///    `"this week"`), so accepting that result would turn unparseable input
///    into a silent "right now" rather than an error.
pub(crate) fn parse_natural(s: &str, now: DateTime<Utc>) -> Result<DateTime<Utc>, TimeError> {
    if s.contains(' ') && looks_like_date(s) {
        if let Ok(parsed) = parse_date_string(s, now, Dialect::Us) {
            if parsed != now {
                return Ok(truncate_bare_week_phrase(s, parsed));
            }
        }
    }
    parse_iso(s)
}

/// Truncate `"next week"` / `"last week"` to midnight, matching Go.
///
/// `go-naturaldate` resolves a bare week phrase to the start of that day,
/// while `interim` carries the reference time-of-day forward — so with a
/// reference of 12:00, Go yields `00:00` and `interim` yields `12:00` for
/// the same calendar date. Every other phrase `interim` handles
/// (`"next monday"`, `"last friday"`, `"1 May 2026"`) already lands on
/// midnight, so this is deliberately scoped to the bare week phrases
/// rather than applied as a blanket truncation: flattening every natural
/// result would also flatten the day-offset forms that are *supposed* to
/// preserve the time of day (`"3 days ago"` from 12:00 is 12:00 in both
/// implementations).
fn truncate_bare_week_phrase(s: &str, parsed: DateTime<Utc>) -> DateTime<Utc> {
    let lower = s.trim().to_lowercase();
    if matches!(lower.as_str(), "next week" | "last week") {
        return parsed
            .date_naive()
            .and_hms_opt(0, 0, 0)
            .map(|naive| naive.and_utc())
            .unwrap_or(parsed);
    }
    parsed
}

/// Resolve a weekday name to its [`chrono::Weekday`], case-insensitively.
pub(crate) fn weekday_from_name(s: &str) -> Option<chrono::Weekday> {
    match s.to_lowercase().as_str() {
        "sunday" => Some(chrono::Weekday::Sun),
        "monday" => Some(chrono::Weekday::Mon),
        "tuesday" => Some(chrono::Weekday::Tue),
        "wednesday" => Some(chrono::Weekday::Wed),
        "thursday" => Some(chrono::Weekday::Thu),
        "friday" => Some(chrono::Weekday::Fri),
        "saturday" => Some(chrono::Weekday::Sat),
        _ => None,
    }
}

/// Days until the **next** occurrence of `target`, always in `1..=7`.
///
/// Ports the Go `days := int(target - current); if days <= 0 { days += 7 }`
/// rule. Note the consequence: asking for today's own weekday yields `7`, not
/// `0` — "friday" said on a Friday means *next* Friday.
pub(crate) fn days_until_weekday(now: DateTime<Utc>, target: chrono::Weekday) -> u64 {
    let current = i64::from(now.weekday().num_days_from_sunday());
    let target = i64::from(target.num_days_from_sunday());
    let diff = target - current;
    u64::try_from(if diff <= 0 { diff + 7 } else { diff }).unwrap_or(7)
}
