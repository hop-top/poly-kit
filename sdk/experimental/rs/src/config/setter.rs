//! [`set`], [`set_value`], [`unset`]: write one key into a scope's
//! file. Ports Go `setter.go` and `unset.go`; edits keep comments (see
//! the `edit` module).

use std::path::Path;

use serde::Serialize;

use super::edit;
use super::getter::default_cache;
use super::goquote::quote;
use super::yaml::{ser::to_node, Node};
use super::{scope_path, Error, Options, Scope};

fn read_or_empty(path: &Path) -> Result<String, Error> {
    match std::fs::read_to_string(path) {
        Ok(s) => Ok(s),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(String::new()),
        Err(e) => Err(super::error::io_error("open", path, &e)),
    }
}

fn write(path: &Path, text: &str) -> Result<(), Error> {
    if let Some(dir) = path.parent().filter(|d| !d.as_os_str().is_empty()) {
        let mut b = std::fs::DirBuilder::new();
        b.recursive(true);
        #[cfg(unix)]
        std::os::unix::fs::DirBuilderExt::mode(&mut b, 0o750);
        b.create(dir)
            .map_err(|e| super::error::io_error("mkdir", dir, &e))?;
    }
    std::fs::write(path, text).map_err(|e| super::error::io_error("open", path, &e))
}

fn set_node(key: &str, node: &Node, scope: Scope, opts: &Options) -> Result<(), Error> {
    let path = scope_path(opts, scope)?;
    let src = read_or_empty(&path).map_err(|e| e.wrap("config set"))?;
    let text = edit::set(&src, key, node).map_err(|e| e.wrap("config set"))?;
    write(&path, &text).map_err(|e| e.wrap("config set"))?;
    default_cache().invalidate(&path);
    Ok(())
}

/// Writes `value` as a string at dotted `key` in `scope`'s file,
/// creating the file and parents. Like Go, the scalar is a `!!str`:
/// text that would read back as another type is quoted (`"0.9"`,
/// `"true"`), YAML 1.1 lookalikes (`yes`) are not.
pub fn set(key: &str, value: &str, scope: Scope, opts: &Options) -> Result<(), Error> {
    set_node(key, &Node::str(value), scope, opts)
}

/// Writes a typed value at dotted `key`: the YAML tag follows the
/// Rust type (`0.9` → `k: 0.9`, `true` → `k: true`, `None` / unit →
/// `k: null`); strings that would retype are quoted, as are YAML 1.1
/// lookalikes. Maps, lists and structs splice in whole. Pair with
/// [`parse_scalar`](super::parse_scalar) for raw CLI arguments.
pub fn set_value<T: Serialize + ?Sized>(
    key: &str,
    value: &T,
    scope: Scope,
    opts: &Options,
) -> Result<(), Error> {
    let node = to_node(value)
        .map_err(|e| Error::from(e).wrap(format!("config set: encode value for {}", quote(key))))?;
    set_node(key, &node, scope, opts)
}

/// Removes dotted `key` from `scope`'s file, pruning mappings left
/// empty. [`ErrorKind::KeyNotFound`](super::ErrorKind::KeyNotFound)
/// when the key (or the file) is absent.
pub fn unset(key: &str, scope: Scope, opts: &Options) -> Result<(), Error> {
    let path = scope_path(opts, scope)?;
    let shown = path.display().to_string();
    let src = read_or_empty(&path).map_err(|e| e.wrap(format!("config unset {shown}")))?;
    let text = match edit::unset(&src, key) {
        Ok(t) => t,
        Err(e) if e.is(super::ErrorKind::KeyNotFound) => return Err(e.wrap("config unset")),
        Err(e) => return Err(e.wrap(format!("config unset {shown}"))),
    };
    write(&path, &text).map_err(|e| e.wrap(format!("config unset {shown}")))?;
    default_cache().invalidate(&path);
    Ok(())
}
