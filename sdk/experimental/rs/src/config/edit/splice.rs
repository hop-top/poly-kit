//! Text splicing: apply an edit to the source by replacing or inserting
//! only the bytes of the affected entry, so comments, blank lines,
//! anchors and formatting elsewhere survive untouched. Returns `None`
//! for shapes it does not handle; the caller then falls back.

use crate::config::yaml::{emit, Kind, Node, Style};

use super::tree::nest;

fn line_start(src: &str, pos: usize) -> usize {
    src[..pos].rfind('\n').map_or(0, |i| i + 1)
}

fn line_end(src: &str, pos: usize) -> usize {
    src[pos..].find('\n').map_or(src.len(), |i| pos + i)
}

fn col(src: &str, pos: usize) -> usize {
    pos - line_start(src, pos)
}

fn span(n: &Node) -> Option<(usize, usize)> {
    n.span
}

/// Position of the `:` indicator after a key.
fn colon_after(src: &str, key: &Node) -> Option<usize> {
    let (_, end) = span(key)?;
    let rest = &src[end..];
    let off = rest.len() - rest.trim_start_matches([' ', '\t']).len();
    (src.as_bytes().get(end + off) == Some(&b':')).then_some(end + off)
}

fn is_empty_plain(v: &Node) -> bool {
    v.style == Style::Plain && v.scalar_value() == Some("")
}

/// Last byte of an entry: the value's end, or the colon for an empty value.
fn entry_end(src: &str, k: &Node, v: &Node) -> Option<usize> {
    if is_empty_plain(v) {
        return colon_after(src, k).map(|c| c + 1);
    }
    span(v).map(|s| s.1)
}

/// Indentation step of the document: the first nested block mapping's
/// offset from its parent key, else 2.
pub(crate) fn indent_unit(src: &str, root: &Node) -> usize {
    fn find(src: &str, n: &Node) -> Option<usize> {
        let Kind::Map(entries) = &n.kind else {
            return None;
        };
        for (k, v) in entries {
            if let (Kind::Map(child), Style::Plain) = (&v.kind, v.style) {
                if let (Some(ck), Some(kk)) = (child.first().and_then(|c| span(&c.0)), span(k)) {
                    let (c, p) = (col(src, ck.0), col(src, kk.0));
                    if c > p {
                        return Some(c - p);
                    }
                }
            }
            if let Some(u) = find(src, v) {
                return Some(u);
            }
        }
        None
    }
    find(src, root).unwrap_or(2)
}

fn replace(src: &str, from: usize, to: usize, with: &str) -> String {
    format!("{}{with}{}", &src[..from], &src[to..])
}

/// End of the value's properties (anchor, tag) on the key line, before
/// any comment.
fn props_end(src: &str, colon: usize) -> usize {
    let le = line_end(src, colon);
    let rest = &src[colon + 1..le];
    let mut cut = rest.len();
    let b = rest.as_bytes();
    for i in 0..b.len() {
        if b[i] == b'#' && (i == 0 || b[i - 1] == b' ' || b[i - 1] == b'\t') {
            cut = i;
            break;
        }
    }
    colon + 1 + rest[..cut].trim_end().len()
}

/// Replaces the value of entry `(k, v)` with `new`.
fn replace_value(
    src: &str,
    parent: &Node,
    k: &Node,
    v: &Node,
    new: &Node,
    unit: usize,
) -> Option<String> {
    if parent.style == Style::Flow {
        let (s, e) = span(v)?;
        if is_empty_plain(v) {
            return None;
        }
        return Some(replace(src, s, e, &emit::flow(new)));
    }
    let colon = colon_after(src, k)?;
    let kcol = col(src, span(k)?.0);
    let text = emit::after_key(new, kcol, unit);
    let block = emit::is_block(new);
    if is_empty_plain(v) {
        let at = if block {
            line_end(src, colon)
        } else {
            colon + 1
        };
        return Some(replace(src, at, at, &text));
    }
    let (s, e) = span(v)?;
    let inline_old = !src[colon..s].contains('\n');
    match (inline_old, block) {
        (true, false) => Some(replace(src, s, e, text.trim_start_matches(' '))),
        (true, true) => {
            let at = line_end(src, e);
            let ws = colon + 1 + src[colon + 1..s].trim_end_matches([' ', '\t']).len();
            let out = format!("{}{}{text}{}", &src[..ws], &src[e..at], &src[at..]);
            Some(out)
        }
        (false, false) => {
            let ls = line_start(src, s);
            let pe = props_end(src, colon);
            Some(format!(
                "{}{text}{}{}",
                &src[..pe],
                &src[pe..ls.saturating_sub(1)],
                &src[e..]
            ))
        }
        (false, true) => {
            let body = text.strip_prefix('\n')?;
            Some(replace(src, line_start(src, s), e, body))
        }
    }
}

