//! Versioned schema migrations. Ports Go `migrate.go`.

use std::fmt;
use std::sync::Arc;

use super::{Error, ErrorKind, Map, Value};

/// Error a migration step returns.
pub type MigrateError = Box<dyn std::error::Error + Send + Sync>;

/// One step: transforms a file's mapping from version `from` to `to`.
pub type MigrateFn = Arc<dyn Fn(Map) -> Result<Map, MigrateError> + Send + Sync>;

/// Hook called once per applied migration, with the file path.
pub type OnMigration = Arc<dyn Fn(&std::path::Path, &Migration) + Send + Sync>;

/// A migration from one schema version to the next.
#[derive(Clone)]
pub struct Migration {
    pub from: i64,
    pub to: i64,
    pub apply: MigrateFn,
    /// Short description for hooks ("rename auth.api_key to auth.token").
    pub note: String,
}

impl fmt::Debug for Migration {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Migration")
            .field("from", &self.from)
            .field("to", &self.to)
            .field("note", &self.note)
            .finish_non_exhaustive()
    }
}

impl Migration {
    pub fn new(
        from: i64,
        to: i64,
        apply: impl Fn(Map) -> Result<Map, MigrateError> + Send + Sync + 'static,
    ) -> Migration {
        Migration {
            from,
            to,
            apply: Arc::new(apply),
            note: String::new(),
        }
    }
}

/// Migration behaviour.
#[derive(Clone, Default)]
pub struct MigrateOptions {
    /// Top-level key holding the version; `schema_version` when empty.
    pub schema_key: String,
    /// Rewrite the file after migrating (off by default).
    pub write_back: bool,
    pub on_migration: Option<OnMigration>,
}

impl fmt::Debug for MigrateOptions {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("MigrateOptions")
            .field("schema_key", &self.schema_key)
            .field("write_back", &self.write_back)
            .field("on_migration", &self.on_migration.is_some())
            .finish()
    }
}

fn schema_key(opts: &MigrateOptions) -> &str {
    if opts.schema_key.is_empty() {
        "schema_version"
    } else {
        &opts.schema_key
    }
}

/// Go `toInt`: ints and floats (truncated); anything else is 0.
fn to_int(v: &Value) -> i64 {
    match v {
        Value::Int(i) => *i,
        Value::Float(f) => *f as i64,
        _ => 0,
    }
}

/// Applies `migrations` to an in-memory mapping while its version is
/// below the highest target. Each step must start at the current
/// version exactly; a gap is an error. Returns the migrated map and the
/// steps applied. The file-free core of [`migrate`] (usable on wasm32).
pub fn apply_migrations(
    mut raw: Map,
    migrations: &[Migration],
    opts: &MigrateOptions,
) -> Result<(Map, Vec<Migration>), Error> {
    let key = schema_key(opts).to_string();
    let mut cur = raw.get(&key).map(to_int).unwrap_or(0);
    let mut sorted = migrations.to_vec();
    sorted.sort_by_key(|m| m.from);
    let highest = sorted.last().map_or(0, |m| m.to);
    let mut applied = Vec::new();
    while cur < highest {
        let Some(next) = sorted.iter().find(|m| m.from == cur) else {
            return Err(Error::new(
                ErrorKind::Migration,
                format!(
                    "no migration from version {cur} (highest target is {highest}); chain has a gap"
                ),
            ));
        };
        raw = (next.apply)(raw).map_err(|e| {
            Error::new(
                ErrorKind::Migration,
                format!("migration {} -> {}: {e}", next.from, next.to),
            )
        })?;
        raw.insert(key.clone(), Value::Int(next.to));
        cur = next.to;
        applied.push(next.clone());
    }
    Ok((raw, applied))
}

/// Reads the YAML file at `path`, applies `migrations` until it reaches
/// the highest target version, optionally writes it back, and returns
/// the migrated mapping. A missing version key is version 0; a file
/// already at the latest version is untouched.
#[cfg(not(target_arch = "wasm32"))]
pub fn migrate(
    path: impl AsRef<std::path::Path>,
    migrations: &[Migration],
    opts: &MigrateOptions,
) -> Result<Map, Error> {
    let path = path.as_ref();
    let raw = read_yaml_map(path)?;
    if migrations.is_empty() {
        return Ok(raw);
    }
    let (migrated, applied) = apply_migrations(raw, migrations, opts)
        .map_err(|e| e.wrap(format!("migrate {}", path.display())))?;
    if let Some(hook) = &opts.on_migration {
        for m in &applied {
            hook(path, m);
        }
    }
    if opts.write_back && !applied.is_empty() {
        write_yaml_map(path, &migrated)
            .map_err(|e| e.wrap(format!("migrate {}: write-back", path.display())))?;
    }
    Ok(migrated)
}

#[cfg(not(target_arch = "wasm32"))]
pub(crate) fn read_yaml_map(path: &std::path::Path) -> Result<Map, Error> {
    let data =
        std::fs::read_to_string(path).map_err(|e| super::error::io_error("open", path, &e))?;
    if data.is_empty() {
        return Ok(Map::new());
    }
    let node = super::yaml::parse(&data)
        .map_err(|e| Error::from(e).wrap(format!("parse {}", path.display())))?;
    let Some(node) = node else {
        return Ok(Map::new());
    };
    let m: Option<Map> = super::yaml::de::from_node(&node)
        .map_err(|e| Error::from(e).wrap(format!("parse {}", path.display())))?;
    Ok(m.unwrap_or_default())
}

#[cfg(not(target_arch = "wasm32"))]
fn write_yaml_map(path: &std::path::Path, raw: &Map) -> Result<(), Error> {
    let node = super::yaml::ser::to_node(raw)?;
    let text = super::yaml::emit::document(&node, 4);
    #[cfg(unix)]
    let mode = {
        use std::os::unix::fs::PermissionsExt;
        std::fs::metadata(path)
            .map(|m| m.permissions().mode() & 0o777)
            .unwrap_or(0o644)
    };
    std::fs::write(path, text).map_err(|e| super::error::io_error("open", path, &e))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(path, std::fs::Permissions::from_mode(mode));
    }
    Ok(())
}
