//! Root aliases. Ports Go `console/cli/alias.go`: validation with Go's
//! error texts, a clap alias for a direct child and Go's flagless shim
//! for a deeper command.

use std::collections::BTreeMap;
use std::fmt;

use clap::{Arg, Command};

use super::root::{resolve_path, Root};

/// The positional arguments a deep-alias shim accepts, unparsed.
pub const SHIM_ARGS: &str = "args";

/// An alias that cannot be registered. The texts are Go's.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AliasError(String);

impl fmt::Display for AliasError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for AliasError {}

impl Root {
    /// Registers `name` as a root-level alias for the command at `target`
    /// (a space-separated path). A direct child gains a clap alias. A
    /// deeper command gets Go's shim: a hidden root subcommand named
    /// `name` with none of the target's flags, taking any positional
    /// arguments, which the caller dispatches through
    /// [`Root::alias_target`].
    pub fn alias(&mut self, name: &str, target: &str) -> Result<(), AliasError> {
        if name.is_empty() || name.contains([' ', '\t', '\n']) {
            return Err(AliasError(format!(
                "alias name {name:?} must be non-empty without whitespace"
            )));
        }
        if let Some(c) = self.cmd.get_subcommands().find(|c| c.get_name() == name) {
            return Err(AliasError(format!(
                "alias {name:?} collides with command {:?}",
                c.get_name()
            )));
        }
        if let Some(t) = self.aliases.get(name) {
            return Err(AliasError(format!(
                "alias {name:?} already registered for {t:?}"
            )));
        }
        let words = resolve_path(&self.cmd, target).map_err(AliasError)?;
        let path = words.join(" ");
        let cmd = std::mem::take(&mut self.cmd);
        self.cmd = if words.len() == 1 {
            cmd.mut_subcommand(&words[0], |c| c.alias(name.to_string()))
        } else {
            let shim = Command::new(name.to_string())
                .about(format!("Alias for {path}"))
                .hide(true)
                .arg(Arg::new(SHIM_ARGS).num_args(0..).hide(true));
            cmd.subcommand(shim)
        };
        self.aliases.insert(name.to_string(), path);
        Ok(())
    }

    /// Every registered alias with its target path.
    pub fn aliases(&self) -> &BTreeMap<String, String> {
        &self.aliases
    }

    /// The target path of alias `name`.
    pub fn alias_target(&self, name: &str) -> Option<&str> {
        self.aliases.get(name).map(String::as_str)
    }
}
