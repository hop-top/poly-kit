//! Construction options of a kit root: Go `cli.Config` and the parts of
//! it a kit-rs CLI author sets. kit-owned types; nothing here names clap.

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
    /// Built-in flags to suppress.
    pub disable: Disable,
    /// Extra persistent flags.
    pub globals: Vec<GlobalFlag>,
    /// Extra command groups beyond the built-in hidden `management`.
    pub groups: Vec<GroupConfig>,
    /// Skips the pre-flight [`Root::validate`](super::Root::validate)
    /// that `execute` runs by default (Go `DisableValidate`).
    pub disable_validate: bool,
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
