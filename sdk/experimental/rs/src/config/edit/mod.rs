//! Comment-keeping YAML edits for `set`, `set_value` and `unset`.
//!
//! Go re-emits the whole `yaml.Node` document, which keeps comments
//! because yaml.v3 attaches them to nodes. The port edits the source
//! text instead: only the bytes of the targeted entry change, so
//! comments, blank lines, anchors and the file's own layout survive.
//! Each splice is checked by re-parsing: the result must hold exactly
//! the values Go's tree edit produces ([`tree`]). A shape the splicer
//! does not handle, or a check that fails, falls back to emitting that
//! expected tree as a whole document (comments lost, values right).
//!
//! The one deliberate difference from Go is bytes: values and kept
//! comments match, key order is stable, but untouched text keeps its
//! original formatting rather than yaml.v3's re-emitted layout.

mod splice;
pub(crate) mod tree;

use super::getter::node_to_value;
use super::yaml::{emit, parse, Kind, Node};
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
    Ok(checked(splice::set(src, &root, key, value), &expected))
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
    Ok(checked(splice::unset(src, &root, key), &expected))
}

/// The spliced text when it re-parses to the expected values, else the
/// expected tree emitted whole.
fn checked(spliced: Option<String>, expected: &Node) -> String {
    let want = value_of(Some(expected));
    if let Some(text) = spliced {
        if let Ok(parsed) = parse(&text) {
            if value_of(parsed.as_ref()) == want {
                return text;
            }
        }
    }
    emit::document(expected, 2)
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
}
