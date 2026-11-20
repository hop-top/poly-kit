//! Absolute ISO-8601-ish timestamps, a port of the Go `isoLayouts` table.
//!
//! [`jiff`]'s `strtime` is stricter than the layouts the Go port (and the
//! chrono parser this replaced) accept: single-digit fields, a permissive
//! offset (`Z`, `+05`, `+0500`, `+05:00`), unbounded fractional digits and
//! leap seconds. So the layouts are read by this small cursor instead, which
//! follows the same item rules: leading whitespace is skipped before each
//! number, a space in a layout matches any run of whitespace, and anything
//! left over rejects the layout.

use jiff::{civil, tz::TimeZone, Timestamp};

use super::TimeError;

/// How the `T` / space between date and time is matched.
#[derive(Clone, Copy)]
enum Sep {
    T,
    Space,
}

/// The offset a layout ends with.
#[derive(Clone, Copy)]
enum Tz {
    /// No offset: wall-clock time, taken as UTC.
    None,
    /// `+hh:mm` (colon optional), minutes required, no `Z`.
    Strict,
    /// `Z`, `+hh`, `+hhmm` or `+hh:mm`.
    Permissive,
}

/// One layout: separator, seconds present, fractional seconds allowed, offset.
struct Layout {
    sep: Sep,
    seconds: bool,
    frac: bool,
    tz: Tz,
}

/// Layouts with an explicit offset are tried first, then the offset-less
/// ones, in the order of the Go table.
const LAYOUTS: &[Layout] = &[
    Layout {
        sep: Sep::T,
        seconds: true,
        frac: true,
        tz: Tz::Strict,
    },
    Layout {
        sep: Sep::T,
        seconds: true,
        frac: false,
        tz: Tz::Permissive,
    },
    Layout {
        sep: Sep::T,
        seconds: false,
        frac: false,
        tz: Tz::Strict,
    },
    Layout {
        sep: Sep::T,
        seconds: false,
        frac: false,
        tz: Tz::Permissive,
    },
    Layout {
        sep: Sep::T,
        seconds: true,
        frac: true,
        tz: Tz::None,
    },
    Layout {
        sep: Sep::T,
        seconds: false,
        frac: false,
        tz: Tz::None,
    },
    Layout {
        sep: Sep::Space,
        seconds: true,
        frac: true,
        tz: Tz::None,
    },
    Layout {
        sep: Sep::Space,
        seconds: false,
        frac: false,
        tz: Tz::None,
    },
];

/// Parse an absolute ISO-8601-ish timestamp.
///
/// Handles the offset-bearing forms (`Z`, `+05:00`) as well as the
/// offset-less forms, which are interpreted as UTC. Date-only input
/// (`"2026-05-01"`) resolves to midnight UTC.
pub(crate) fn parse_iso(s: &str) -> Result<Timestamp, TimeError> {
    for layout in LAYOUTS {
        if let Some(ts) = Cursor(s).layout(layout) {
            return Ok(ts);
        }
    }
    if let Some(ts) = Cursor(s).date_only() {
        return Ok(ts);
    }
    Err(TimeError::Unrecognized(s.to_string()))
}

/// The parsed fields of one layout, before range checks.
struct Fields {
    date: (i64, i64, i64),
    time: (i64, i64, i64, i32),
    offset: i32,
}

/// A read position in the input.
#[derive(Clone, Copy)]
struct Cursor<'a>(&'a str);

impl<'a> Cursor<'a> {
    fn skip_space(&mut self) {
        self.0 = self.0.trim_start();
    }

    fn literal(&mut self, c: char) -> Option<()> {
        self.0 = self.0.strip_prefix(c)?;
        Some(())
    }

    /// `min..=max` ASCII digits, after skipping whitespace.
    fn number(&mut self, max: usize) -> Option<i64> {
        self.skip_space();
        let end = self
            .0
            .bytes()
            .take(max)
            .take_while(u8::is_ascii_digit)
            .count();
        let (digits, rest) = self.0.split_at(end);
        let n = digits.parse().ok()?;
        self.0 = rest;
        Some(n)
    }

    /// `%Y`: an explicit sign lifts the four-digit cap.
    fn year(&mut self) -> Option<i64> {
        self.skip_space();
        let (neg, rest) = if let Some(rest) = self.0.strip_prefix('-') {
            (true, Some(rest))
        } else {
            (false, self.0.strip_prefix('+'))
        };
        match rest {
            Some(rest) => {
                self.0 = rest;
                let n = self.number(usize::MAX)?;
                Some(if neg { -n } else { n })
            }
            None => self.number(4),
        }
    }

