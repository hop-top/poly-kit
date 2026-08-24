//! Parity suite for the `timeutil` module.
//!
//! Ported case-for-case from the Go tables in
//! `go/core/util/until_test.go` and `go/core/util/since_test.go`, using the
//! same fixed reference instant so expectations line up exactly.
//!
//! The Go source comments the reference as "Saturday"; that comment is stale.
//! 2026-04-19 is a **Sunday**, which is what the weekday assertions encode.

#![cfg(feature = "timeutil")]

use chrono::{DateTime, Days, Duration, Months, TimeZone, Utc};
use hop_top_kit::timeutil::{parse_since, parse_since_at, parse_until, parse_until_at};

/// The fixed reference instant shared with the Go tests: Sunday 2026-04-19 12:00 UTC.
fn now() -> DateTime<Utc> {
    Utc.with_ymd_and_hms(2026, 4, 19, 12, 0, 0).unwrap()
}

fn at(y: i32, m: u32, d: u32, hh: u32, mm: u32, ss: u32) -> DateTime<Utc> {
    Utc.with_ymd_and_hms(y, m, d, hh, mm, ss).unwrap()
}

fn plus_days(n: u64) -> DateTime<Utc> {
    now().checked_add_days(Days::new(n)).unwrap()
}

fn minus_days(n: u64) -> DateTime<Utc> {
    now().checked_sub_days(Days::new(n)).unwrap()
}

fn plus_months(n: u32) -> DateTime<Utc> {
    now().checked_add_months(Months::new(n)).unwrap()
}

fn minus_months(n: u32) -> DateTime<Utc> {
    now().checked_sub_months(Months::new(n)).unwrap()
}

// ---------------------------------------------------------------------------
// until — the port of Go's TestParseUntil
// ---------------------------------------------------------------------------

#[test]
fn parse_until_table() {
    let cases: Vec<(&str, &str, DateTime<Utc>)> = vec![
        // named relative
        ("tomorrow", "tomorrow", plus_days(1)),
        // "in N <unit>"
        ("in 1 day", "in 1 day", plus_days(1)),
        ("in 3 days", "in 3 days", plus_days(3)),
        ("in 1 week", "in 1 week", plus_days(7)),
        ("in 2 weeks", "in 2 weeks", plus_days(14)),
        ("in 1 month", "in 1 month", plus_months(1)),
        ("in 6 months", "in 6 months", plus_months(6)),
        ("in 1 year", "in 1 year", plus_months(12)),
        ("in 2 years", "in 2 years", plus_months(24)),
        ("in 1 hour", "in 1 hour", now() + Duration::hours(1)),
        ("in 3 hours", "in 3 hours", now() + Duration::hours(3)),
        (
            "in 30 minutes",
            "in 30 minutes",
            now() + Duration::minutes(30),
        ),
        ("in 5 seconds", "in 5 seconds", now() + Duration::seconds(5)),
        // short relative: +Nd, +Nh, ...
        ("+3d", "+3d", plus_days(3)),
        ("+24h", "+24h", now() + Duration::hours(24)),
        ("+30m", "+30m", now() + Duration::minutes(30)),
        ("+2w", "+2w", plus_days(14)),
        ("+3M", "+3M", plus_months(3)),
        ("+1y", "+1y", plus_months(12)),
        ("+10s", "+10s", now() + Duration::seconds(10)),
        // weekday names (now = Sunday 2026-04-19)
        ("monday", "monday", plus_days(1)),
        ("tuesday", "tuesday", plus_days(2)),
        ("wednesday", "wednesday", plus_days(3)),
        ("thursday", "thursday", plus_days(4)),
        ("friday", "friday", plus_days(5)),
        ("saturday", "saturday", plus_days(6)),
        ("sunday", "sunday", plus_days(7)),
        ("Friday uppercase", "Friday", plus_days(5)),
        // natural language relative dates
        ("next monday", "next monday", at(2026, 4, 20, 0, 0, 0)),
        ("next friday", "next friday", at(2026, 4, 24, 0, 0, 0)),
        // ISO 8601
        ("date only", "2026-05-01", at(2026, 5, 1, 0, 0, 0)),
        (
            "datetime UTC",
            "2026-05-01T10:30:00Z",
            at(2026, 5, 1, 10, 30, 0),
        ),
        (
            "datetime offset",
            "2026-05-01T10:30:00+05:00",
            at(2026, 5, 1, 5, 30, 0),
        ),
        // common absolute formats (space-separated)
        (
            "date time space sep",
            "2026-05-01 10:30:00",
            at(2026, 5, 1, 10, 30, 0),
        ),
        (
            "date time space no seconds",
            "2026-05-01 10:30",
            at(2026, 5, 1, 10, 30, 0),
        ),
        // T separator without seconds
        (
            "datetime T no seconds",
            "2026-05-01T10:30",
            at(2026, 5, 1, 10, 30, 0),
        ),
        (
            "datetime T with offset no seconds",
            "2026-05-01T10:30+05:00",
            at(2026, 5, 1, 5, 30, 0),
        ),
        // whitespace tolerance
        ("leading/trailing spaces", "  in 3 days  ", plus_days(3)),
    ];

    for (name, input, expected) in cases {
        let got = parse_until_at(input, now())
            .unwrap_or_else(|e| panic!("{name}: input {input:?} errored: {e}"));
        assert_eq!(got, expected, "{name}: input {input:?}");
    }
}

