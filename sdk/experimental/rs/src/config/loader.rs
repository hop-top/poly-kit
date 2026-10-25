//! [`load`]: layered file loading into a typed struct. Ports Go
//! `loader.go` and `defaults.go`.

use std::path::Path;

use serde::de::DeserializeOwned;
use serde::Serialize;

use super::merge::merge;
use super::migrate::{migrate, Migration};
use super::yaml::{de::from_node, parse, roundtrip, ser::to_node, Node};
use super::{Error, ErrorKind, Options};

/// Merges every configured layer into `dst`, in order:
///
/// defaults → system → user → project → extra paths (in order) →
/// `env_override` → `overrides`.
///
/// Missing files in the system, user and project slots are skipped
/// (conventional locations); a missing extra path is an error (an
/// explicit `-c <path>`). A file that exists but does not parse or
/// decode fails with `load config <path>: <cause>`.
///
/// Merge rules are yaml.v3's decode-into-existing-value rules: struct
/// fields merge one by one, while scalars, lists and each map entry are
/// replaced; a null clears only `Option`s, lists and maps. Struct types
/// should derive `Default` with `#[serde(default)]` so a partial file
/// decodes (Go zero-fills missing fields implicitly).
pub fn load<T>(dst: &mut T, opts: &Options) -> Result<(), Error>
where
    T: Serialize + DeserializeOwned + 'static,
{
    let mut tree = to_node(dst)?;
    if let Some(defaults) = &opts.defaults {
        apply_layer::<T>(&mut tree, &defaults.0).map_err(|e| e.wrap("apply defaults"))?;
    }
    for path in [
        &opts.system_config_path,
        &opts.user_config_path,
        &opts.project_config_path,
    ] {
        if path.as_os_str().is_empty() {
            continue;
        }
        match merge_file::<T>(&mut tree, path, &opts.migrations, opts) {
            Err(e) if e.is(ErrorKind::NotFound) => {}
            Err(e) => return Err(e.wrap(format!("load config {}", path.display()))),
            Ok(()) => {}
        }
    }
    for path in &opts.extra_config_paths {
        merge_file::<T>(&mut tree, path, &opts.migrations, opts)
            .map_err(|e| e.wrap(format!("load config {}", path.display())))?;
    }
    if let Some(f) = &opts.env_override {
        let mut cur: T = from_node(&tree)?;
        f(&mut cur);
        tree = to_node(&cur)?;
    }
    if let Some(ov) = &opts.overrides {
        if !ov.is_empty() {
            let src = roundtrip(ov)
                .map_err(|e| e.wrap("marshal overrides").wrap("apply CLI overrides"))?;
            apply_layer::<T>(&mut tree, &src)
                .map_err(|e| e.wrap("apply overrides").wrap("apply CLI overrides"))?;
        }
    }
    *dst = from_node(&tree)?;
    Ok(())
}

/// Merges one layer and checks it decodes, so a type error is reported
/// against the layer that caused it.
fn apply_layer<T: DeserializeOwned>(tree: &mut Node, layer: &Node) -> Result<(), Error> {
    merge(tree, layer)?;
    from_node::<T>(tree)?;
    Ok(())
}

fn merge_file<T: DeserializeOwned>(
    tree: &mut Node,
    path: &Path,
    migrations: &[Migration],
    opts: &Options,
) -> Result<(), Error> {
    if !migrations.is_empty() {
        let migrated = migrate(path, migrations, &opts.migrate_options)?;
        let node = roundtrip(&migrated)
            .map_err(|e| e.wrap(format!("re-marshal migrated {}", path.display())))?;
        return apply_layer::<T>(tree, &node);
    }
    let data =
        std::fs::read_to_string(path).map_err(|e| super::error::io_error("open", path, &e))?;
    match parse(&data)? {
        Some(node) => apply_layer::<T>(tree, &node),
        None => Ok(()),
    }
}

#[cfg(test)]
mod tests {
    use serde::Deserialize;

    use super::*;

    #[derive(Debug, Default, Serialize, Deserialize, PartialEq)]
    #[serde(default)]
    struct Cfg {
        name: String,
        port: i64,
    }

    #[test]
    fn type_errors_name_the_file() {
        let dir = tempfile::tempdir().unwrap();
        let p = dir.path().join("c.yaml");
        std::fs::write(&p, "port: abc\n").unwrap();
        let mut cfg = Cfg::default();
        let err = load(
            &mut cfg,
            &Options {
                user_config_path: p.clone(),
                ..Options::default()
            },
        )
        .unwrap_err();
        assert_eq!(
            err.to_string(),
            format!(
                "load config {}: yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `abc` into int",
                p.display()
            )
        );
    }
}
