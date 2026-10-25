//! Plain-scalar resolution with gopkg.in/yaml.v3's rules, so a value
//! types the same in Rust as in Go: YAML 1.2 core booleans and nulls,
//! `0x`/`0o`/`0b`/legacy-octal and underscored integers, `.inf`/`.nan`,
//! and timestamps detected but kept as text.

/// Short tags.
pub(crate) const NULL_TAG: &str = "!!null";
pub(crate) const BOOL_TAG: &str = "!!bool";
pub(crate) const STR_TAG: &str = "!!str";
pub(crate) const INT_TAG: &str = "!!int";
pub(crate) const FLOAT_TAG: &str = "!!float";
pub(crate) const TIMESTAMP_TAG: &str = "!!timestamp";
pub(crate) const SEQ_TAG: &str = "!!seq";
pub(crate) const MAP_TAG: &str = "!!map";
pub(crate) const BINARY_TAG: &str = "!!binary";
pub(crate) const MERGE_TAG: &str = "!!merge";

const LONG_TAG_PREFIX: &str = "tag:yaml.org,2002:";

/// A resolved scalar.
#[derive(Clone, Debug, PartialEq)]
pub(crate) enum Scalar {
    Null,
    Bool(bool),
    /// Fits Go's `int` (64-bit).
    Int(i64),
    /// Larger than `i64::MAX`; Go resolves these to `uint64`.
    Uint(u64),
    Float(f64),
    Str(String),
    /// Timestamp-shaped text. Go decodes it to `time.Time`; the Rust
    /// port keeps the source text.
    Timestamp(String),
    Merge,
}

/// `tag:yaml.org,2002:x` → `!!x`; anything else unchanged.
pub(crate) fn short_tag(tag: &str) -> String {
    match tag.strip_prefix(LONG_TAG_PREFIX) {
        Some(rest) => format!("!!{rest}"),
        None => tag.to_string(),
    }
}

fn resolvable_tag(tag: &str) -> bool {
    matches!(
        tag,
        "" | STR_TAG | BOOL_TAG | INT_TAG | FLOAT_TAG | NULL_TAG | TIMESTAMP_TAG
    )
}

/// yaml.v3 `resolve`: returns the resolved short tag and value, or the
/// error text for an explicit tag the value cannot satisfy
/// (`cannot decode !!str `abc` as a !!int`).
pub(crate) fn resolve(tag: &str, input: &str) -> Result<(String, Scalar), String> {
    let tag = short_tag(tag);
    if !resolvable_tag(&tag) {
        return Ok((tag, Scalar::Str(input.to_string())));
    }
    let (rtag, out) = resolve_plain(&tag, input);
    if tag.is_empty() || tag == rtag || tag == STR_TAG || tag == BINARY_TAG {
        return Ok((rtag.to_string(), out));
    }
    if tag == FLOAT_TAG && rtag == INT_TAG {
        let f = match out {
            Scalar::Int(i) => i as f64,
            // Go converts only int/int64; a uint64 falls to the error.
            _ => return Err(fail(rtag, input, &tag)),
        };
        return Ok((FLOAT_TAG.to_string(), Scalar::Float(f)));
    }
    Err(fail(rtag, input, &tag))
}

fn fail(rtag: &str, input: &str, tag: &str) -> String {
    format!("cannot decode {rtag} `{input}` as a {tag}")
}