    fn date(&mut self) -> Option<(i64, i64, i64)> {
        let y = self.year()?;
        self.literal('-')?;
        let m = self.number(2)?;
        self.literal('-')?;
        let d = self.number(2)?;
        Some((y, m, d))
    }

    /// `%.f`: nothing, or a dot and at least one digit; digits past the
    /// ninth are read and dropped.
    fn fraction(&mut self) -> Option<i32> {
        let Some(rest) = self.0.strip_prefix('.') else {
            return Some(0);
        };
        let end = rest.bytes().take_while(u8::is_ascii_digit).count();
        if end == 0 {
            return None;
        }
        let nanos: i32 = format!("{:0<9}", &rest[..end.min(9)]).parse().ok()?;
        self.0 = &rest[end..];
        Some(nanos)
    }

    /// An offset in seconds east of UTC, `|offset| < 24h`.
    fn offset(&mut self, tz: Tz) -> Option<i32> {
        self.skip_space();
        if matches!(tz, Tz::Permissive) && self.0.starts_with(['Z', 'z']) {
            self.0 = &self.0[1..];
            return Some(0);
        }
        let (neg, rest) = match self.0.chars().next()? {
            '+' => (false, &self.0[1..]),
            '-' | '\u{2212}' => (true, &self.0[self.0.chars().next()?.len_utf8()..]),
            _ => return None,
        };
        let two = |s: &str| -> Option<i32> {
            let b = s.as_bytes().get(..2)?;
            (b[0].is_ascii_digit() && b[1].is_ascii_digit())
                .then(|| i32::from(b[0] - b'0') * 10 + i32::from(b[1] - b'0'))
        };
        let hours = two(rest)?;
        let rest = rest[2..].trim_start_matches(|c: char| c == ':' || c.is_whitespace());
        let (minutes, rest) = match two(rest) {
            Some(m) if m < 60 => (m, &rest[2..]),
            Some(_) => return None,
            None if matches!(tz, Tz::Permissive) && rest.len() != 1 => (0, rest),
            None => return None,
        };
        self.0 = rest;
        let secs = hours * 3600 + minutes * 60;
        (secs < 86_400).then_some(if neg { -secs } else { secs })
    }

    fn layout(mut self, l: &Layout) -> Option<Timestamp> {
        let date = self.date()?;
        match l.sep {
            Sep::T => self.literal('T')?,
            Sep::Space => self.skip_space(),
        }
        let hour = self.number(2)?;
        self.literal(':')?;
        let minute = self.number(2)?;
        let (mut second, mut nanos) = (0, 0);
        if l.seconds {
            self.literal(':')?;
            second = self.number(2)?;
            if l.frac {
                nanos = self.fraction()?;
            }
        }
        let offset = match l.tz {
            Tz::None => 0,
            tz => self.offset(tz)?,
        };
        if !self.0.is_empty() {
            return None;
        }
        Fields {
            date,
            time: (hour, minute, second, nanos),
            offset,
        }
        .into_timestamp()
    }

    fn date_only(mut self) -> Option<Timestamp> {
        let date = self.date()?;
        if !self.0.is_empty() {
            return None;
        }
        Fields {
            date,
            time: (0, 0, 0, 0),
            offset: 0,
        }
        .into_timestamp()
    }
}

impl Fields {
    /// Range-check the fields and resolve them to an instant. Second 60 is a
    /// leap second: it lands on the next minute, as it did under chrono.
    fn into_timestamp(self) -> Option<Timestamp> {
        let (y, mo, d) = self.date;
        let (h, mi, s, nanos) = self.time;
        let date = civil::Date::new(
            i16::try_from(y).ok()?,
            i8::try_from(mo).ok()?,
            i8::try_from(d).ok()?,
        )
        .ok()?;
        if !(0..=60).contains(&s) {
            return None;
        }
        let time = civil::Time::new(
            i8::try_from(h).ok()?,
            i8::try_from(mi).ok()?,
            i8::try_from(s.min(59)).ok()?,
            nanos,
        )
        .ok()?;
        let utc = date
            .to_datetime(time)
            .to_zoned(TimeZone::UTC)
            .ok()?
            .timestamp();
        let leap = i64::from(s == 60) - i64::from(self.offset);
        utc.checked_add(jiff::SignedDuration::from_secs(leap)).ok()
    }
}
