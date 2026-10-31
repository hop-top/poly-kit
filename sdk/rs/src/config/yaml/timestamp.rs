//! yaml.v3 timestamp detection (`parseTimestamp` with Go's
//! `time.Parse` range checks).

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
