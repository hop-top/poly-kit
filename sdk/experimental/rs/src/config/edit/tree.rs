//! Node-tree edits with Go's semantics (`walkOrCreate`,
//! `setNodeInMapping`, `removeFromMapping`, `cleanEmptyMappings`).
//! They define the expected result of an edit; the text splicer must
//! reproduce it, and the whole-document fallback emits it.

use crate::config::yaml::{Kind, Node};

fn entries_mut(n: &mut Node) -> Option<&mut Vec<(Node, Node)>> {
    match &mut n.kind {
        Kind::Map(e) => Some(e),
        _ => None,
    }
}

/// Nested mappings for `segs` around `leaf`: `["a", "b"]` → `{a: {b: leaf}}`.
pub(crate) fn nest(segs: &[&str], leaf: Node) -> Node {
    segs.iter()
        .rev()
        .fold(leaf, |acc, seg| Node::map(vec![(Node::str(*seg), acc)]))
}

/// Go `walkOrCreate` + `setNodeInMapping`: intermediate keys are
/// created, a non-mapping in the way becomes a mapping, the leaf is
/// replaced or appended. A non-mapping root is left unchanged (Go's
/// encoder drops what it appends there).
pub(crate) fn set(root: &mut Node, key: &str, value: Node) {
    let segs: Vec<&str> = key.split('.').collect();
    let mut cur = root;
    for (i, seg) in segs.iter().enumerate() {
        let Some(entries) = entries_mut(cur) else {
            return;
        };
        let last = i + 1 == segs.len();
        let pos = entries
            .iter()
            .position(|(k, _)| k.scalar_value() == Some(seg));
        let pos = match pos {
            Some(p) => p,
            None => {
                entries.push((Node::str(*seg), nest(&segs[i + 1..], value)));
                return;
            }
        };
        let slot = &mut entries[pos].1;
        if last {
            *slot = value;
            return;
        }
        if !matches!(slot.kind, Kind::Map(_)) {
            *slot = Node::map(Vec::new());
        }
        cur = slot;
    }
}

/// Go `Unset` on the tree: removes the leaf, then prunes ancestors left
/// empty (never the root). False when the key is absent.
pub(crate) fn unset(root: &mut Node, key: &str) -> bool {
    let parts: Vec<&str> = key.split('.').collect();
    let (leaf, parents) = parts.split_last().expect("split yields one segment");
    if !remove_at(root, parents, leaf) {
        return false;
    }
    for depth in (1..=parents.len()).rev() {
        let path = &parents[..depth];
        let empty =
            walk(root, path).is_some_and(|n| matches!(&n.kind, Kind::Map(e) if e.is_empty()));
        if !empty {
            break;
        }
        remove_at(root, &path[..depth - 1], path[depth - 1]);
    }
    true
}

fn walk<'a>(root: &'a Node, path: &[&str]) -> Option<&'a Node> {
    let mut cur = root;
    for p in path {
        let Kind::Map(entries) = &cur.kind else {
            return None;
        };
        cur = entries
            .iter()
            .find(|(k, _)| k.scalar_value() == Some(p))
            .map(|(_, v)| v)?;
    }
    Some(cur)
}

fn remove_at(root: &mut Node, parents: &[&str], leaf: &str) -> bool {
    let mut cur = root;
    for p in parents {
        let Some(entries) = entries_mut(cur) else {
            return false;
        };
        let Some(pos) = entries
            .iter()
            .position(|(k, _)| k.scalar_value() == Some(p))
        else {
            return false;
        };
        cur = &mut entries[pos].1;
    }
    let Some(entries) = entries_mut(cur) else {
        return false;
    };
    match entries
        .iter()
        .position(|(k, _)| k.scalar_value() == Some(leaf))
    {
        Some(pos) => {
            entries.remove(pos);
            true
        }
        None => false,
    }
}
