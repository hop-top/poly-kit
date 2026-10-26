//! Text splicing: apply an edit to the source by replacing or inserting
//! only the bytes of the affected entry, so comments, blank lines,
//! anchors and formatting elsewhere survive untouched. Returns `None`
//! for shapes it does not handle; the caller then refuses the edit.
//!
//! Each splice also returns the source range it dropped: comments and
//! anchors there go with the replaced or removed value (as Go drops the
//! node that carried them); everywhere else they must survive.

/// The source bytes a splice dropped.
pub(crate) type Region = (usize, usize);

use crate::config::yaml::{emit, Kind, Node, Style};

use super::tree::nest;

/// Start of the line holding `pos`. A leading BOM is not part of the
/// first line (it is no indentation, and stays when that line goes).
fn line_start(src: &str, pos: usize) -> usize {
    match src[..pos].rfind('\n') {
        Some(i) => i + 1,
        None if src.starts_with('\u{feff}') && pos >= 3 => 3,
        None => 0,
    }
}

/// `|+` / `>2+`: a block scalar header with keep chomping.
fn keeps_trailing(src: &str, header: usize) -> bool {
    src[header + 1..]
        .bytes()
        .take_while(|c| matches!(c, b'0'..=b'9' | b'+' | b'-'))
        .any(|c| c == b'+')
}

/// End of the line holding `pos`, before its `\n` (or `\r\n`).
fn line_end(src: &str, pos: usize) -> usize {
    match src[pos..].find('\n') {
        Some(i) if i > 0 && src.as_bytes()[pos + i - 1] == b'\r' => pos + i - 1,
        Some(i) => pos + i,
        None => src.len(),
    }
}

/// Start of the line after the one holding `pos`.
fn next_line_start(src: &str, pos: usize) -> usize {
    src[pos..].find('\n').map_or(src.len(), |i| pos + i + 1)
}

/// `t` with the source's line endings (`\r\n` files stay `\r\n`).
fn nl(src: &str, t: &str) -> String {
    if src.contains("\r\n") {
        t.replace("\r\n", "\n").replace('\n', "\r\n")
    } else {
        t.to_string()
    }
}

/// A block scalar (`|`, `>`) value.
fn is_block_scalar(v: &Node) -> bool {
    matches!(v.style, Style::Literal | Style::Folded)
}

/// Where a value's droppable bytes start: a block scalar keeps its
/// header line (and the comment on it) out of the dropped region.
fn region_start(src: &str, v: &Node, s: usize) -> usize {
    if is_block_scalar(v) {
        line_end(src, s)
    } else {
        s
    }
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
) -> Option<(String, Region)> {
    if parent.style == Style::Flow {
        if is_empty_plain(v) {
            // `{a: , b: 1}`: write the value after the colon.
            let at = colon_after(src, k)? + 1;
            let (at, text) = if src.as_bytes().get(at) == Some(&b' ') {
                (at + 1, emit::flow(new))
            } else {
                (at, format!(" {}", emit::flow(new)))
            };
            return Some((replace(src, at, at, &text), (at, at)));
        }
        let (s, e) = span(v)?;
        return Some((replace(src, s, e, &emit::flow(new)), (s, e)));
    }
    let colon = colon_after(src, k)?;
    let kcol = col(src, span(k)?.0);
    let text = nl(src, &emit::after_key(new, kcol, unit));
    // `header` stays on the key line (` x`, ` |-`, or nothing before a
    // nested block); `body` is the following lines, if any.
    let split = text.find(['\r', '\n']).unwrap_or(text.len());
    let (header, body) = text.split_at(split);
    if is_empty_plain(v) {
        let at = line_end(src, colon);
        let out = format!(
            "{}{header}{}{body}{}",
            &src[..colon + 1],
            &src[colon + 1..at],
            &src[at..]
        );
        return Some((out, (colon + 1, colon + 1)));
    }
    let (s, e) = span(v)?;
    let dropped = (region_start(src, v, s), e);
    // The comment on an old block scalar's header line stays with the key.
    let header_comment = if is_block_scalar(v) {
        let h = &src[s..line_end(src, s)];
        h.find(" #").map(|i| h[i..].to_string()).unwrap_or_default()
    } else {
        String::new()
    };
    if !src[colon..s].contains('\n') {
        if body.is_empty() {
            let new_text = format!("{}{header_comment}", header.trim_start_matches(' '));
            return Some((replace(src, s, e, &new_text), dropped));
        }
        let at = line_end(src, e);
        let ws = colon + 1 + src[colon + 1..s].trim_end_matches([' ', '\t']).len();
        let out = format!(
            "{}{header}{header_comment}{}{body}{}",
            &src[..ws],
            &src[e..at],
            &src[at..]
        );
        return Some((out, dropped));
    }
    // The old value starts on a later line: keep the key line (and its
    // comment), drop from the end of that line through the value.
    let pe = props_end(src, colon);
    let ls = line_start(src, s);
    let cut = ls.saturating_sub(1);
    let cut = if cut > 0 && src.as_bytes()[cut - 1] == b'\r' {
        cut - 1
    } else {
        cut
    };
    Some((
        format!("{}{header}{}{body}{}", &src[..pe], &src[pe..cut], &src[e..]),
        (cut, e),
    ))
}

/// Appends `key: new` to mapping `map`.
fn insert_entry(
    src: &str,
    map: &Node,
    key: &str,
    new: &Node,
    unit: usize,
) -> Option<(String, Region)> {
    let Kind::Map(entries) = &map.kind else {
        return None;
    };
    let key_text = emit::key(&Node::str(key));
    if map.style == Style::Flow {
        let entry = format!("{key_text}: {}", emit::flow(new));
        return match entries.last() {
            Some((lk, lv)) => {
                let at = entry_end(src, lk, lv)?;
                Some((replace(src, at, at, &format!(", {entry}")), (at, at)))
            }
            None => {
                let at = span(map)?.0 + 1;
                Some((replace(src, at, at, &entry), (at, at)))
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
    Some((replace(src, at, at, &nl(src, &line)), (at, at)))
}

/// Splices `key: value` into `src` (whose parsed root mapping is `root`).
pub(crate) fn set(src: &str, root: &Node, key: &str, value: &Node) -> Option<(String, Region)> {
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
fn remove_entry(src: &str, map: &Node, key: &str) -> Option<(String, Region)> {
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
        return Some((replace(src, from, to, ""), (from, to)));
    }
    let ls = line_start(src, ks);
    if !src[ls..ks].trim().is_empty() {
        return None;
    }
    let mut to = next_line_start(src, end);
    // A keep-chomped block scalar (`|+`) owns the blank lines after it.
    if is_block_scalar(v) && keeps_trailing(src, span(v)?.0) {
        while to < src.len() && src[to..next_line_start(src, to)].trim().is_empty() {
            to = next_line_start(src, to);
        }
    }
    Some((replace(src, ls, to, ""), (ls, to)))
}

/// Splices the removal of `key` (and of ancestors it leaves empty).
pub(crate) fn unset(src: &str, root: &Node, key: &str) -> Option<(String, Region)> {
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
