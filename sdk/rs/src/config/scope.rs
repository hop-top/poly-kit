//! [`Scope`]: which file layer a write targets. Ports Go `scope.go`.

use std::path::PathBuf;

use super::{Error, ErrorKind, Options};

/// A configuration layer to target.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Scope {
    /// System-wide (`/etc/...`).
    System,
    /// Per-user (`~/.config/...`).
    User,
    /// Project (`.tool.yaml`).
    Project,
}

/// The file path for `scope`. Fails with [`ErrorKind::EmptyScope`]
/// (`config: scope path is empty`) when the slot is unset.
pub fn scope_path(opts: &Options, scope: Scope) -> Result<PathBuf, Error> {
    let p = match scope {
        Scope::System => &opts.system_config_path,
        Scope::User => &opts.user_config_path,
        Scope::Project => &opts.project_config_path,
    };
    if p.as_os_str().is_empty() {
        return Err(Error::new(
            ErrorKind::EmptyScope,
            "config: scope path is empty",
        ));
    }
    Ok(p.clone())
}
