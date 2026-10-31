//! Finding, judging and merging spans; see the steps in `go/core/redact/spans.go`.

use super::rule::{go_lower, shannon_entropy, Finding};
use super::{Redactor, Rule};

#[derive(Clone, Copy, PartialEq)]
pub(crate) enum Mode {
    /// `apply`: fail-closed allowlists, exemptions recorded.
    RedactRecord,
    /// `spans`: same decisions, nothing recorded.
    RedactQuiet,
    /// `scan`: gitleaks allowlist semantics.
    Detect,
}

/// A surviving finding, or a merged region: byte span and rule index.
#[derive(Clone, Copy, Debug)]
pub(crate) struct Hit {
    pub start: usize,
    pub end: usize,
    pub rule: usize,
}

/// Folds overlapping and adjacent hits; the earliest start wins, then
/// the longest, then the rule added first.
pub(crate) fn merge(mut hits: Vec<Hit>) -> Vec<Hit> {
    hits.sort_by(|a, b| {
        a.start
            .cmp(&b.start)
            .then(b.end.cmp(&a.end))
            .then(a.rule.cmp(&b.rule))
    });
    let mut out: Vec<Hit> = Vec::with_capacity(hits.len());
    for h in hits {
        match out.last_mut() {
            Some(last) if h.start <= last.end => last.end = last.end.max(h.end),
            _ => out.push(h),
        }
    }
    out
}

/// Every rule's hits on `s`, in rule order then position.
pub(crate) fn collect(r: &Redactor, s: &str, mode: Mode) -> Vec<Hit> {
    let mut hits = Vec::new();
    if r.rules.is_empty() || s.is_empty() {
        return hits;
    }
    let present = keywords_present(r, s);
    for (i, rule) in r.rules.iter().enumerate() {
        if s.len() < rule.min_len {
            continue;
        }
        let keys = &r.gate.rule_keys[i];
        let trusted = mode != Mode::Detect && rule.spec.distinctive;
        if !trusted && !keys.is_empty() && !keys.iter().any(|&k| present[k]) {
            continue;
        }
        rule_hits(r, s, i, rule, mode, &mut hits);
    }
    hits
}

/// Which gate keywords occur in the lower-cased input.
fn keywords_present(r: &Redactor, s: &str) -> Vec<bool> {
    let mut present = vec![false; r.gate.keywords.len()];
    if let Some(set) = &r.gate.set {
        for k in set.matches(&go_lower(s)).iter() {
            present[k] = true;
        }
    }
    present
}

fn rule_hits(r: &Redactor, s: &str, i: usize, rule: &Rule, mode: Mode, hits: &mut Vec<Hit>) {
    let g = rule.spec.secret_group;
    let mut push = |ms: usize, me: usize, group: Option<(usize, usize)>| {
        let b = s.as_bytes();
        let (mut ms, mut me) = (ms, me);
        while ms < me && b[ms] == b'\n' {
            ms += 1;
        }
        while me > ms && b[me - 1] == b'\n' {
            me -= 1;
        }
        let (mut ss, mut se) = (ms, me);
        if let Some((gs, ge)) = group {
            let (gs, ge) = (gs.max(ms), ge.min(me));
            if ge > gs {
                (ss, se) = (gs, ge);
            }
        }
        if se <= ss {
            return;
        }
        let secret = &s[ss..se];
        let trusted = mode != Mode::Detect && rule.spec.distinctive;
        if !trusted && rule.spec.entropy != 0.0 && shannon_entropy(secret) <= rule.spec.entropy {
            return;
        }
        if exempt(r, rule, s, secret, ms, me, mode == Mode::Detect) {
            if mode == Mode::RedactRecord {
                r.record_allowed(rule, secret, ss, se);
            }
            return;
        }
        hits.push(Hit {
            start: ss,
            end: se,
            rule: i,
        });
    };
    if g == 0 {
        for m in rule.re.find_iter(s) {
            push(m.start(), m.end(), None);
        }
    } else {
        for c in rule.re.captures_iter(s) {
            let whole = c.get(0).expect("group 0 always participates");
            push(
                whole.start(),
                whole.end(),
                c.get(g).map(|m| (m.start(), m.end())),
            );
        }
    }
}

#[allow(clippy::too_many_arguments)]
fn exempt(
    r: &Redactor,
    rule: &Rule,
    s: &str,
    secret: &str,
    ms: usize,
    me: usize,
    detect: bool,
) -> bool {
    let whole = &s[ms..me];
    let exact = |e: &String| !e.is_empty() && (e == secret || e == whole);
    if r.exact.iter().any(exact) || rule.spec.allowlist_exact.iter().any(exact) {
        return true;
    }
    if r.allowlists.is_empty() && rule.allowlists.is_empty() {
        return false;
    }
    let line = if r.need_line || rule.allowlists.iter().any(|a| a.targets_line()) {
        line_around(s, ms, me)
    } else {
        ""
    };
    let f = Finding {
        secret,
        whole,
        line,
    };
    r.allowlists
        .iter()
        .chain(rule.allowlists.iter())
        .any(|a| a.allows(&f, detect))
}

/// The line(s) holding `s[start..end]`, without their newlines.
fn line_around(s: &str, start: usize, end: usize) -> &str {
    let ls = s[..start].rfind('\n').map_or(0, |i| i + 1);
    let le = s[end..].find('\n').map_or(s.len(), |i| end + i);
    &s[ls..le]
}
