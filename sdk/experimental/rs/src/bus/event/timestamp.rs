//! Timestamp grammar and formatting for [`Event`](super::Event).

use std::fmt;
use std::time::{SystemTime, UNIX_EPOCH};

/// A timestamp that is not RFC 3339.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct TimestampError;

impl fmt::Display for TimestampError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("invalid RFC 3339 timestamp")
    }
}

impl std::error::Error for TimestampError {}

/// Validates `s` as an event timestamp and returns the form Go's
/// `time.Time` re-encodes it as.
///
/// The grammar is `YYYY-MM-DD`, `T`, `hh:mm:ss`, an optional fraction,
/// then `Z` or `±hh:mm`: a four-digit year, a month and day that exist
/// (leap years honoured), hour 00-23, minute 00-59, second 00-59 (no leap
/// second), offset hour 00-23 and offset minute 00-59, with `T` and `Z`
/// uppercase. The fraction takes `.` or `,` and at least one digit.
///
/// The result drops trailing fraction zeros (and the fraction when it is
/// all zeros), truncates to nine digits, uses `.` and writes a zero
/// offset as `Z`; any other offset is kept.
pub fn canonical_timestamp(s: &str) -> Result<String, TimestampError> {
    let b = s.as_bytes();
    if b.len() < 20 {
        return Err(TimestampError);
    }
    let num = |from: usize, len: usize| -> Option<u32> {
        b[from..from + len].iter().try_fold(0u32, |acc, &c| {
            c.is_ascii_digit().then(|| acc * 10 + u32::from(c - b'0'))
        })
    };
    let (Some(y), Some(mo), Some(d), Some(h), Some(mi), Some(sec)) = (
        num(0, 4),
        num(5, 2),
        num(8, 2),
        num(11, 2),
        num(14, 2),
        num(17, 2),
    ) else {
        return Err(TimestampError);
    };
    let separators =
        b[4] == b'-' && b[7] == b'-' && b[10] == b'T' && b[13] == b':' && b[16] == b':';
    if !separators || !(1..=12).contains(&mo) || d < 1 || d > days_in_month(y, mo) {
        return Err(TimestampError);
    }
    if h > 23 || mi > 59 || sec > 59 {
        return Err(TimestampError);
    }
    let mut i = 19;
    let mut out = s[..19].to_string();
    if b[i] == b'.' || b[i] == b',' {
        let digits = b[i + 1..].iter().take_while(|c| c.is_ascii_digit()).count();
        if digits == 0 {
            return Err(TimestampError);
        }
        let frac = s[i + 1..i + 1 + digits.min(9)].trim_end_matches('0');
        if !frac.is_empty() {
            out.push('.');
            out.push_str(frac);
        }
        i += 1 + digits;
    }
    match &b[i..] {
        b"Z" => out.push('Z'),
        [sign @ (b'+' | b'-'), _, _, b':', _, _] if i + 6 == b.len() => {
            let (Some(oh), Some(om)) = (num(i + 1, 2), num(i + 4, 2)) else {
                return Err(TimestampError);
            };
            if oh > 23 || om > 59 {
                return Err(TimestampError);
            }
            if oh == 0 && om == 0 {
                out.push('Z');
            } else {
                out.push(*sign as char);
                out.push_str(&s[i + 1..]);
            }
        }
        _ => return Err(TimestampError),
    }
    Ok(out)
}

fn days_in_month(year: u32, month: u32) -> u32 {
    match month {
        2 if year.is_multiple_of(4) && (!year.is_multiple_of(100) || year.is_multiple_of(400)) => {
            29
        }
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    }
}

/// Formats `at` as `YYYY-MM-DDTHH:MM:SS[.fraction]Z` in UTC with
/// nanosecond precision and trailing zeros trimmed, the form
/// [`canonical_timestamp`] returns.
///
/// Hand-rolled rather than pulling in `chrono`/`time`: the bus needs one
/// UTC timestamp format and nothing else from a date library, and the
/// `bus` feature is meant to stay dependency-light.
pub(super) fn format_rfc3339(at: SystemTime) -> String {
    // Year 9999 is the last the format can carry.
    const MAX_SECS: u64 = 253_402_300_799;
    let d = at.duration_since(UNIX_EPOCH).unwrap_or_default();
    if d.as_secs() > MAX_SECS {
        return format_epoch(MAX_SECS, 999_999_999);
    }
    format_epoch(d.as_secs(), d.subsec_nanos())
}

fn format_epoch(secs: u64, nanos: u32) -> String {
    let days = (secs / 86_400) as i64;
    let secs_of_day = secs % 86_400;
    let (y, m, d) = civil_from_days(days);
    let mut out = format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}",
        secs_of_day / 3600,
        (secs_of_day % 3600) / 60,
        secs_of_day % 60,
    );
    if nanos != 0 {
        out.push('.');
        out.push_str(format!("{nanos:09}").trim_end_matches('0'));
    }
    out.push('Z');
    out
}

/// Howard Hinnant's `civil_from_days` algorithm: days since the Unix epoch
/// to a proleptic Gregorian (year, month, day).
fn civil_from_days(z: i64) -> (i64, u32, u32) {
    let z = z + 719_468;
    let era = if z >= 0 { z } else { z - 146_096 } / 146_097;
    let doe = (z - era * 146_097) as u64;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let y = yoe as i64 + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
    (if m <= 2 { y + 1 } else { y }, m, d)
}