/// Appends `key: new` to mapping `map`.
fn insert_entry(src: &str, map: &Node, key: &str, new: &Node, unit: usize) -> Option<String> {
    let Kind::Map(entries) = &map.kind else {
        return None;
    };
    let key_text = emit::key(&Node::str(key));
    if map.style == Style::Flow {
        let entry = format!("{key_text}: {}", emit::flow(new));
        return match entries.last() {
            Some((lk, lv)) => {
                let at = entry_end(src, lk, lv)?;
                Some(replace(src, at, at, &format!(", {entry}")))
            }
            None => {
                let at = span(map)?.0 + 1;
                Some(replace(src, at, at, &entry))
            }
        };
    }
    let (first, _) = entries.first()?;
    let mcol = col(src, span(first)?.0);
    let (lk, lv) = entries.last()?;
    let at = line_end(src, entry_end(src, lk, lv)?);
    let mut line = format!(
        "\n{}{key_text}:{}",
        " ".repeat(mcol),
        emit::after_key(new, mcol, unit)
    );
    if at == src.len() && src.ends_with('\n') {
        line.remove(0);
        line.push('\n');
    }
    Some(replace(src, at, at, &line))
}

/// Splices `key: value` into `src` (whose parsed root mapping is `root`).
pub(crate) fn set(src: &str, root: &Node, key: &str, value: &Node) -> Option<String> {
    let unit = indent_unit(src, root);
    let segs: Vec<&str> = key.split('.').collect();
    let mut cur = root;
    for (i, seg) in segs.iter().enumerate() {
        let Kind::Map(entries) = &cur.kind else {
            return None;
        };
        let Some((k, v)) = entries.iter().find(|(k, _)| k.scalar_value() == Some(seg)) else {
            return insert_entry(src, cur, seg, &nest(&segs[i + 1..], value.clone()), unit);
        };
        if i + 1 == segs.len() {
            return replace_value(src, cur, k, v, value, unit);
        }
        if !matches!(v.kind, Kind::Map(_)) {
            return replace_value(src, cur, k, v, &nest(&segs[i + 1..], value.clone()), unit);
        }
        cur = v;
    }
    None
}

/// Removes entry `key` from `map`, with its line(s) in block style.
fn remove_entry(src: &str, map: &Node, key: &str) -> Option<String> {
    let Kind::Map(entries) = &map.kind else {
        return None;
    };
    let idx = entries
        .iter()
        .position(|(k, _)| k.scalar_value() == Some(key))?;
    let (k, v) = &entries[idx];
    let ks = span(k)?.0;
    let end = entry_end(src, k, v)?;
    if map.style == Style::Flow {
        let (from, to) = if let Some((nk, _)) = entries.get(idx + 1) {
            (ks, span(nk)?.0)
        } else if idx > 0 {
            let (pk, pv) = &entries[idx - 1];
            (entry_end(src, pk, pv)?, end)
        } else {
            (ks, end)
        };
        return Some(replace(src, from, to, ""));
    }
    let ls = line_start(src, ks);
    if !src[ls..ks].trim().is_empty() {
        return None;
    }
    let le = line_end(src, end);
    let to = (le + 1).min(src.len());
    Some(replace(src, ls, to, ""))
}

/// Splices the removal of `key` (and of ancestors it leaves empty).
pub(crate) fn unset(src: &str, root: &Node, key: &str) -> Option<String> {
    let parts: Vec<&str> = key.split('.').collect();
    let mut chain = vec![root];
    for p in &parts[..parts.len() - 1] {
        let Kind::Map(entries) = &chain.last()?.kind else {
            return None;
        };
        let next = entries
            .iter()
            .find(|(k, _)| k.scalar_value() == Some(p))
            .map(|(_, v)| v)?;
        chain.push(next);
    }
    let mut depth = parts.len() - 1;
    while depth >= 1 && matches!(&chain[depth].kind, Kind::Map(e) if e.len() == 1) {
        depth -= 1;
    }
    remove_entry(src, chain[depth], parts[depth])
}
