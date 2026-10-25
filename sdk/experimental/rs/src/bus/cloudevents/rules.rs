//! CloudEvents attribute rules: time grammar, base64, media types and
//! extension names. Mirrors `go/runtime/bus/cloudevents_rules.go`.

/// An instant: seconds since the Unix epoch (UTC) and nanoseconds.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) struct Instant {
    secs: i64,
    nanos: u32,
}

/// Go's zero `time.Time`, 0001-01-01T00:00:00Z: "unset".
const ZERO_SECS: i64 = -62_135_596_800;

impl Instant {
    pub(super) fn is_zero(self) -> bool {
        self.secs == ZERO_SECS && self.nanos == 0
    }

    /// Whether the UTC year is 0000-9999, the range the grammar carries.
    pub(super) fn encodable(self) -> bool {
        (0..=9999).contains(&civil_from_days(self.secs.div_euclid(86_400)).0)
    }

    /// `YYYY-MM-DDThh:mm:ss[.frac]Z` in UTC, trailing fraction zeros
    /// trimmed: Go's `time.RFC3339Nano` on a UTC time. Only called on
    /// [`Instant::encodable`] instants.
    pub(super) fn format_utc(self) -> String {
        let (y, m, d) = civil_from_days(self.secs.div_euclid(86_400));
        let s = self.secs.rem_euclid(86_400);
        let mut out = format!(
            "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}",
            s / 3600,
            s % 3600 / 60,
            s % 60
        );
        if self.nanos != 0 {
            out.push('.');
            out.push_str(format!("{:09}", self.nanos).trim_end_matches('0'));
        }
        out.push('Z');
        out
    }
}

/// Parses a CloudEvents `time` against RFC 3339 §5.6 date-time with kit's
/// rulings:
///
/// ```text
/// YYYY-MM-DD ("T"|"t") hh:mm:ss ["." 1-9 digits] ("Z"|"z"|("+"|"-")hh:mm)
/// ```
///
/// Four-digit year, month 01-12, a day that exists in that month, hour
/// 00-23, minute 00-59, second 00-59 (no leap second), offset hour 00-23
/// and offset minute 00-59. The year range of the UTC instant is checked
/// separately ([`Instant::encodable`]), as Go does in `Validate`.
pub(super) fn parse_time(s: &str) -> Option<Instant> {
    let b = s.as_bytes();
    if b.len() < 20 {
        return None;
    }
    let num = |from: usize, len: usize| -> Option<i64> {
        b.get(from..from + len)?.iter().try_fold(0i64, |acc, &c| {
            c.is_ascii_digit().then(|| acc * 10 + i64::from(c - b'0'))
        })
    };
    let (y, mo, d, h, mi, sec) = (
        num(0, 4)?,
        num(5, 2)?,
        num(8, 2)?,
        num(11, 2)?,
        num(14, 2)?,
        num(17, 2)?,
    );
    let separators = b[4] == b'-'
        && b[7] == b'-'
        && matches!(b[10], b'T' | b't')
        && b[13] == b':'
        && b[16] == b':';
    if !separators || !(1..=12).contains(&mo) || d < 1 || d > days_in_month(y, mo) {
        return None;
    }
    if h > 23 || mi > 59 || sec > 59 {
        return None;
    }
    let mut i = 19;
    let mut nanos = 0u32;
    if b[i] == b'.' {
        let digits = b[i + 1..].iter().take_while(|c| c.is_ascii_digit()).count();
        if !(1..=9).contains(&digits) {
            return None;
        }
        let frac = &s[i + 1..i + 1 + digits];
        nanos = format!("{frac:0<9}").parse().ok()?;
        i += 1 + digits;
    }
    let offset = match &b[i..] {
        b"Z" | b"z" => 0,
        [sign @ (b'+' | b'-'), _, _, b':', _, _] => {
            let (oh, om) = (num(i + 1, 2)?, num(i + 4, 2)?);
            if oh > 23 || om > 59 {
                return None;
            }
            let off = oh * 3600 + om * 60;
            if *sign == b'-' {
                -off
            } else {
                off
            }
        }
        _ => return None,
    };
    let secs = days_from_civil(y, mo, d) * 86_400 + h * 3600 + mi * 60 + sec - offset;
    Some(Instant { secs, nanos })
}

/// `s` with `t` and `z` uppercased, for an already-parsed time: the form
/// Go's `time.Parse` accepts, ready for `canonical_timestamp`.
pub(super) fn upper_time(s: &str) -> String {
    s.to_ascii_uppercase()
}

fn days_in_month(year: i64, month: i64) -> i64 {
    let leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
    match month {
        2 if leap => 29,
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    }
}

/// Howard Hinnant's `days_from_civil`: a proleptic Gregorian date to
/// days since the Unix epoch.
fn days_from_civil(y: i64, m: i64, d: i64) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let mp = (m + 9) % 12;
    let doy = (153 * mp + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146_097 + doe - 719_468
}

/// The inverse of [`days_from_civil`].
fn civil_from_days(z: i64) -> (i64, i64, i64) {
    let z = z + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    (yoe + era * 400 + i64::from(m <= 2), m, d)
}

