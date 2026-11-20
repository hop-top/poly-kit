//! Shared internals: calendar arithmetic, ISO parsing, and the heuristics
//! that gate the natural-language fallback.

use interim::{parse_date_string, Dialect};
use jiff::{civil::Weekday, tz::TimeZone, Span, Timestamp};

use super::iso::parse_iso;
use super::{Direction, TimeError, Unit};

/// Apply `n` units of `unit` to `base` in the given direction.
///
/// Arithmetic runs on the instant seen in UTC, where a day is 24 hours.
/// Sub-day units are exact; day and week units move whole calendar days;
/// month and year units move calendar months and **clamp** overflow (see the
/// module-level docs for why clamping is the chosen semantics). A count or
/// result outside [`jiff`]'s range is [`TimeError::OutOfRange`].
pub(crate) fn shift(
    base: Timestamp,
    n: u32,
    unit: Unit,
    dir: Direction,
    input: &str,
) -> Result<Timestamp, TimeError> {
    let out_of_range = || TimeError::OutOfRange(input.to_string());
    let n = match dir {
        Direction::Forward => i64::from(n),
        Direction::Backward => -i64::from(n),
    };

    let span = match unit {
        Unit::Second => Span::new().try_seconds(n),
        Unit::Minute => Span::new().try_minutes(n),
        Unit::Hour => Span::new().try_hours(n),
        Unit::Day => Span::new().try_days(n),
        Unit::Week => Span::new().try_weeks(n),
        Unit::Month => Span::new().try_months(n),
        Unit::Year => Span::new().try_years(n),
    }
    .map_err(|_| out_of_range())?;

    base.to_zoned(TimeZone::UTC)
        .checked_add(span)
        .map(|zoned| zoned.timestamp())
        .map_err(|_| out_of_range())
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
pub(crate) fn parse_natural(s: &str, now: Timestamp) -> Result<Timestamp, TimeError> {
    if s.contains(' ') && looks_like_date(s) {
        if let Ok(parsed) = parse_date_string(s, now.to_zoned(TimeZone::UTC), Dialect::Us) {
            let parsed = parsed.timestamp();
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
fn truncate_bare_week_phrase(s: &str, parsed: Timestamp) -> Timestamp {
    let lower = s.trim().to_lowercase();
    if matches!(lower.as_str(), "next week" | "last week") {
        return parsed
            .to_zoned(TimeZone::UTC)
            .start_of_day()
            .map(|zoned| zoned.timestamp())
            .unwrap_or(parsed);
    }
    parsed
}

/// Resolve a weekday name to its [`Weekday`], case-insensitively.
pub(crate) fn weekday_from_name(s: &str) -> Option<Weekday> {
    match s.to_lowercase().as_str() {
        "sunday" => Some(Weekday::Sunday),
        "monday" => Some(Weekday::Monday),
        "tuesday" => Some(Weekday::Tuesday),
        "wednesday" => Some(Weekday::Wednesday),
        "thursday" => Some(Weekday::Thursday),
        "friday" => Some(Weekday::Friday),
        "saturday" => Some(Weekday::Saturday),
        _ => None,
    }
}

/// Days until the **next** occurrence of `target`, always in `1..=7`.
///
/// Ports the Go `days := int(target - current); if days <= 0 { days += 7 }`
/// rule. Note the consequence: asking for today's own weekday yields `7`, not
/// `0` — "friday" said on a Friday means *next* Friday.
pub(crate) fn days_until_weekday(now: Timestamp, target: Weekday) -> u32 {
    let current = i32::from(
        now.to_zoned(TimeZone::UTC)
            .weekday()
            .to_sunday_zero_offset(),
    );
    let target = i32::from(target.to_sunday_zero_offset());
    let diff = target - current;
    u32::try_from(if diff <= 0 { diff + 7 } else { diff }).unwrap_or(7)
}
