//! Inputs libyaml (and so Go) reads but saphyr-parser rejects, made
//! readable without changing any value, in one linear pass:
//!
//! - a tab in the whitespace after a `:` indicator becomes a space;
//! - continuation lines of a multi-line quoted scalar that sit at or left
//!   of the block indent (or start with a tab) get leading spaces, which
//!   folding drops.
//!
//! Candidates are found lexically, then checked against the parsed tree:
//! a tab fix inside a quoted or block scalar, or an insert outside a
//! quoted scalar, is withdrawn and the text re-parsed. A few parses at
//! most, whatever the number of fixes.

use super::{Kind, Node, Style};

/// Parser messages the pass can fix.
pub(super) fn repairable(msg: &str) -> bool {
    matches!(
        msg,
        "':' must be followed by a valid YAML whitespace"
            | "invalid indentation in quoted scalar"
            | "tab cannot be used as indentation"
    )
}

/// Fixes in source offsets: tabs to turn into spaces (same length), and
/// `(offset, spaces)` inserts, both sorted.
#[derive(Default)]
pub(super) struct Fixes {
    pub tabs: Vec<usize>,
    pub inserts: Vec<(usize, usize)>,
}

impl Fixes {
    /// The repaired text.
    pub(super) fn apply(&self, src: &str) -> String {
        self.apply_with(src, true)
    }

    /// The repaired text; `tabs: false` keeps the tabs (same offsets).
    pub(super) fn apply_with(&self, src: &str, tabs: bool) -> String {
        let mut b = src.as_bytes().to_vec();
        if tabs {
            for &t in &self.tabs {
                b[t] = b' ';
            }
        }
        let mut out = Vec::with_capacity(b.len() + self.inserts.iter().map(|i| i.1).sum::<usize>());
        let mut last = 0;
        for &(p, n) in &self.inserts {
            out.extend_from_slice(&b[last..p]);
            out.extend(std::iter::repeat_n(b' ', n));
            last = p;
        }
        out.extend_from_slice(&b[last..]);
        // Only ASCII bytes were replaced or inserted at char boundaries.
        String::from_utf8(out).expect("repairs keep UTF-8")
    }
}

/// Scalar spans of a tree, sorted by start (scalars never nest).
struct Spans(Vec<(usize, usize, Style)>);

impl Spans {
    fn of(root: Option<&Node>) -> Spans {
        let mut v = Vec::new();
        if let Some(r) = root {
            scalar_spans(r, &mut v);
        }
        v.sort_unstable_by_key(|s| s.0);
        Spans(v)
    }

    /// The scalar whose span holds offset `p` (`strict`: not its start).
    fn at(&self, p: usize, strict: bool) -> Option<Style> {
        let i = self.0.partition_point(|s| s.0 <= p);
        let &(s, e, st) = self.0.get(i.checked_sub(1)?)?;
        (p < e && (!strict || s < p)).then_some(st)
    }
}

/// Repaired-text offsets back to source offsets, in O(log n) each.
pub(super) struct OrigMap {
    /// `(text offset of the insert, source offset, spaces, spaces up to
    /// and including it)`, ascending.
    runs: Vec<(usize, usize, usize, usize)>,
}

impl OrigMap {
    pub(super) fn new(inserts: &[(usize, usize)]) -> OrigMap {
        let mut cum = 0;
        let runs = inserts
            .iter()
            .map(|&(p, n)| {
                let at = p + cum;
                cum += n;
                (at, p, n, cum)
            })
            .collect();
        OrigMap { runs }
    }

    /// Source offset for offset `m` in the repaired text (inside an
    /// inserted run: where it was inserted).
    pub(super) fn orig(&self, m: usize) -> usize {
        let i = self.runs.partition_point(|r| r.0 <= m);
        match i.checked_sub(1).map(|i| self.runs[i]) {
            None => m,
            Some((at, p, n, _)) if m < at + n => p,
            Some((_, _, _, cum)) => m - cum,
        }
    }
}

fn is_ws(c: u8) -> bool {
    c == b' ' || c == b'\t'
}