/// `"next week"` matches Go exactly, date *and* time.
///
/// `interim` carries the reference time-of-day forward on a bare week
/// phrase where `go-naturaldate` truncates to midnight, so the result is
/// truncated back to `00:00` to keep the two implementations identical.
/// Asserting the full instant, not just `date_naive()`, is the point: a
/// weaker assertion would let the divergence silently return.
#[test]
fn parse_until_next_week_matches_go_exactly() {
    let got = parse_until_at("next week", now()).unwrap();
    assert_eq!(got, at(2026, 4, 26, 0, 0, 0));
}

#[test]
fn parse_until_errors() {
    let cases = [
        ("empty", ""),
        ("garbage", "not a date"),
        ("negative count", "in -3 days"),
        ("zero count", "in 0 days"),
        ("missing unit", "in 3"),
        ("unknown unit", "in 3 fortnights"),
        ("invalid short", "+0d"),
        ("negative short", "+-3d"),
    ];

    for (name, input) in cases {
        assert!(
            parse_until_at(input, now()).is_err(),
            "{name}: input {input:?} should error"
        );
    }
}

#[test]
fn parse_until_convenience_uses_now() {
    let got = parse_until("in 1 day").expect("in 1 day should parse");
    let delta = got - Utc::now();
    assert!(
        (delta - Duration::hours(24)).abs() < Duration::minutes(1),
        "in 1 day should land ~24h out, got {delta}"
    );
}

#[test]
fn parse_until_integration() {
    let reference = Utc::now();

    let got = parse_until("tomorrow").expect("tomorrow should parse");
    assert!(
        ((got - reference) - Duration::hours(24)).abs() < Duration::minutes(1),
        "tomorrow should be ~24h from now"
    );

    let got = parse_until("+1h").expect("+1h should parse");
    assert!(
        ((got - reference) - Duration::hours(1)).abs() < Duration::minutes(1),
        "+1h should be ~1h from now"
    );

    // ISO roundtrip: a future date parses back to itself.
    let future = (reference + Duration::hours(48))
        .format("%Y-%m-%d")
        .to_string();
    let got = parse_until(&future).expect("future ISO date should parse");
    assert_eq!(got.format("%Y-%m-%d").to_string(), future);

    // since and until are symmetric around now.
    let since = parse_since("1 day ago").expect("1 day ago should parse");
    let until = parse_until("in 1 day").expect("in 1 day should parse");
    assert!(
        ((until - since) - Duration::hours(48)).abs() < Duration::minutes(1),
        "since(-1d) to until(+1d) should span ~48h"
    );
}

// ---------------------------------------------------------------------------
// since — the port of Go's TestParseSince
// ---------------------------------------------------------------------------

