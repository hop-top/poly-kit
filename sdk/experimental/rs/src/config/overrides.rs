//! `-c` / `--config` tokens: extra file paths and dotted `key=value`
//! overrides. Ports Go `override.go`.

use serde::de::DeserializeOwned;
use serde::Serialize;

use super::goquote::quote;
use super::merge::merge;
use super::yaml::{de::from_node, parse, roundtrip, ser::to_node};
use super::{Error, Map, Value};

/// Options for [`parse_config_args`].
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum ParseOption {
    /// See [`with_project_marker`].
    ProjectMarker(String),
}

/// Resolves a bare-directory `-c <dir>` token to `<dir>/<marker>`; the
/// marker file must exist. The marker must be relative. Empty is a
/// no-op (directories stay an error).
pub fn with_project_marker(marker: impl Into<String>) -> ParseOption {
    ParseOption::ProjectMarker(marker.into())
}

/// Splits raw `-c` tokens into extra config file paths and overrides.
///
/// A token with `=` is an override (`model=o3`, `shell.policy.inherit=all`,
/// `tags=["a","b"]`; the value parses as YAML, falling back to the raw
/// text). A token without `=` must name an existing file. Both groups
/// keep argument order; overrides merge into one nested map, later
/// wins. `None` overrides when there are none.
#[cfg(not(target_arch = "wasm32"))]
pub fn parse_config_args(
    args: &[impl AsRef<str>],
    opts: &[ParseOption],
) -> Result<(Vec<std::path::PathBuf>, Option<Map>), Error> {
    use std::path::{Path, PathBuf};

    let mut paths: Vec<PathBuf> = Vec::new();
    if args.is_empty() {
        return Ok((paths, None));
    }
    let mut marker = String::new();
    for o in opts {
        let ParseOption::ProjectMarker(m) = o;
        marker = m.clone();
    }
    if !marker.is_empty() && Path::new(&marker).is_absolute() {
        return Err(Error::invalid(format!(
            "config: ParseConfigArgs: project marker {} must be a relative path",
            quote(&marker)
        )));
    }
    let mut overrides = Map::new();
    for a in args {
        let a = a.as_ref();
        let Some(eq) = a.find('=') else {
            let meta = std::fs::metadata(a).map_err(|e| {
                if e.kind() == std::io::ErrorKind::NotFound {
                    Error::invalid(format!(
                        "-c {}: not a key=value pair and no such file",
                        quote(a)
                    ))
                } else {
                    super::error::io_error("stat", Path::new(a), &e)
                        .wrap(format!("-c {}", quote(a)))
                }
            })?;
            if !meta.is_dir() {
                paths.push(PathBuf::from(a));
                continue;
            }
            if marker.is_empty() {
                return Err(Error::invalid(format!(
                    "-c {}: path is a directory, expected a file",
                    quote(a)
                )));
            }
            let resolved = super::paths::go_join(Path::new(a), &marker);
            let shown = resolved.display().to_string();
            match std::fs::metadata(&resolved) {
                Ok(m) if m.is_dir() => {
                    return Err(Error::invalid(format!(
                        "-c {}: project marker {} resolved to a directory, expected a file ({})",
                        quote(a),
                        quote(&marker),
                        quote(&shown)
                    )))
                }
                Ok(_) => paths.push(resolved),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                    return Err(Error::invalid(format!(
                        "-c {}: directory has no project marker {} (resolved to {})",
                        quote(a),
                        quote(&marker),
                        quote(&shown)
                    )))
                }
                Err(e) => {
                    return Err(super::error::io_error("stat", &resolved, &e).wrap(format!(
                        "-c {}: stat project marker {}",
                        quote(a),
                        quote(&shown)
                    )))
                }
            }
            continue;
        };
        if eq == 0 {
            return Err(Error::invalid(format!("-c {}: empty key", quote(a))));
        }
        let key = a[..eq].trim();
        set_dotted_key(&mut overrides, key, parse_value(&a[eq + 1..]))
            .map_err(|e| e.wrap(format!("-c {}", quote(a))))?;
    }
    Ok((paths, (!overrides.is_empty()).then_some(overrides)))
}

/// Override pairs only; bare tokens are an error. Go marks this
/// deprecated in favour of [`parse_config_args`].
pub fn parse_overrides(pairs: &[impl AsRef<str>]) -> Result<Option<Map>, Error> {
    if pairs.is_empty() {
        return Ok(None);
    }
    let mut out = Map::new();
    for p in pairs {
        let p = p.as_ref();
        let eq = match p.find('=') {
            Some(i) if i > 0 => i,
            _ => {
                return Err(Error::invalid(format!(
                    "override {}: expected key=value",
                    quote(p)
                )))
            }
        };
        set_dotted_key(&mut out, p[..eq].trim(), parse_value(&p[eq + 1..]))
            .map_err(|e| e.wrap(format!("override {}", quote(p))))?;
    }
    Ok(Some(out))
}

/// The raw text as YAML, or the text itself when it does not parse.
/// Empty text is null (yaml.v3 leaves `any` nil).
pub(crate) fn parse_value(raw: &str) -> Value {
    match parse(raw) {
        Ok(None) => Value::Null,
        Ok(Some(node)) => from_node(&node).unwrap_or_else(|_| Value::String(raw.to_string())),
        Err(_) => Value::String(raw.to_string()),
    }
}

/// Go's `%T` for a decoded `any`.
pub(crate) fn go_type_name(v: &Value) -> &'static str {
    match v {
        Value::Null => "<nil>",
        Value::Bool(_) => "bool",
        Value::Int(_) => "int",
        Value::Uint(_) => "uint64",
        Value::Float(_) => "float64",
        Value::String(_) => "string",
        Value::Seq(_) => "[]interface {}",
        Value::Map(_) => "map[string]interface {}",
    }
}

/// Writes `val` at the dotted `key`, creating intermediate maps. A
/// non-map in the way is a conflict, not a clobber.
pub(crate) fn set_dotted_key(dst: &mut Map, key: &str, val: Value) -> Result<(), Error> {
    if key.is_empty() {
        return Err(Error::invalid("empty key"));
    }
    let segs: Vec<&str> = key.split('.').collect();
    let (leaf, parents) = segs
        .split_last()
        .expect("split yields at least one segment");
    let mut cur = dst;
    for (i, seg) in parents.iter().enumerate() {
        let next = cur
            .entry((*seg).to_string())
            .or_insert_with(|| Value::Map(Map::new()));
        match next {
            Value::Map(m) => cur = m,
            other => {
                return Err(Error::invalid(format!(
                    "conflict at {}: expected map, got {}",
                    quote(&segs[..=i].join(".")),
                    go_type_name(other)
                )))
            }
        }
    }
    cur.insert((*leaf).to_string(), val);
    Ok(())
}

/// Merges `overrides` onto `dst` with the same rules as a file layer:
/// struct fields merge, map entries and scalars and lists replace. An
/// empty map is a no-op.
pub fn apply_overrides<T: Serialize + DeserializeOwned>(
    dst: &mut T,
    overrides: &Map,
) -> Result<(), Error> {
    if overrides.is_empty() {
        return Ok(());
    }
    let src = roundtrip(overrides).map_err(|e| e.wrap("marshal overrides"))?;
    let mut tree = to_node(dst).map_err(|e| Error::from(e).wrap("apply overrides"))?;
    merge(&mut tree, &src).map_err(|e| Error::from(e).wrap("apply overrides"))?;
    *dst = from_node(&tree).map_err(|e| Error::from(e).wrap("apply overrides"))?;
    Ok(())
}