/// End (exclusive) of the quoted scalar opening at `q`, or the text end.
fn quoted_end(b: &[u8], q: usize) -> usize {
    let quote = b[q];
    let mut i = q + 1;
    while i < b.len() {
        match (quote, b[i]) {
            (b'"', b'\\') => i += 2,
            (b'"', b'"') => return i + 1,
            (b'\'', b'\'') if b.get(i + 1) == Some(&b'\'') => i += 2,
            (b'\'', b'\'') => return i + 1,
            _ => i += 1,
        }
    }
    b.len()
}

fn indent(b: &[u8], ls: usize) -> usize {
    b[ls..].iter().take_while(|c| **c == b' ').count()
}

/// `---` or `...` at column 0: a document marker, never content.
fn doc_marker(b: &[u8], ls: usize) -> bool {
    let m = &b[ls..b.len().min(ls + 3)];
    (m == b"---" || m == b"...")
        && b.get(ls + 3)
            .is_none_or(|c| matches!(c, b' ' | b'\t' | b'\r' | b'\n'))
}

/// A blank, line end or end of text after `b[i]`.
fn blank_after(b: &[u8], i: usize) -> bool {
    b.get(i + 1)
        .is_none_or(|c| matches!(c, b' ' | b'\t' | b'\r' | b'\n'))
}

/// All candidate fixes in `src`, in one forward scan (O(len)).
///
/// Quotes count as opening a scalar only at a node start: at a line
/// start, after a `:` value indicator, after a `-`/`?` indicator that
/// itself sits at a node start, or after `,` `[` `{` inside a flow
/// collection. A quote inside a plain scalar (`Rock, 'n roll`) is text.
pub(super) fn candidates(src: &str) -> Fixes {
    let b = src.as_bytes();
    let mut f = Fixes::default();
    // Tabs in the whitespace run after any `:`.
    for i in 0..b.len() {
        if b[i] != b':' {
            continue;
        }
        let mut j = i + 1;
        while j < b.len() && is_ws(b[j]) {
            if b[j] == b'\t' {
                f.tabs.push(j);
            }
            j += 1;
        }
    }
    let mut i = 0;
    let mut ls = 0; // start of the current line
    let mut start = true; // the next token begins a node
    let mut flow = 0usize; // flow collection depth
    while i < b.len() {
        let c = b[i];
        if c == b'\n' {
            i += 1;
            ls = i;
            start = flow == 0 || start;
            continue;
        }
        if is_ws(c) || c == b'\r' {
            i += 1;
            continue;
        }
        if c == b'#' && (i == ls || matches!(b[i - 1], b' ' | b'\t')) {
            while i < b.len() && b[i] != b'\n' {
                i += 1;
            }
            continue;
        }
        if start && (c == b'&' || c == b'!') {
            while i < b.len() && !matches!(b[i], b' ' | b'\t' | b'\r' | b'\n') {
                i += 1;
            }
            continue;
        }
        if start && flow == 0 && (c == b'|' || c == b'>') {
            let mut j = i + 1;
            while j < b.len() && matches!(b[j], b'0'..=b'9' | b'+' | b'-') {
                j += 1;
            }
            while j < b.len() && is_ws(b[j]) {
                j += 1;
            }
            if j >= b.len() || matches!(b[j], b'\n' | b'\r' | b'#') {
                // Skip the body: blank lines and lines indented deeper.
                let ind = indent(b, ls);
                let mut k = b[j..]
                    .iter()
                    .position(|c| *c == b'\n')
                    .map_or(b.len(), |p| j + p + 1);
                while k < b.len() {
                    let end = b[k..]
                        .iter()
                        .position(|c| *c == b'\n')
                        .map_or(b.len(), |p| k + p);
                    let blank = b[k..end].iter().all(|c| matches!(c, b' ' | b'\t' | b'\r'));
                    if !blank && indent(b, k) <= ind {
                        break;
                    }
                    k = (end + 1).min(b.len());
                }
                i = k;
                ls = k;
                start = true;
                continue;
            }
        }
        if start && (c == b'"' || c == b'\'') {
            let e = quoted_end(b, i);
            let need = i - ls + 1;
            for k in i..e {
                if b[k] != b'\n' {
                    continue;
                }
                let next = k + 1;
                ls = next;
                if next >= b.len() || doc_marker(b, next) || matches!(b[next], b'\n' | b'\r') {
                    continue;
                }
                let sp = indent(b, next);
                if sp < need {
                    f.inserts.push((next, need - sp));
                }
            }
            start = false;
            i = e;
            continue;
        }
        match c {
            b'-' | b'?' if start && blank_after(b, i) => start = true,
            b':' if blank_after(b, i)
                || (flow > 0 && matches!(b.get(i + 1), Some(b',' | b']' | b'}'))) =>
            {
                start = true
            }
            b'[' | b'{' if start || flow > 0 => {
                flow += 1;
                start = true;
            }
            b',' if flow > 0 => start = true,
            b']' | b'}' if flow > 0 => {
                flow -= 1;
                start = false;
            }
            _ => start = false,
        }
        i += 1;
    }
    f
}

