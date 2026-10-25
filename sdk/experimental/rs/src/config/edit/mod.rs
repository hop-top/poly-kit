//! Comment-keeping YAML edits for `set`, `set_value` and `unset`.
//!
//! Go re-emits the whole `yaml.Node` document, which keeps comments
//! because yaml.v3 attaches them to nodes. The port edits the source
//! text instead: only the bytes of the targeted entry change, so
//! comments, blank lines, anchors and the file's own layout survive.
//! Each splice is checked by re-parsing: the result must hold exactly
//! the values Go's tree edit produces ([`tree`]), and every comment,
//! anchor and alias outside the bytes the splice dropped must still be
//! there. A shape the splicer does not handle, or a check that fails,
//! is refused with [`ErrorKind::EditUnsupported`] and the file is not
//! written; nothing is ever re-emitted whole.
//!
//! The one deliberate difference from Go is bytes: values and kept
//! comments match, key order is stable, but untouched text keeps its
//! original formatting rather than yaml.v3's re-emitted layout.

mod splice;
pub(crate) mod tree;

use super::getter::node_to_value;
use super::yaml::{emit, parse, Kind, Node, Style};
use super::{Error, ErrorKind, Map, Value};

fn value_of(root: Option<&Node>) -> Value {
    match root {
        Some(n) => node_to_value(n),
        None => Value::Map(Map::new()),
    }
}

/// Returns `src` with `key` set to `value`.
pub(crate) fn set(src: &str, key: &str, value: &Node) -> Result<String, Error> {
    if src.trim().is_empty() {
        let mut root = Node::map(Vec::new());
        tree::set(&mut root, key, value.clone());
        return Ok(emit::document(&root, 2));
    }
    let Some(root) = parse(src)? else {
        // Comments only: keep them, append the new mapping.
        let mut fresh = Node::map(Vec::new());
        tree::set(&mut fresh, key, value.clone());
        let sep = if src.ends_with('\n') { "" } else { "\n" };
        return Ok(format!("{src}{sep}{}", emit::document(&fresh, 2)));
    };
    if !matches!(root.kind, Kind::Map(_)) {
        return Err(Error::invalid("document root is not a mapping"));
    }
    let mut expected = root.clone();
    tree::set(&mut expected, key, value.clone());
    checked(src, &root, splice::set(src, &root, key, value), &expected)
}

/// Returns `src` without `key`, pruning ancestors left empty.
/// [`ErrorKind::KeyNotFound`] when absent.
pub(crate) fn unset(src: &str, key: &str) -> Result<String, Error> {
    let not_found = || Error::new(ErrorKind::KeyNotFound, "config: key not found");
    let root = parse(src)?.ok_or_else(not_found)?;
    let mut expected = root.clone();
    if !tree::unset(&mut expected, key) {
        return Err(not_found());
    }
    checked(src, &root, splice::unset(src, &root, key), &expected)
}

fn refused() -> Error {
    Error::new(
        ErrorKind::EditUnsupported,
        "config: cannot edit this file in place without losing comments; file left unchanged",
    )
}

/// The spliced text, when it re-parses to the expected values and keeps
/// every comment, anchor and alias outside the dropped region.
fn checked(
    src: &str,
    root: &Node,
    spliced: Option<(String, splice::Region)>,
    expected: &Node,
) -> Result<String, Error> {
    let (text, (from, to)) = spliced.ok_or_else(refused)?;
    let parsed = parse(&text).map_err(|_| refused())?;
    if value_of(parsed.as_ref()) != value_of(Some(expected)) {
        return Err(refused());
    }
    let mut kept: std::collections::HashMap<&str, usize> = std::collections::HashMap::new();
    for (_, t) in tokens(&text, parsed.as_ref()) {
        *kept.entry(t).or_default() += 1;
    }
    for (at, t) in tokens(src, Some(root)) {
        if (from..to).contains(&at) {
            continue;
        }
        match kept.get_mut(t) {
            Some(n) if *n > 0 => *n -= 1,
            _ => return Err(refused()),
        }
    }
    Ok(text)
}