/// Whether `ct` names JSON per the CloudEvents JSON format §3.1: subtype
/// `json` or a `+json` suffix, parameters ignored, case-insensitive.
pub(super) fn is_json_media_type(ct: &str) -> bool {
    let ct = ct.split(';').next().unwrap_or_default();
    let ct = ct.trim().to_ascii_lowercase();
    match ct.find('/') {
        Some(slash) if slash > 0 => {
            let sub = &ct[slash + 1..];
            sub == "json" || sub.ends_with("+json")
        }
        _ => false,
    }
}

/// Whether `name` is lowercase ASCII letters and digits only. CloudEvents
/// recommends at most 20 characters; longer names are accepted.
pub(super) fn valid_extension_name(name: &str) -> bool {
    !name.is_empty()
        && name
            .bytes()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit())
}

/// Whether `s` is a JSON number with no fraction or exponent:
/// `-?(0|[1-9][0-9]*)`.
pub(super) fn is_json_integer(s: &str) -> bool {
    let s = s.strip_prefix('-').unwrap_or(s);
    !s.is_empty() && !(s.starts_with('0') && s.len() > 1) && s.bytes().all(|c| c.is_ascii_digit())
}

const B64: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/// RFC 4648 standard base64 with padding.
pub(super) fn encode_base64(data: &[u8]) -> String {
    let mut out = String::with_capacity(data.len().div_ceil(3) * 4);
    for chunk in data.chunks(3) {
        let n = chunk
            .iter()
            .enumerate()
            .fold(0u32, |acc, (i, &b)| acc | u32::from(b) << (16 - 8 * i));
        for i in 0..4 {
            if i <= chunk.len() {
                out.push(B64[(n >> (18 - 6 * i) & 63) as usize] as char);
            } else {
                out.push('=');
            }
        }
    }
    out
}

/// Decodes `data_base64`: RFC 4648 standard alphabet, padding required,
/// canonical trailing bits, and nothing else (no CR or LF anywhere), as
/// Go's `base64.StdEncoding.Strict()` behind a line-break check.
pub(super) fn decode_base64(s: &str) -> Option<Vec<u8>> {
    let b = s.as_bytes();
    if !b.len().is_multiple_of(4) {
        return None;
    }
    let value = |c: u8| B64.iter().position(|&a| a == c).map(|v| v as u32);
    let mut out = Vec::with_capacity(b.len() / 4 * 3);
    let quads = b.len() / 4;
    for (q, quad) in b.chunks(4).enumerate() {
        let pad = quad.iter().rev().take_while(|&&c| c == b'=').count();
        if pad > 2 || (pad > 0 && q + 1 != quads) {
            return None;
        }
        let mut n = 0u32;
        for &c in &quad[..4 - pad] {
            n = n << 6 | value(c)?;
        }
        n <<= 6 * pad as u32;
        let bytes = [(n >> 16) as u8, (n >> 8) as u8, n as u8];
        let keep = 3 - pad;
        // Canonical: the bits a padded quad does not carry must be zero.
        if bytes[keep..].iter().any(|&x| x != 0) {
            return None;
        }
        out.extend_from_slice(&bytes[..keep]);
    }
    Some(out)
}

/// `s` quoted as Go's `%q` (`strconv.Quote`) prints it, for the
/// `unsupported value` text. Control characters are escaped; other
/// characters are kept as they are.
pub(super) fn go_quote(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\u{7}' => out.push_str("\\a"),
            '\u{8}' => out.push_str("\\b"),
            '\u{c}' => out.push_str("\\f"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\u{b}' => out.push_str("\\v"),
            c if (c as u32) < 0x20 || c == '\u{7f}' => {
                out.push_str(&format!("\\x{:02x}", c as u32));
            }
            c if c.is_control() => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn civil_round_trip() {
        for days in [-719_528, -1, 0, 1, 19_000, 2_932_896] {
            let (y, m, d) = civil_from_days(days);
            assert_eq!(days_from_civil(y, m, d), days);
        }
        assert_eq!(days_from_civil(1, 1, 1) * 86_400, ZERO_SECS);
    }

    #[test]
    fn year_bounds() {
        let t = |s| parse_time(s).unwrap();
        assert!(t("0000-01-01T00:00:00Z").encodable());
        assert!(!t("0000-01-01T00:00:00+01:00").encodable());
        assert!(t("9999-12-31T23:59:59Z").encodable());
        assert!(!t("9999-12-31T23:00:00-02:00").encodable());
        assert_eq!(
            t("0000-01-01T00:00:00Z").format_utc(),
            "0000-01-01T00:00:00Z"
        );
    }

    #[test]
    fn base64_canonical() {
        assert_eq!(decode_base64("AQ=="), Some(vec![1]));
        assert_eq!(decode_base64("AR=="), None);
        assert_eq!(decode_base64("AAE="), Some(vec![0, 1]));
        assert_eq!(decode_base64("AAF="), None);
        assert_eq!(decode_base64(""), Some(vec![]));
        assert_eq!(encode_base64(&[0xde, 0xad]), "3q0=");
        assert_eq!(encode_base64(b"hi!"), "aGkh");
    }

    #[test]
    fn quote() {
        assert_eq!(go_quote("0.3"), r#""0.3""#);
        assert_eq!(go_quote("a\"\\\n\u{1}\u{85}é"), r#""a\"\\\n\x01\u0085é""#);
    }
}