fn resolve_plain(tag: &str, input: &str) -> (&'static str, Scalar) {
    let hint = match input.as_bytes().first() {
        None => b'N',
        Some(b'+' | b'-') => b'S',
        Some(b'0'..=b'9') => b'D',
        Some(b'y' | b'Y' | b'n' | b'N' | b't' | b'T' | b'f' | b'F' | b'o' | b'O' | b'~') => b'M',
        Some(b'.') => b'.',
        Some(_) => 0,
    };
    if hint == 0 || tag == STR_TAG || tag == BINARY_TAG {
        return (STR_TAG, Scalar::Str(input.to_string()));
    }
    if let Some(hit) = resolve_map(input) {
        return hit;
    }
    match hint {
        b'.' => {
            if let Some(f) = go_parse_float(input) {
                return (FLOAT_TAG, Scalar::Float(f));
            }
        }
        b'D' | b'S' => {
            if (tag.is_empty() || tag == TIMESTAMP_TAG) && is_timestamp(input) {
                return (TIMESTAMP_TAG, Scalar::Timestamp(input.to_string()));
            }
            let plain: String = input.chars().filter(|&c| c != '_').collect();
            if let Some(i) = go_parse_int(&plain) {
                return (INT_TAG, Scalar::Int(i));
            }
            if let Some(u) = go_parse_uint(&plain) {
                return (INT_TAG, Scalar::Uint(u));
            }
            if yaml_style_float(&plain) {
                if let Some(f) = go_parse_float(&plain) {
                    return (FLOAT_TAG, Scalar::Float(f));
                }
            }
            if let Some(hit) = prefixed_int(&plain) {
                return hit;
            }
        }
        _ => {}
    }
    (STR_TAG, Scalar::Str(input.to_string()))
}

fn resolve_map(s: &str) -> Option<(&'static str, Scalar)> {
    Some(match s {
        "true" | "True" | "TRUE" => (BOOL_TAG, Scalar::Bool(true)),
        "false" | "False" | "FALSE" => (BOOL_TAG, Scalar::Bool(false)),
        "" | "~" | "null" | "Null" | "NULL" => (NULL_TAG, Scalar::Null),
        ".nan" | ".NaN" | ".NAN" => (FLOAT_TAG, Scalar::Float(f64::NAN)),
        ".inf" | ".Inf" | ".INF" | "+.inf" | "+.Inf" | "+.INF" => {
            (FLOAT_TAG, Scalar::Float(f64::INFINITY))
        }
        "-.inf" | "-.Inf" | "-.INF" => (FLOAT_TAG, Scalar::Float(f64::NEG_INFINITY)),
        "<<" => (MERGE_TAG, Scalar::Merge),
        _ => return None,
    })
}

/// The `0b`/`0o` spellings strconv's base 0 misses after the sign.
fn prefixed_int(plain: &str) -> Option<(&'static str, Scalar)> {
    let (neg, body, radix) = [
        ("0b", false, 2),
        ("-0b", true, 2),
        ("0o", false, 8),
        ("-0o", true, 8),
    ]
    .into_iter()
    .find_map(|(p, neg, radix)| plain.strip_prefix(p).map(|r| (neg, r, radix)))?;
    let mag = parse_digits(body, radix)?;
    if neg {
        let i = if mag == 1u64 << 63 {
            i64::MIN
        } else {
            i64::try_from(mag).ok()?.checked_neg()?
        };
        return Some((INT_TAG, Scalar::Int(i)));
    }
    Some(match i64::try_from(mag) {
        Ok(i) => (INT_TAG, Scalar::Int(i)),
        Err(_) => (INT_TAG, Scalar::Uint(mag)),
    })
}

/// `^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$`
fn yaml_style_float(s: &str) -> bool {
    let b = s.as_bytes();
    let mut i = 0;
    if matches!(b.first(), Some(b'+' | b'-')) {
        i += 1;
    }
    let digits = |i: &mut usize| {
        let start = *i;
        while *i < b.len() && b[*i].is_ascii_digit() {
            *i += 1;
        }
        *i - start
    };
    if b.get(i) == Some(&b'.') {
        i += 1;
        if digits(&mut i) == 0 {
            return false;
        }
    } else {
        if digits(&mut i) == 0 {
            return false;
        }
        if b.get(i) == Some(&b'.') {
            i += 1;
            digits(&mut i);
        }
    }
    if matches!(b.get(i), Some(b'e' | b'E')) {
        i += 1;
        if matches!(b.get(i), Some(b'+' | b'-')) {
            i += 1;
        }
        if digits(&mut i) == 0 {
            return false;
        }
    }
    i == b.len()
}

