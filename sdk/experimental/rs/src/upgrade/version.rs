//! Migration version order: Go `parseSemver` / `compareSemver`, which are
//! lenient where semver.org is strict. Never fails; malformed parts read
//! as 0.

use std::cmp::Ordering;

#[derive(Debug, PartialEq, Eq)]
struct Version<'a> {
    major: i64,
    minor: i64,
    patch: i64,
    pre: &'a str,
}

/// Go `fmt.Sscanf(part, "%d")` on a core part: its leading decimal
/// digits, 0 when there are none or they overflow.
fn leading_number(part: &str) -> i64 {
    let end = part
        .find(|c: char| !c.is_ascii_digit())
        .unwrap_or(part.len());
    part[..end].parse().unwrap_or(0)
}

fn parse(s: &str) -> Version<'_> {
    let s = s.strip_prefix('v').unwrap_or(s);
    let s = s.split_once('+').map_or(s, |(core, _)| core);
    let (core, pre) = s.split_once('-').unwrap_or((s, ""));
    let mut parts = core.splitn(3, '.').map(leading_number);
    Version {
        major: parts.next().unwrap_or(0),
        minor: parts.next().unwrap_or(0),
        patch: parts.next().unwrap_or(0),
        pre,
    }
}

/// A pre-release identifier is numeric when it round-trips through an
/// integer unchanged: no sign, no leading zero, in range.
fn numeric(ident: &str) -> Option<i64> {
    ident.parse::<i64>().ok().filter(|n| n.to_string() == ident)
}

fn compare_ident(a: &str, b: &str) -> Ordering {
    match (numeric(a), numeric(b)) {
        (Some(x), Some(y)) => x.cmp(&y),
        (Some(_), None) => Ordering::Less,
        (None, Some(_)) => Ordering::Greater,
        (None, None) => a.cmp(b),
    }
}

fn compare_pre(a: &str, b: &str) -> Ordering {
    let (mut xs, mut ys) = (a.split('.'), b.split('.'));
    loop {
        match (xs.next(), ys.next()) {
            (Some(x), Some(y)) => match compare_ident(x, y) {
                Ordering::Equal => continue,
                other => return other,
            },
            (None, None) => return Ordering::Equal,
            (None, Some(_)) => return Ordering::Less,
            (Some(_), None) => return Ordering::Greater,
        }
    }
}

/// Orders two migration versions. A leading `v` and build metadata are
/// ignored, a release sorts above its pre-releases.
///
/// ```
/// use std::cmp::Ordering;
/// use hop_top_kit::upgrade::compare_versions;
///
/// assert_eq!(compare_versions("1.9.0", "v1.10.0"), Ordering::Less);
/// assert_eq!(compare_versions("1.0.0-rc.1", "1.0.0"), Ordering::Less);
/// ```
pub fn compare_versions(a: &str, b: &str) -> Ordering {
    let (x, y) = (parse(a), parse(b));
    (x.major, x.minor, x.patch)
        .cmp(&(y.major, y.minor, y.patch))
        .then_with(|| match (x.pre.is_empty(), y.pre.is_empty()) {
            (true, true) => Ordering::Equal,
            (true, false) => Ordering::Greater,
            (false, true) => Ordering::Less,
            (false, false) => compare_pre(x.pre, y.pre),
        })
}