/// Quoted and block scalar spans (`quoted` = flow-quoted) of a tree in
/// repaired-text offsets.
fn scalar_spans(n: &Node, out: &mut Vec<(usize, usize, Style)>) {
    match &n.kind {
        Kind::Scalar(_) => {
            if let Some((s, e)) = n.span {
                out.push((s, e, n.style));
            }
        }
        Kind::Seq(items) => items.iter().for_each(|i| scalar_spans(i, out)),
        Kind::Map(entries) => entries.iter().for_each(|(k, v)| {
            scalar_spans(k, out);
            scalar_spans(v, out);
        }),
        Kind::Alias(_) => {}
    }
}

/// Withdraws fixes the parsed tree shows were wrong; `true` if any.
/// Both lists are sorted, so text offsets accumulate in one sweep.
pub(super) fn withdraw_wrong(f: &mut Fixes, root: Option<&Node>) -> bool {
    let spans = Spans::of(root);
    let quoted = |s: Style| matches!(s, Style::Single | Style::Double);
    let content = |s: Style| quoted(s) || matches!(s, Style::Literal | Style::Folded);
    let before = f.tabs.len() + f.inserts.len();
    // Tab at source t sits after every insert at or before t.
    let (mut k, mut shift) = (0, 0);
    let inserts = f.inserts.clone();
    f.tabs.retain(|&t| {
        while k < inserts.len() && inserts[k].0 <= t {
            shift += inserts[k].1;
            k += 1;
        }
        !spans.at(t + shift, false).is_some_and(content)
    });
    // An insert at q lands after every earlier insert.
    let mut shift = 0;
    f.inserts.retain(|&(q, n)| {
        let ok = spans.at(q + shift, true).is_some_and(quoted);
        shift += n;
        ok
    });
    f.tabs.len() + f.inserts.len() != before
}

/// libyaml rejects a tab between a block indicator at the start of a
/// line (`-`, `?`, `:`) and what follows it (`-\tx`, `- \tx`,
/// `:\tb`); saphyr accepts it. `text` must still hold the tabs. The 1-based line
/// of the first one outside a scalar, if any.
pub(super) fn dash_tab(text: &str, root: Option<&Node>) -> Option<usize> {
    let b = text.as_bytes();
    if !text.contains('\t') {
        return None;
    }
    let spans = Spans::of(root);
    let mut ls = 0;
    let mut line = 1;
    while ls < b.len() {
        let end = b[ls..]
            .iter()
            .position(|c| *c == b'\n')
            .map_or(b.len(), |p| ls + p);
        let mut i = ls + indent(b, ls);
        // Each block indicator (`-`, `?`, `:`) opening the line, and the
        // blanks after it.
        while i + 1 < end && matches!(b[i], b'-' | b'?' | b':') && is_ws(b[i + 1]) {
            let mut j = i + 1;
            let mut tab = false;
            while j < end && is_ws(b[j]) {
                tab |= b[j] == b'\t';
                j += 1;
            }
            let token = j < end && !matches!(b[j], b'#' | b'\r');
            if tab && token && spans.at(i, false).is_none() {
                return Some(line);
            }
            i = j;
        }
        ls = end + 1;
        line += 1;
    }
    None
}