fn parse_digits(s: &str, radix: u32) -> Option<u64> {
    if s.is_empty() {
        return None;
    }
    let mut n: u64 = 0;
    for c in s.chars() {
        let d = c.to_digit(radix)?;
        n = n.checked_mul(u64::from(radix))?.checked_add(u64::from(d))?;
    }
    Some(n)
}

/// Go `strconv.ParseUint(s, 0, 64)` without underscore support (callers
/// strip them first).
pub(crate) fn go_parse_uint(s: &str) -> Option<u64> {
    let b = s.as_bytes();
    if b.is_empty() {
        return None;
    }
    if b[0] == b'0' {
        if b.len() >= 3 {
            match b[1].to_ascii_lowercase() {
                b'b' => return parse_digits(&s[2..], 2),
                b'o' => return parse_digits(&s[2..], 8),
                b'x' => return parse_digits(&s[2..], 16),
                _ => {}
            }
        }
        if b.len() == 1 {
            return Some(0);
        }
        return parse_digits(&s[1..], 8);
    }
    parse_digits(s, 10)
}

/// Go `strconv.ParseInt(s, 0, 64)` without underscore support.
pub(crate) fn go_parse_int(s: &str) -> Option<i64> {
    let (neg, body) = match s.as_bytes().first()? {
        b'+' => (false, &s[1..]),
        b'-' => (true, &s[1..]),
        _ => (false, s),
    };
    let mag = go_parse_uint(body)?;
    if neg {
        if mag == 1u64 << 63 {
            return Some(i64::MIN);
        }
        return i64::try_from(mag).ok().map(|v| -v);
    }
    i64::try_from(mag).ok()
}

/// Go `strconv.ParseFloat(s, 64)` for decimal spellings: out-of-range
/// values fail (Go reports ErrRange), so callers fall through to string.
pub(crate) fn go_parse_float(s: &str) -> Option<f64> {
    let b = s.as_bytes();
    // Go accepts inf/infinity/nan and hex floats; YAML never routes
    // those here (the hint table and regex exclude them), and Rust's
    // parser would accept spellings Go rejects, so refuse letters other
    // than the exponent marker.
    if !b
        .iter()
        .all(|c| c.is_ascii_digit() || matches!(c, b'+' | b'-' | b'.' | b'e' | b'E'))
    {
        return None;
    }
    let f: f64 = s.parse().ok()?;
    if f.is_infinite() {
        return None;
    }
    Some(f)
}

/// Go `strconv.FormatFloat(f, 'g', -1, 64)`, with yaml.v3's
/// `.inf`/`-.inf`/`.nan` spellings when `yaml` is set.
pub(crate) fn format_float(f: f64, yaml: bool) -> String {
    if f.is_nan() {
        return if yaml { ".nan".into() } else { "NaN".into() };
    }
    if f.is_infinite() {
        return match (f > 0.0, yaml) {
            (true, true) => ".inf".into(),
            (false, true) => "-.inf".into(),
            (true, false) => "+Inf".into(),
            (false, false) => "-Inf".into(),
        };
    }
    if f == 0.0 {
        return if f.is_sign_negative() {
            "-0".into()
        } else {
            "0".into()
        };
    }
    let sci = format!("{:e}", f.abs());
    let (mant, exp) = sci.split_once('e').expect("{:e} has an exponent");
    let digits: String = mant.chars().filter(|c| *c != '.').collect();
    let exp: i32 = exp.parse().expect("exponent is an integer");
    let nd = digits.len() as i32;
    let mut out = String::new();
    if f < 0.0 {
        out.push('-');
    }
    if !(-4..6).contains(&exp) {
        out.push_str(&digits[..1]);
        if nd > 1 {
            out.push('.');
            out.push_str(&digits[1..]);
        }
        out.push('e');
        out.push(if exp < 0 { '-' } else { '+' });
        let a = exp.unsigned_abs();
        if a < 10 {
            out.push('0');
        }
        out.push_str(&a.to_string());
        return out;
    }
    let dp = exp + 1;
    if dp <= 0 {
        out.push_str("0.");
        for _ in 0..(-dp) {
            out.push('0');
        }
        out.push_str(&digits);
    } else if dp >= nd {
        out.push_str(&digits);
        for _ in 0..(dp - nd) {
            out.push('0');
        }
    } else {
        out.push_str(&digits[..dp as usize]);
        out.push('.');
        out.push_str(&digits[dp as usize..]);
    }
    out
}

