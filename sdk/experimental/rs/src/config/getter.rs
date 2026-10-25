//! [`get`] and [`list`]: read keys straight from the layer files, with
//! types from each scalar's YAML tag. Ports Go `getter.go`, `list.go`
//! and `node.go`.

use std::collections::{BTreeMap, HashMap};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, OnceLock};

use super::yaml::resolve::{resolve, Scalar, BOOL_TAG, FLOAT_TAG, INT_TAG, NULL_TAG};
use super::yaml::{parse, Kind, Node};
use super::{Error, ErrorKind, Map, Options, Scope, Value};

/// Parsed documents by path. Like Go's package-level cache, entries
/// live until [`set`](super::set) / [`unset`](super::unset) on that
/// path invalidates them; missing files are not cached.
pub(crate) struct NodeCache {
    nodes: Mutex<HashMap<PathBuf, Arc<Option<Node>>>>,
}

impl NodeCache {
    pub(crate) fn new() -> NodeCache {
        NodeCache {
            nodes: Mutex::new(HashMap::new()),
        }
    }

    /// The parsed document, `Ok(None)` when the file does not exist.
    pub(crate) fn get(&self, path: &Path) -> Result<Option<Arc<Option<Node>>>, Error> {
        let mut nodes = self.nodes.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(n) = nodes.get(path) {
            return Ok(Some(n.clone()));
        }
        let data = match std::fs::read_to_string(path) {
            Ok(d) => d,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(e) => return Err(super::error::io_error("open", path, &e)),
        };
        let doc = Arc::new(parse(&data)?);
        nodes.insert(path.to_path_buf(), doc.clone());
        Ok(Some(doc))
    }

    #[cfg(test)]
    pub(crate) fn invalidate(&self, path: &Path) {
        self.nodes
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .remove(path);
    }
}

pub(crate) fn default_cache() -> &'static NodeCache {
    static CACHE: OnceLock<NodeCache> = OnceLock::new();
    CACHE.get_or_init(NodeCache::new)
}

/// The node at dotted `key`, through mappings only (an alias is not
/// followed, as in Go's `walkPath`).
pub(crate) fn walk_path<'a>(doc: &'a Node, key: &str) -> Option<&'a Node> {
    let mut cur = doc;
    for part in key.split('.') {
        let Kind::Map(entries) = &cur.kind else {
            return None;
        };
        cur = entries
            .iter()
            .find(|(k, _)| k.scalar_value() == Some(part))
            .map(|(_, v)| v)?;
    }
    Some(cur)
}

/// The node as a [`Value`], typed by tag: `!!int` → `Int`, `!!float` →
/// `Float`, `!!bool` → `Bool`, `!!null` → `Null`; every other tag
/// (`!!str`, `!!timestamp`, `!!binary`, custom) yields the source text.
/// An `!!int` beyond `i64` and an explicitly tagged value that does not
/// convert also yield the text. Aliases yield `Null`, as in Go.
pub(crate) fn node_to_value(n: &Node) -> Value {
    match &n.kind {
        Kind::Scalar(v) => scalar_to_value(n, v),
        Kind::Seq(items) => Value::Seq(items.iter().map(node_to_value).collect()),
        Kind::Map(entries) => Value::Map(
            entries
                .iter()
                .map(|(k, v)| {
                    (
                        k.scalar_value().unwrap_or_default().to_string(),
                        node_to_value(v),
                    )
                })
                .collect::<Map>(),
        ),
        Kind::Alias(_) => Value::Null,
    }
}

fn scalar_to_value(n: &Node, text: &str) -> Value {
    if !matches!(n.tag.as_str(), INT_TAG | FLOAT_TAG | BOOL_TAG | NULL_TAG) {
        return Value::String(text.to_string());
    }
    let tag = if n.tagged { n.tag.as_str() } else { "" };
    match resolve(tag, text) {
        Ok((_, Scalar::Int(i))) => Value::Int(i),
        Ok((_, Scalar::Float(f))) => Value::Float(f),
        Ok((_, Scalar::Bool(b))) => Value::Bool(b),
        Ok((_, Scalar::Null)) => Value::Null,
        _ => Value::String(text.to_string()),
    }
}

fn layer_paths(opts: &Options) -> [(Scope, &PathBuf); 3] {
    [
        (Scope::System, &opts.system_config_path),
        (Scope::User, &opts.user_config_path),
        (Scope::Project, &opts.project_config_path),
    ]
}

/// The value at dotted `key` from the highest layer holding it
/// (project, then user, then system). Missing files are skipped;
/// [`ErrorKind::KeyNotFound`] when no layer has the key.
pub fn get(key: &str, opts: &Options) -> Result<Value, Error> {
    let mut result = None;
    for (_, path) in layer_paths(opts) {
        if path.as_os_str().is_empty() {
            continue;
        }
        let doc = default_cache()
            .get(path)
            .map_err(|e| e.wrap(format!("config get {}", path.display())))?;
        if let Some(Some(node)) = doc.as_deref() {
            if let Some(n) = walk_path(node, key) {
                result = Some(node_to_value(n));
            }
        }
    }
    result.ok_or_else(|| Error::new(ErrorKind::KeyNotFound, "config: key not found"))
}

/// One effective key and the scope it came from.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Entry {
    pub key: String,
    /// The scalar's source text (empty for lists).
    pub value: String,
    pub scope: Scope,
}

