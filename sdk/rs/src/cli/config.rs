//! Construction options of a kit root: Go `cli.Config` and the parts of
//! it a kit-rs CLI author sets. kit-owned types; nothing here names clap.

use std::fmt;
use std::path::PathBuf;
use std::sync::Arc;

pub use super::validate::{PassthroughStrictness, SignatureStrictness, ValidationFailureMode};

/// Resolves a `-C <target>` that is not an existing directory
/// (shortnames, a project registry, fuzzy lookup). Without one, such a
/// target is an error.
#[derive(Clone)]
pub struct ChdirResolver(Arc<ResolveFn>);

type ResolveFn = dyn Fn(&str) -> Result<PathBuf, String> + Send + Sync;

impl ChdirResolver {
    /// A resolver from `f`: the target in, the directory or the
    /// reason out.
    pub fn new<F>(f: F) -> ChdirResolver
    where
        F: Fn(&str) -> Result<PathBuf, String> + Send + Sync + 'static,
    {
        ChdirResolver(Arc::new(f))
    }

    pub(super) fn resolve(&self, target: &str) -> Result<PathBuf, String> {
        (self.0)(target)
    }
}

impl fmt::Debug for ChdirResolver {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("ChdirResolver")
    }
}

/// Which built-in persistent flags to suppress. The zero value keeps
/// every flag, as in Go's `cli.Disable`.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Disable {
    /// `--format` and the rest of the output flag suite.
    pub format: bool,
    /// `--quiet`.
    pub quiet: bool,
    /// `--no-color`.
    pub no_color: bool,
    /// `--no-hints`.
    pub hints: bool,
    /// `-C/--chdir`.
    pub chdir: bool,
    /// `-c/--config`.
    pub config: bool,
    /// The global `--dry-run`.
    pub dry_run: bool,
}

/// The value type of a tool-specific persistent flag.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum GlobalKind {
    /// Takes a string value.
    #[default]
    String,
    /// A switch.
    Bool,
    /// Takes an integer value.
    Int,
}

/// A tool-specific persistent flag (Go `cli.Flag`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct GlobalFlag {
    /// Long name without the dashes.
    pub name: String,
    /// Optional one-character short name.
    pub short: Option<char>,
    /// Description shown in help.
    pub usage: String,
    /// Default value; `None` for none.
    pub default: Option<String>,
    /// The value type.
    pub kind: GlobalKind,
}

/// A command group of the root help (Go `cli.GroupConfig`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct GroupConfig {
    /// The group id, also the `--help-<id>` suffix.
    pub id: String,
    /// The section title.
    pub title: String,
    /// Hidden from default help; shown by `--help-all`.
    pub hidden: bool,
}

/// What a kit root is built from (Go `cli.Config`).
#[derive(Clone, Debug, Default)]
pub struct Config {
    /// The binary name.
    pub name: String,
    /// The version `--version` prints; a leading `v` is dropped.
    pub version: String,
    /// The one-line description.
    pub short: String,
    /// A paragraph shown on the root help after the description and
    /// before the command sections (Go `HelpConfig.Disclaimer`). Empty
    /// adds nothing; subcommand help does not carry it.
    pub disclaimer: String,
    /// Built-in flags to suppress.
    pub disable: Disable,
    /// Extra persistent flags.
    pub globals: Vec<GlobalFlag>,
    /// Extra command groups beyond the built-in hidden `management`.
    pub groups: Vec<GroupConfig>,
    /// Skips the pre-flight [`Root::validate`](super::Root::validate)
    /// that `execute` runs by default (Go `DisableValidate`).
    pub disable_validate: bool,
    /// How `execute` reports a validation failure.
    pub validation_failure: ValidationFailureMode,
    /// What the signature validators do with a violation.
    pub signature_strictness: SignatureStrictness,
    /// What `kit/passthrough` does under validation.
    pub passthrough_strictness: PassthroughStrictness,
    /// Resolves a `-C` target that is not a directory.
    pub chdir_resolver: Option<ChdirResolver>,
    /// The relative path inside a project directory that holds its
    /// config file: a bare-directory `-c <dir>` resolves to
    /// `<dir>/<marker>`. Empty keeps directories an error.
    pub project_marker: String,
    /// Rejects opted-out `--dry-run` leaves without a rationale.
    pub enforce_dry_run_rationale: bool,
    /// Rejects destructive leaves without a typed-token requirement.
    pub enforce_destructive_token: bool,
    /// Rejects leaves without examples and next steps.
    pub enforce_guidance: bool,
    /// Cap on depth-1 leaves; 0 is the default (10).
    pub max_top_level_verbs: usize,
    /// Cap on tree depth; 0 is the default (3), at most 5.
    pub max_hierarchy_depth: usize,
}

impl Config {
    /// A config with a name, version and description.
    pub fn new(name: &str, version: &str, short: &str) -> Config {
        Config {
            name: name.into(),
            version: version.into(),
            short: short.into(),
            ..Config::default()
        }
    }
}