/// yaml.v3 `parseTimestamp`: four digits and a dash, then one of the
/// accepted layouts (RFC 3339 with short date fields, space-separated
/// without a zone, or date only), with Go's range checks.
pub(crate) fn is_timestamp(s: &str) -> bool {
    let b = s.as_bytes();
    if b.len() < 5 || !b[..4].iter().all(u8::is_ascii_digit) || b[4] != b'-' {
        return false;
    }
    let mut p = Cursor { b, i: 0 };
    let Some(year) = p.num(4, 4) else {
        return false;
    };
    if !p.eat(b'-') {
        return false;
    }
    let Some(month) = p.num(1, 2) else {
        return false;
    };
    if !p.eat(b'-') {
        return false;
    }
    let Some(day) = p.num(1, 2) else { return false };
    if !(1..=12).contains(&month) || day < 1 || day > days_in(month, year) {
        return false;
    }
    if p.done() {
        return true;
    }
    let zoned = match b[p.i] {
        b'T' | b't' => true,
        b' ' => false,
        _ => return false,
    };
    p.i += 1;
    let (Some(h), true, Some(m), true, Some(sec)) = (
        p.num(1, 2),
        p.eat(b':'),
        p.num(1, 2),
        p.eat(b':'),
        p.num(1, 2),
    ) else {
        return false;
    };
    if h > 23 || m > 59 || sec > 59 {
        return false;
    }
    if p.eat(b'.') && p.num(1, 9).is_none() {
        return false;
    }
    if !zoned {
        return p.done();
    }
    if p.eat(b'Z') {
        return p.done();
    }
    if !(p.eat(b'+') || p.eat(b'-')) {
        return false;
    }
    let (Some(zh), true, Some(zm)) = (p.num(2, 2), p.eat(b':'), p.num(2, 2)) else {
        return false;
    };
    zh <= 23 && zm <= 59 && p.done()
}

struct Cursor<'a> {
    b: &'a [u8],
    i: usize,
}

impl Cursor<'_> {
    fn num(&mut self, min: usize, max: usize) -> Option<u32> {
        let start = self.i;
        while self.i < self.b.len() && self.i - start < max && self.b[self.i].is_ascii_digit() {
            self.i += 1;
        }
        if self.i - start < min {
            return None;
        }
        std::str::from_utf8(&self.b[start..self.i])
            .ok()?
            .parse()
            .ok()
    }
    fn eat(&mut self, c: u8) -> bool {
        if self.b.get(self.i) == Some(&c) {
            self.i += 1;
            true
        } else {
            false
        }
    }
    fn done(&self) -> bool {
        self.i == self.b.len()
    }
}

fn days_in(month: u32, year: u32) -> u32 {
    match month {
        2 if (year.is_multiple_of(4) && !year.is_multiple_of(100)) || year.is_multiple_of(400) => {
            29
        }
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    }
}

/// yaml.v3 `isOldBool`: YAML 1.1 booleans that v3 reads as strings.
pub(crate) fn is_old_bool(s: &str) -> bool {
    matches!(
        s,
        "y" | "Y"
            | "yes"
            | "Yes"
            | "YES"
            | "on"
            | "On"
            | "ON"
            | "n"
            | "N"
            | "no"
            | "No"
            | "NO"
            | "off"
            | "Off"
            | "OFF"
    )
}