#[test]
fn parse_since_table() {
    let cases: Vec<(&str, &str, DateTime<Utc>)> = vec![
        // git-style relative: "N <unit> ago"
        ("1 day ago", "1 day ago", minus_days(1)),
        ("3 days ago", "3 days ago", minus_days(3)),
        ("1 week ago", "1 week ago", minus_days(7)),
        ("2 weeks ago", "2 weeks ago", minus_days(14)),
        ("1 month ago", "1 month ago", minus_months(1)),
        ("6 months ago", "6 months ago", minus_months(6)),
        ("1 year ago", "1 year ago", minus_months(12)),
        ("2 years ago", "2 years ago", minus_months(24)),
        ("1 hour ago", "1 hour ago", now() - Duration::hours(1)),
        ("3 hours ago", "3 hours ago", now() - Duration::hours(3)),
        (
            "30 minutes ago",
            "30 minutes ago",
            now() - Duration::minutes(30),
        ),
        (
            "5 seconds ago",
            "5 seconds ago",
            now() - Duration::seconds(5),
        ),
        // named relative
        ("yesterday", "yesterday", minus_days(1)),
        // natural language relative dates
        ("last friday", "last friday", at(2026, 4, 17, 0, 0, 0)),
        // short relative
        ("7d", "7d", minus_days(7)),
        ("24h", "24h", now() - Duration::hours(24)),
        ("30m", "30m", now() - Duration::minutes(30)),
        ("2w", "2w", minus_days(14)),
        ("3M", "3M", minus_months(3)),
        ("1y", "1y", minus_months(12)),
        // ISO 8601
        ("date only", "2026-04-15", at(2026, 4, 15, 0, 0, 0)),
        (
            "datetime UTC",
            "2026-04-15T10:30:00Z",
            at(2026, 4, 15, 10, 30, 0),
        ),
        (
            "datetime offset",
            "2026-04-15T10:30:00+05:00",
            at(2026, 4, 15, 5, 30, 0),
        ),
        (
            "datetime negative offset",
            "2026-04-15T10:30:00-04:00",
            at(2026, 4, 15, 14, 30, 0),
        ),
        // whitespace tolerance
        ("leading/trailing spaces", "  3 days ago  ", minus_days(3)),
    ];

    for (name, input, expected) in cases {
        let got = parse_since_at(input, now())
            .unwrap_or_else(|e| panic!("{name}: input {input:?} errored: {e}"));
        assert_eq!(got, expected, "{name}: input {input:?}");
    }
}

/// The mirror of `parse_until_next_week_matches_go_exactly`: `"last week"`
/// is truncated to midnight too, so both directions match Go exactly.
#[test]
fn parse_since_last_week_matches_go_exactly() {
    let got = parse_since_at("last week", now()).unwrap();
    assert_eq!(got, at(2026, 4, 12, 0, 0, 0));
}

#[test]
fn parse_since_errors() {
    let cases = [
        ("empty", ""),
        ("garbage", "not a date"),
        ("negative number", "-3 days ago"),
        ("zero", "0 days ago"),
        ("missing unit", "3 ago"),
        ("unknown unit", "3 fortnights ago"),
    ];

    for (name, input) in cases {
        assert!(
            parse_since_at(input, now()).is_err(),
            "{name}: input {input:?} should error"
        );
    }
}

#[test]
fn parse_since_convenience_uses_now() {
    let got = parse_since("1 day ago").expect("1 day ago should parse");
    let delta = Utc::now() - got;
    assert!(
        (delta - Duration::hours(24)).abs() < Duration::minutes(1),
        "1 day ago should land ~24h back, got {delta}"
    );
}

// ---------------------------------------------------------------------------
// Trap-specific regression tests
// ---------------------------------------------------------------------------

