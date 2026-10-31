//! RFC 3339 at whole seconds, UTC, as Go's `time.Parse(time.RFC3339)`
//! and `t.UTC().Format(time.RFC3339)` read and write grant timestamps.
//!
//! Hand-rolled like the bus and sqlstore stamps: one format, no date
//! library. Validation is the bus grammar ([`canonical_timestamp`]),
//! which is Go's: four-digit year, real calendar days, `T` and `Z`
//! uppercase, an optional fraction, offsets up to 23:59.

use std::time::{Duration, SystemTime, UNIX_EPOCH};

use crate::bus::event::canonical_timestamp;

/// Seconds since the Unix epoch (negative before it).
pub(crate) fn unix_secs(t: SystemTime) -> i64 {
    match t.duration_since(UNIX_EPOCH) {
        Ok(d) => d.as_secs() as i64,
        Err(e) => {
            let d = e.duration();
            -(d.as_secs() as i64) - i64::from(d.subsec_nanos() > 0)
        }
    }
}

/// The instant `secs` seconds after the epoch.
pub(crate) fn from_unix_secs(secs: i64) -> SystemTime {
    if secs >= 0 {
        UNIX_EPOCH + Duration::from_secs(secs as u64)
    } else {
        UNIX_EPOCH - Duration::from_secs(secs.unsigned_abs())
    }
}

/// `t` truncated to the second, as Go's `Truncate(time.Second)`.
pub(crate) fn truncate(t: SystemTime) -> SystemTime {
    from_unix_secs(unix_secs(t))
}

/// Formats `t` as `YYYY-MM-DDTHH:MM:SSZ` (fraction dropped).
pub(crate) fn format_rfc3339(t: SystemTime) -> String {
    let secs = unix_secs(t);
    let days = secs.div_euclid(86_400);
    let rem = secs.rem_euclid(86_400);
    let (y, mo, d) = civil_from_days(days);
    format!(
        "{y:04}-{mo:02}-{d:02}T{:02}:{:02}:{:02}Z",
        rem / 3600,
        (rem % 3600) / 60,
        rem % 60
    )
}

/// Parses an RFC 3339 timestamp; `None` when Go's parser would refuse
/// it. A fraction is kept to the nanosecond.
pub(crate) fn parse_rfc3339(s: &str) -> Option<SystemTime> {
    let c = canonical_timestamp(s).ok()?;
    let num = |r: std::ops::Range<usize>| c.get(r)?.parse::<i64>().ok();
    let (y, mo, d) = (num(0..4)?, num(5..7)?, num(8..10)?);
    let (h, mi, sec) = (num(11..13)?, num(14..16)?, num(17..19)?);
    let mut secs = days_from_civil(y, mo, d) * 86_400 + h * 3600 + mi * 60 + sec;
    let mut rest = &c[19..];
    let mut nanos = 0u32;
    if let Some(frac) = rest.strip_prefix('.') {
        let digits = frac.bytes().take_while(u8::is_ascii_digit).count();
        let padded = format!("{:0<9}", &frac[..digits]);
        nanos = padded.parse().ok()?;
        rest = &frac[digits..];
    }
    if let Some(off) = rest.strip_prefix(['+', '-']) {
        let oh: i64 = off.get(0..2)?.parse().ok()?;
        let om: i64 = off.get(3..5)?.parse().ok()?;
        let offset = oh * 3600 + om * 60;
        secs += if rest.starts_with('+') {
            -offset
        } else {
            offset
        };
    }
    Some(from_unix_secs(secs) + Duration::from_nanos(u64::from(nanos)))
}

fn days_from_civil(y: i64, m: i64, d: i64) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = if y >= 0 { y } else { y - 399 } / 400;
    let yoe = y - era * 400;
    let mp = (m + 9) % 12;
    let doy = (153 * mp + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146_097 + doe - 719_468
}

fn civil_from_days(z: i64) -> (i64, i64, i64) {
    let z = z + 719_468;
    let era = if z >= 0 { z } else { z - 146_096 } / 146_097;
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    (if m <= 2 { y + 1 } else { y }, m, d)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trips_utc_seconds() {
        for s in [
            "2026-03-01T00:00:00Z",
            "1970-01-01T00:00:00Z",
            "0001-01-01T00:00:00Z",
            "2024-02-29T23:59:59Z",
        ] {
            assert_eq!(format_rfc3339(parse_rfc3339(s).unwrap()), s);
        }
    }

    #[test]
    fn offsets_and_fractions_normalise_to_utc() {
        let t = parse_rfc3339("2026-03-01T02:00:00.75+02:00").unwrap();
        assert_eq!(format_rfc3339(t), "2026-03-01T00:00:00Z");
        assert_eq!(format_rfc3339(truncate(t)), "2026-03-01T00:00:00Z");
    }

    #[test]
    fn refuses_what_go_refuses() {
        for s in [
            "",
            "2026-02-30T00:00:00Z",
            "2026-03-01t00:00:00Z",
            "2026-03-01T00:00:00",
            "2026-03-01T24:00:00Z",
            "2026-03-01T00:00:00z",
        ] {
            assert!(parse_rfc3339(s).is_none(), "{s}");
        }
    }
}