/// yaml.v3 `isBase60Float`:
/// `^[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+(?:\.[0-9_]*)?$`.
pub(crate) fn is_base60_float(s: &str) -> bool {
    let b = s.as_bytes();
    let mut i = 0;
    if matches!(b.first(), Some(b'+' | b'-')) {
        i += 1;
    }
    if !b.get(i).is_some_and(u8::is_ascii_digit) {
        return false;
    }
    i += 1;
    while i < b.len() && (b[i].is_ascii_digit() || b[i] == b'_') {
        i += 1;
    }
    let mut groups = 0;
    while b.get(i) == Some(&b':') {
        i += 1;
        let start = i;
        while i < b.len() && b[i].is_ascii_digit() && i - start < 2 {
            i += 1;
        }
        match i - start {
            1 => {}
            2 if b[start] <= b'5' => {}
            _ => return false,
        }
        groups += 1;
    }
    if groups == 0 {
        return false;
    }
    if b.get(i) == Some(&b'.') {
        i += 1;
        while i < b.len() && (b[i].is_ascii_digit() || b[i] == b'_') {
            i += 1;
        }
    }
    i == b.len()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn r(s: &str) -> (String, Scalar) {
        resolve("", s).unwrap()
    }

    #[test]
    fn resolves_like_yaml_v3() {
        assert_eq!(r("123"), (INT_TAG.into(), Scalar::Int(123)));
        assert_eq!(r("0x1F"), (INT_TAG.into(), Scalar::Int(31)));
        assert_eq!(r("0o17"), (INT_TAG.into(), Scalar::Int(15)));
        assert_eq!(r("017"), (INT_TAG.into(), Scalar::Int(15)));
        assert_eq!(r("0b101"), (INT_TAG.into(), Scalar::Int(5)));
        assert_eq!(r("-0b101"), (INT_TAG.into(), Scalar::Int(-5)));
        assert_eq!(r("1_000"), (INT_TAG.into(), Scalar::Int(1000)));
        assert_eq!(
            r("9223372036854775808"),
            (INT_TAG.into(), Scalar::Uint(9223372036854775808))
        );
        assert_eq!(r("0.9"), (FLOAT_TAG.into(), Scalar::Float(0.9)));
        assert_eq!(r(".5"), (FLOAT_TAG.into(), Scalar::Float(0.5)));
        assert_eq!(r("1e3"), (FLOAT_TAG.into(), Scalar::Float(1000.0)));
        assert_eq!(r("1e400"), (STR_TAG.into(), Scalar::Str("1e400".into())));
        assert_eq!(r("yes"), (STR_TAG.into(), Scalar::Str("yes".into())));
        assert_eq!(r("True"), (BOOL_TAG.into(), Scalar::Bool(true)));
        assert_eq!(r("~"), (NULL_TAG.into(), Scalar::Null));
        assert_eq!(r(""), (NULL_TAG.into(), Scalar::Null));
        assert_eq!(r("2024-01-02").0, TIMESTAMP_TAG);
        assert_eq!(r("2024-1-2T3:04:05Z").0, TIMESTAMP_TAG);
        assert_eq!(r("2024-02-30").0, STR_TAG);
        assert_eq!(r("08"), (FLOAT_TAG.into(), Scalar::Float(8.0)));
        assert_eq!(r("<<").0, STR_TAG, "hint table has no entry for <");
        assert!(resolve("!!int", "abc").is_err());
        assert_eq!(
            resolve("!!float", "3").unwrap(),
            (FLOAT_TAG.into(), Scalar::Float(3.0))
        );
    }

    #[test]
    fn formats_like_strconv_g() {
        for (f, want) in [
            (0.9, "0.9"),
            (1e6, "1e+06"),
            (123456.0, "123456"),
            (0.0001, "0.0001"),
            (0.00001, "1e-05"),
            (1.5e300, "1.5e+300"),
            (-2.5, "-2.5"),
            (100.0, "100"),
        ] {
            assert_eq!(format_float(f, true), want);
        }
        assert_eq!(format_float(f64::INFINITY, true), ".inf");
    }

    #[test]
    fn base60() {
        assert!(is_base60_float("1:20"));
        assert!(is_base60_float("190:20:30.15"));
        assert!(!is_base60_float("1:60"));
        assert!(!is_base60_float("abc"));
    }
}
