//! Decode helpers shared by the deserializer and the merge step: `<<`
//! merge-key expansion, yaml.v3's duplicate-key check and alias budget,
//! and Go's standard base64 for `!!binary`.

use std::cell::Cell;

use super::de::DeError;
use super::{Kind, Node};
use crate::config::goquote::quote;

/// Go `encoding/base64.StdEncoding.DecodeString` (padding required,
/// CR/LF ignored).
pub(crate) fn base64_decode(s: &str) -> Option<Vec<u8>> {
    let clean: Vec<u8> = s.bytes().filter(|b| *b != b'\r' && *b != b'\n').collect();
    if !clean.len().is_multiple_of(4) {
        return None;
    }
    let val = |c: u8| -> Option<u32> {
        Some(match c {
            b'A'..=b'Z' => u32::from(c - b'A'),
            b'a'..=b'z' => u32::from(c - b'a') + 26,
            b'0'..=b'9' => u32::from(c - b'0') + 52,
            b'+' => 62,
            b'/' => 63,
            _ => return None,
        })
    };
    let mut out = Vec::with_capacity(clean.len() / 4 * 3);
    for (ci, chunk) in clean.chunks(4).enumerate() {
        let last = ci == clean.len() / 4 - 1;
        let pad = chunk.iter().rev().take_while(|c| **c == b'=').count();
        if pad > 2 || (pad > 0 && !last) {
            return None;
        }
        let mut acc = 0u32;
        for &c in &chunk[..4 - pad] {
            acc = (acc << 6) | val(c)?;
        }
        acc <<= 6 * pad as u32;
        let bytes = [(acc >> 16) as u8, (acc >> 8) as u8, acc as u8];
        out.extend_from_slice(&bytes[..3 - pad]);
    }
    Some(out)
}

/// Mapping entries with `<<` merges expanded: explicit keys first, then
/// merged keys not already present (earlier merge sources win).
pub(crate) fn expand_merges(n: &Node) -> Result<Vec<(&Node, &Node)>, DeError> {
    let n = n.target();
    let Kind::Map(entries) = &n.kind else {
        return Ok(Vec::new());
    };
    let mut out: Vec<(&Node, &Node)> = Vec::new();
    let mut merges: Vec<&Node> = Vec::new();
    for (k, v) in entries {
        if k.is_merge_key() {
            merges.push(v);
        } else {
            out.push((k, v));
        }
    }
    let want_map =
        || DeError("yaml: map merge requires map or sequence of maps as the value".into());
    for m in merges {
        let sources: Vec<&Node> = match &m.target().kind {
            Kind::Map(_) => vec![m.target()],
            Kind::Seq(items) => {
                let mut s = Vec::new();
                for i in items {
                    if !matches!(i.target().kind, Kind::Map(_)) {
                        return Err(want_map());
                    }
                    s.push(i.target());
                }
                s
            }
            _ => return Err(want_map()),
        };
        for src in sources {
            for (k, v) in expand_merges(src)? {
                let present = out
                    .iter()
                    .any(|(ek, _)| ek.scalar_value() == k.scalar_value());
                if !present {
                    out.push((k, v));
                }
            }
        }
    }
    Ok(out)
}

/// yaml.v3 rejects duplicate keys (`uniqueKeys` is on by default).
pub(crate) fn check_unique(n: &Node) -> Result<(), DeError> {
    let Kind::Map(entries) = &n.kind else {
        return Ok(());
    };
    // Linear screen first; the pairwise pass below only runs to word
    // the errors in yaml.v3's order.
    let mut seen = std::collections::HashSet::with_capacity(entries.len());
    let dup = entries
        .iter()
        .filter(|(k, _)| !k.is_merge_key())
        .filter_map(|(k, _)| k.scalar_value())
        .any(|k| !seen.insert(k));
    if !dup {
        return Ok(());
    }
    let mut errs = Vec::new();
    for (i, (ki, _)) in entries.iter().enumerate() {
        for (kj, _) in &entries[i + 1..] {
            if let (Some(a), Some(b)) = (ki.scalar_value(), kj.scalar_value()) {
                if a == b && !ki.is_merge_key() {
                    errs.push(format!(
                        "line {}: mapping key {} already defined at line {}",
                        kj.line,
                        quote(b),
                        ki.line
                    ));
                }
            }
        }
    }
    if errs.is_empty() {
        Ok(())
    } else {
        Err(DeError(format!(
            "yaml: unmarshal errors:\n  {}",
            errs.join("\n  ")
        )))
    }
}

/// yaml.v3's alias budget: every node decoded counts, and those decoded
/// through an alias count as aliased. Once past 100 aliased and 1000
/// total, an aliased share above [`allowed_alias_ratio`] fails, which
/// stops billion-laughs expansion early.
#[derive(Clone, Copy, Default)]
struct Budget {
    decoded: u64,
    aliased: u64,
    depth: u32,
}

thread_local! {
    static BUDGET: Cell<Budget> = const { Cell::new(Budget { decoded: 0, aliased: 0, depth: 0 }) };
}

/// yaml.v3 `allowedAliasRatio`.
fn allowed_alias_ratio(decoded: u64) -> f64 {
    const LOW: u64 = 400_000;
    const HIGH: u64 = 4_000_000;
    if decoded <= LOW {
        0.99
    } else if decoded >= HIGH {
        0.10
    } else {
        0.99 - 0.89 * ((decoded - LOW) as f64 / (HIGH - LOW) as f64)
    }
}

fn count() -> Result<(), DeError> {
    let mut b = BUDGET.get();
    b.decoded += 1;
    if b.depth > 0 {
        b.aliased += 1;
    }
    BUDGET.set(b);
    if b.aliased > 100
        && b.decoded > 1000
        && b.aliased as f64 / b.decoded as f64 > allowed_alias_ratio(b.decoded)
    {
        return Err(DeError("yaml: document contains excessive aliasing".into()));
    }
    Ok(())
}

/// Leaves an alias's scope when dropped.
pub(crate) struct Visit(bool);

impl Drop for Visit {
    fn drop(&mut self) {
        if self.0 {
            BUDGET.with(|c| {
                let mut b = c.get();
                b.depth -= 1;
                c.set(b);
            });
        }
    }
}

/// Counts decoding `n` (yaml.v3 `unmarshal`); through an alias, also
/// its target, and everything below it as aliased until the returned
/// guard drops.
pub(crate) fn visit(n: &Node) -> Result<Visit, DeError> {
    count()?;
    if !matches!(n.kind, Kind::Alias(_)) {
        return Ok(Visit(false));
    }
    BUDGET.with(|c| {
        let mut b = c.get();
        b.depth += 1;
        c.set(b);
    });
    let guard = Visit(true);
    count()?;
    Ok(guard)
}

/// Runs one top-level decode with a fresh budget (yaml.v3 counts per
/// `Unmarshal`), restoring the outer one after.
pub(crate) fn with_budget<T>(f: impl FnOnce() -> T) -> T {
    let saved = BUDGET.replace(Budget::default());
    let out = f();
    BUDGET.set(saved);
    out
}
