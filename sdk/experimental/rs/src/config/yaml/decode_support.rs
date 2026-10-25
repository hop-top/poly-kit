//! Decode helpers shared by the deserializer and the merge step: `<<`
//! merge-key expansion, yaml.v3's duplicate-key check, and Go's
//! standard base64 for `!!binary`.

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