/// Leaves of a document as dotted keys: mappings recurse, anything else
/// is a leaf carrying its scalar text.
pub(crate) fn collect_leaves(doc: &Node, prefix: &str, out: &mut Vec<(String, String)>) {
    let Kind::Map(entries) = &doc.kind else {
        return;
    };
    for (k, v) in entries {
        let k = k.scalar_value().unwrap_or_default();
        let full = if prefix.is_empty() {
            k.to_string()
        } else {
            format!("{prefix}.{k}")
        };
        if matches!(v.kind, Kind::Map(_)) {
            collect_leaves(v, &full, out);
        } else {
            out.push((full, v.scalar_value().unwrap_or_default().to_string()));
        }
    }
}

/// Every effective leaf across the layers, later scopes shadowing
/// earlier ones, sorted by key.
pub fn list(opts: &Options) -> Result<Vec<Entry>, Error> {
    let mut merged: BTreeMap<String, Entry> = BTreeMap::new();
    for (scope, path) in layer_paths(opts) {
        if path.as_os_str().is_empty() {
            continue;
        }
        let doc = default_cache()
            .get(path)
            .map_err(|e| e.wrap(format!("config list {}", path.display())))?;
        if let Some(Some(node)) = doc.as_deref() {
            let mut leaves = Vec::new();
            collect_leaves(node, "", &mut leaves);
            for (key, value) in leaves {
                merged.insert(key.clone(), Entry { key, value, scope });
            }
        }
    }
    Ok(merged.into_values().collect())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn doc(src: &str) -> Node {
        parse(src).unwrap().unwrap()
    }

    fn scalar(src: &str) -> Value {
        let d = doc(&format!("v: {src}"));
        node_to_value(walk_path(&d, "v").unwrap())
    }

    #[test]
    fn walk_path_cases() {
        let d = doc("a:\n  b: val");
        assert_eq!(walk_path(&d, "a.b").unwrap().scalar_value(), Some("val"));
        assert!(walk_path(&d, "a.c").is_none());
        let d = doc("name: hello");
        assert_eq!(walk_path(&d, "name").unwrap().scalar_value(), Some("hello"));
    }

    #[test]
    fn node_to_value_scalar_tags() {
        let s = |v: &str| Value::String(v.into());
        let cases: Vec<(&str, Value)> = vec![
            ("8080", Value::Int(8080)),
            ("-3", Value::Int(-3)),
            ("0.9", Value::Float(0.9)),
            ("true", Value::Bool(true)),
            ("false", Value::Bool(false)),
            ("null", Value::Null),
            ("", Value::Null),
            ("hello", s("hello")),
            (r#""8080""#, s("8080")),
            ("yes", s("yes")),
            ("on", s("on")),
            ("off", s("off")),
            ("no", s("no")),
            ("0x1F", Value::Int(31)),
            ("0o17", Value::Int(15)),
            ("1_000", Value::Int(1000)),
            ("1e3", Value::Float(1000.0)),
            ("2024-01-01", s("2024-01-01")),
            ("2024-01-01T10:20:30Z", s("2024-01-01T10:20:30Z")),
            ("!!timestamp 2024-01-01", s("2024-01-01")),
            ("!!binary aGk=", s("aGk=")),
            (r#"!!binary "not!base64""#, s("not!base64")),
            ("!mytype foo", s("foo")),
            ("!<tag:example.com,2024:x> foo", s("foo")),
            ("9223372036854775808", s("9223372036854775808")),
            ("9223372036854775807", Value::Int(i64::MAX)),
            ("!!int notanum", s("notanum")),
            ("!!bool notabool", s("notabool")),
        ];
        for (src, want) in cases {
            assert_eq!(scalar(src), want, "{src}");
        }
    }

    #[test]
    fn node_to_value_collections() {
        let d = doc(r#"v: [1, 2.5, three, true, null, "4"]"#);
        assert_eq!(
            node_to_value(walk_path(&d, "v").unwrap()),
            Value::Seq(vec![
                Value::Int(1),
                Value::Float(2.5),
                "three".into(),
                Value::Bool(true),
                Value::Null,
                "4".into()
            ])
        );
        let d = doc("v: [2024-01-01, !!binary aGk=, 9223372036854775808]");
        assert_eq!(
            node_to_value(walk_path(&d, "v").unwrap()),
            Value::Seq(vec![
                "2024-01-01".into(),
                "aGk=".into(),
                "9223372036854775808".into()
            ])
        );
        let d = doc("a:\n  b:\n    port: 8080\n    ratio: 0.5\n    on: true\n");
        let v = node_to_value(walk_path(&d, "a").unwrap());
        assert_eq!(v.lookup("b.port"), Some(&Value::Int(8080)));
        assert_eq!(v.lookup("b.ratio"), Some(&Value::Float(0.5)));
        assert_eq!(v.lookup("b.on"), Some(&Value::Bool(true)));
    }

    #[test]
    fn collect_leaves_flattens() {
        let mut out = Vec::new();
        collect_leaves(&doc("a:\n  b: 1\n  c: 2\nd: 3"), "", &mut out);
        let mut keys: Vec<&str> = out.iter().map(|(k, _)| k.as_str()).collect();
        keys.sort();
        assert_eq!(keys, ["a.b", "a.c", "d"]);
    }

    #[test]
    fn cache_caches_and_invalidates() {
        let dir = tempfile::tempdir().unwrap();
        let p = dir.path().join("c.yaml");
        std::fs::write(&p, "x: 1").unwrap();
        let c = NodeCache::new();
        let n1 = c.get(&p).unwrap().unwrap();
        let n2 = c.get(&p).unwrap().unwrap();
        assert!(Arc::ptr_eq(&n1, &n2));
        c.invalidate(&p);
        let n3 = c.get(&p).unwrap().unwrap();
        assert!(!Arc::ptr_eq(&n1, &n3));
    }
}