/// Comments (`# ...` to the line end), anchors (`&a`) and aliases
/// (`*a`) in `src`, with their offsets: the text a splice must not lose.
/// Scalars are skipped (a `#` inside a quoted or block scalar is
/// content); a block scalar's header line is not part of it.
fn tokens<'a>(src: &'a str, root: Option<&Node>) -> Vec<(usize, &'a str)> {
    fn scalars(src: &str, n: &Node, out: &mut Vec<(usize, usize)>) {
        match &n.kind {
            Kind::Scalar(_) => {
                if let Some((s, e)) = n.span {
                    let s = match n.style {
                        Style::Literal | Style::Folded => {
                            src[s..].find('\n').map_or(e, |i| (s + i).min(e))
                        }
                        _ => s,
                    };
                    out.push((s, e));
                }
            }
            Kind::Seq(items) => items.iter().for_each(|i| scalars(src, i, out)),
            Kind::Map(entries) => entries.iter().for_each(|(k, v)| {
                scalars(src, k, out);
                scalars(src, v, out);
            }),
            Kind::Alias(_) => {}
        }
    }
    let mut spans = Vec::new();
    if let Some(r) = root {
        scalars(src, r, &mut spans);
    }
    spans.sort_unstable();
    // Scalars do not nest: the last span starting at or before `i`.
    let inside = |i: usize| {
        let k = spans.partition_point(|&(s, _)| s <= i);
        k > 0 && i < spans[k - 1].1
    };
    let b = src.as_bytes();
    let mut out = Vec::new();
    let mut i = 0;
    while i < b.len() {
        let prev = if i == 0 { b'\n' } else { b[i - 1] };
        let starts = match b[i] {
            b'#' => matches!(prev, b' ' | b'\t' | b'\n' | b'\r'),
            b'&' | b'*' => matches!(prev, b' ' | b'\t' | b'\n' | b'\r' | b'[' | b'{' | b','),
            _ => false,
        };
        if !starts || inside(i) {
            i += 1;
            continue;
        }
        let end = if b[i] == b'#' {
            src[i..].find(['\r', '\n']).map_or(b.len(), |n| i + n)
        } else {
            src[i..]
                .find([' ', '\t', '\r', '\n', ',', ']', '}'])
                .map_or(b.len(), |n| i + n)
        };
        out.push((i, src[i..end].trim_end()));
        i = end.max(i + 1);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn s(src: &str, key: &str, v: &str) -> String {
        set(src, key, &Node::str(v)).unwrap()
    }

    #[test]
    fn splices_keep_comments_and_layout() {
        let src = "# head\nname: alice # who\n\nport: 1   # p\n";
        assert_eq!(
            s(src, "name", "bob"),
            "# head\nname: bob # who\n\nport: 1   # p\n"
        );
        assert_eq!(
            s(src, "extra", "x"),
            "# head\nname: alice # who\n\nport: 1   # p\nextra: x\n"
        );
    }

    #[test]
    fn nested_creation_and_conversion() {
        let src = "a:\n    b: 1\n";
        assert_eq!(s(src, "a.c.d", "v"), "a:\n    b: 1\n    c:\n        d: v\n");
        assert_eq!(s("a: 1 # c\n", "a.b", "v"), "a: # c\n  b: v\n");
        assert_eq!(s("a:\n  x: 1\nz: 2\n", "a", "s"), "a: s\nz: 2\n");
    }

    #[test]
    fn anchors_survive() {
        assert_eq!(
            s("base: &b 1\nother: *b\n", "base", "2"),
            "base: &b \"2\"\nother: *b\n"
        );
        assert_eq!(
            s("base: &b\n  x: 1\nother: *b\n", "base", "v"),
            "base: &b v\nother: *b\n"
        );
    }

    #[test]
    fn block_and_quoted_scalars_splice() {
        assert_eq!(s("k: |\n  x\n  y\nz: 1\n", "k", "v"), "k: v\nz: 1\n");
        assert_eq!(s("k: 'a' # c\n", "k", "b"), "k: b # c\n");
        assert_eq!(s("k: v\n", "k", "l1\nl2"), "k: |-\n  l1\n  l2\n");
    }

    #[test]
    fn flow_mappings() {
        assert_eq!(s("m: {a: 1}\n", "m.b", "2"), "m: {a: 1, b: \"2\"}\n");
        assert_eq!(s("m: {}\n", "m.b", "x"), "m: {b: x}\n");
    }

    #[test]
    fn unset_prunes_and_keeps_others() {
        assert_eq!(
            unset("a: 1\nb: 2 # c\nd: 3\n", "b").unwrap(),
            "a: 1\nd: 3\n"
        );
        assert_eq!(
            unset("core:\n  only: v\nkeep: 1\n", "core.only").unwrap(),
            "keep: 1\n"
        );
        assert_eq!(unset("m: {a: 1, b: 2}\n", "m.a").unwrap(), "m: {b: 2}\n");
        assert!(unset("a: 1\n", "zz")
            .unwrap_err()
            .is(ErrorKind::KeyNotFound));
    }

    #[test]
    fn empty_and_comment_only_documents() {
        assert_eq!(s("", "k", "v"), "k: v\n");
        assert_eq!(s("  \n\t\n  ", "k", "v"), "k: v\n");
        assert_eq!(s("# only\n", "k", "v"), "# only\nk: v\n");
    }

    fn refuses(r: Result<String, Error>) {
        let e = r.unwrap_err();
        assert!(e.is(ErrorKind::EditUnsupported), "{e}");
    }

    #[test]
    fn flow_value_with_trailing_comment_keeps_it() {
        assert_eq!(
            s("tags: [a, b] # c\nz: 1\n", "tags", "x"),
            "tags: x # c\nz: 1\n"
        );
        assert_eq!(s("m: {a: 1}   # d\n", "m", "x"), "m: x   # d\n");
    }

    #[test]
    fn multi_line_value_on_key_with_comment() {
        assert_eq!(
            s("k: v # c\nz: 1\n", "k", "l1\nl2"),
            "k: |- # c\n  l1\n  l2\nz: 1\n"
        );
        assert_eq!(
            s("k: |- # h\n  a\n  b\nz: 1\n", "k", "x"),
            "k: x # h\nz: 1\n"
        );
    }

    #[test]
    fn explicit_keys_are_refused_untouched() {
        refuses(set("? k\n: v\nz: 1\n", "k", &Node::str("x")));
        refuses(unset("? k\n: v\nz: 1\n", "k"));
    }

    #[test]
    fn flow_empty_values() {
        assert_eq!(s("m: {a: , b: 1}\n", "m.a", "x"), "m: {a: x, b: 1}\n");
        assert_eq!(s("m: {a:, b: 1}\n", "m.a", "x"), "m: {a: x, b: 1}\n");
        assert_eq!(unset("m: {a: , b: 1}\n", "m.a").unwrap(), "m: {b: 1}\n");
    }

    #[test]
    fn keep_chomped_block_scalars() {
        let src = "k: |+\n  x\n\n# c\nz: 1\n";
        assert_eq!(s(src, "k", "y"), "k: y\n\n# c\nz: 1\n");
        assert_eq!(unset(src, "k").unwrap(), "# c\nz: 1\n");
        assert_eq!(unset("k: |+\n  x\n\nz: 1\n", "k").unwrap(), "z: 1\n");
    }

    #[test]
    fn crlf_files_stay_crlf() {
        assert_eq!(s("a: 1\r\nb: 2\r\n", "c", "x"), "a: 1\r\nb: 2\r\nc: x\r\n");
        assert_eq!(
            s("a: 1\r\nb: 2\r\n", "a.k", "x"),
            "a:\r\n  k: x\r\nb: 2\r\n"
        );
        assert_eq!(
            unset("a: 1\r\nb: 2\r\nc: 3\r\n", "b").unwrap(),
            "a: 1\r\nc: 3\r\n"
        );
    }

    #[test]
    fn dangling_alias_is_refused() {
        // Go writes `other: *b` with the anchor gone, a file it cannot
        // read back; the port refuses.
        refuses(unset("base: &b {x: 1}\nother: *b\n", "base.x"));
    }

    #[test]
    fn token_scan_skips_scalar_content() {
        let src = "a: \"x # y\" # c\nb: |\n  # body\n  &x\nc: &n 1\nd: *n\n";
        let root = parse(src).unwrap().unwrap();
        let t: Vec<&str> = tokens(src, Some(&root))
            .into_iter()
            .map(|(_, t)| t)
            .collect();
        assert_eq!(t, ["# c", "&n", "*n"]);
    }

    #[test]
    fn a_splice_that_drops_a_comment_is_refused() {
        let src = "a: 1 # keep\nb: 2\n";
        let root = parse(src).unwrap().unwrap();
        let mut expected = root.clone();
        tree::set(&mut expected, "b", Node::str("3"));
        // Right values, lost comment outside the dropped region.
        let bad = Some(("a: 1\nb: \"3\"\n".to_string(), (14, 15)));
        refuses(checked(src, &root, bad, &expected));
        let good = Some(("a: 1 # keep\nb: \"3\"\n".to_string(), (14, 15)));
        assert!(checked(src, &root, good, &expected).is_ok());
        // A lost anchor counts too.
        let src = "a: &x 1\nb: 2\n";
        let root = parse(src).unwrap().unwrap();
        let mut expected = root.clone();
        tree::set(&mut expected, "b", Node::str("3"));
        refuses(checked(
            src,
            &root,
            Some(("a: 1\nb: \"3\"\n".into(), (11, 12))),
            &expected,
        ));
    }

    #[test]
    fn bom_files_edit_like_go() {
        let src = "\u{feff}name: x # n\nport: 1\n";
        assert_eq!(s(src, "z", "v"), "\u{feff}name: x # n\nport: 1\nz: v\n");
        assert_eq!(unset(src, "name").unwrap(), "\u{feff}port: 1\n");
        assert_eq!(s(src, "name", "y"), "\u{feff}name: y # n\nport: 1\n");
        assert_eq!(
            s("\u{feff}a:\n  b: 1\n", "a.c", "2"),
            "\u{feff}a:\n  b: 1\n  c: \"2\"\n"
        );
    }

    #[test]
    fn keep_chomping_read_from_the_header() {
        // A `+` in the header comment is not an indicator.
        let src = "k: | # a+b\n  x\n\n# c\nz: 1\n";
        assert_eq!(unset(src, "k").unwrap(), "\n# c\nz: 1\n");
        assert_eq!(unset("k: |2+\n  x\n\nz: 1\n", "k").unwrap(), "z: 1\n");
    }
}