/// `'m'` is MINUTES and `'M'` is MONTHS. Swapping these is the single
/// highest-risk transcription error in this port, so pin it from both
/// directions in both parsers.
#[test]
fn short_form_m_is_minutes_and_capital_m_is_months() {
    assert_eq!(
        parse_until_at("+3m", now()).unwrap(),
        now() + Duration::minutes(3),
        "+3m must be three MINUTES"
    );
    assert_eq!(
        parse_until_at("+3M", now()).unwrap(),
        plus_months(3),
        "+3M must be three MONTHS"
    );
    assert_eq!(
        parse_since_at("3m", now()).unwrap(),
        now() - Duration::minutes(3),
        "3m must be three MINUTES"
    );
    assert_eq!(
        parse_since_at("3M", now()).unwrap(),
        minus_months(3),
        "3M must be three MONTHS"
    );

    // The two must never coincide, or the assertions above prove nothing.
    assert_ne!(
        parse_until_at("+3m", now()).unwrap(),
        parse_until_at("+3M", now()).unwrap()
    );
}

/// Month arithmetic CLAMPS to the end of the target month rather than
/// normalising into the following one. This is the deliberate divergence from
/// Go's `time.AddDate`; see the module docs. Pinned so it cannot drift.
#[test]
fn month_arithmetic_clamps_it_does_not_normalise() {
    let jan31 = Utc.with_ymd_and_hms(2026, 1, 31, 12, 0, 0).unwrap();

    // Go's AddDate(0, 1, 0) would yield 2026-03-03; chrono clamps to Feb 28.
    assert_eq!(
        parse_until_at("in 1 month", jan31).unwrap(),
        Utc.with_ymd_and_hms(2026, 2, 28, 12, 0, 0).unwrap(),
        "Jan 31 + 1 month must clamp to Feb 28, not roll into March"
    );
    assert_eq!(
        parse_until_at("+1M", jan31).unwrap(),
        Utc.with_ymd_and_hms(2026, 2, 28, 12, 0, 0).unwrap()
    );

    // Backward direction clamps too: Mar 31 - 1 month = Feb 28.
    let mar31 = Utc.with_ymd_and_hms(2026, 3, 31, 12, 0, 0).unwrap();
    assert_eq!(
        parse_since_at("1 month ago", mar31).unwrap(),
        Utc.with_ymd_and_hms(2026, 2, 28, 12, 0, 0).unwrap(),
        "Mar 31 - 1 month must clamp to Feb 28"
    );

    // Leap day + 1 year clamps to Feb 28 of the common year.
    let leap = Utc.with_ymd_and_hms(2024, 2, 29, 12, 0, 0).unwrap();
    assert_eq!(
        parse_until_at("in 1 year", leap).unwrap(),
        Utc.with_ymd_and_hms(2025, 2, 28, 12, 0, 0).unwrap(),
        "2024-02-29 + 1 year must clamp to 2025-02-28"
    );
}

/// A weekday name resolves to the NEXT occurrence, so naming the current
/// weekday advances a full week rather than returning the reference instant.
#[test]
fn weekday_today_means_next_week_not_today() {
    // now() is a Sunday.
    assert_eq!(parse_until_at("sunday", now()).unwrap(), plus_days(7));
    assert_ne!(parse_until_at("sunday", now()).unwrap(), now());
}

/// Unparseable phrases must error rather than silently echoing the reference
/// instant. `interim` returns `now` unchanged for inputs like `"this week"`,
/// exactly as `go-naturaldate` does, so the equality guard is load-bearing.
#[test]
fn unparseable_phrases_error_rather_than_returning_now() {
    for input in ["this week", "not a date", "some nonsense here"] {
        let result = parse_until_at(input, now());
        assert!(result.is_err(), "{input:?} should error, got {result:?}");
    }
    for input in ["this week", "not a date"] {
        let result = parse_since_at(input, now());
        assert!(result.is_err(), "{input:?} should error, got {result:?}");
    }
}

/// Unit words are singular-ised by trimming one trailing `"s"`, so both
/// spellings resolve identically.
#[test]
fn plural_and_singular_units_agree() {
    assert_eq!(
        parse_until_at("in 2 day", now()).unwrap(),
        parse_until_at("in 2 days", now()).unwrap()
    );
    assert_eq!(
        parse_since_at("2 week ago", now()).unwrap(),
        parse_since_at("2 weeks ago", now()).unwrap()
    );
}
