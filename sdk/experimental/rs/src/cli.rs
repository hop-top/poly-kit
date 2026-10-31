//! Kit CLI surface on clap. Gated behind the `cli` feature.
//!
//! Today this is the part of Go `console/cli` that shell completion
//! needs: [`Root`], a wrapper over a finished clap command tree that adds
//! the hidden `completion bash|zsh|fish|powershell` command, root aliases
//! and flag value enums, and answers the `__complete` requests the
//! generated scripts make; [`completion`], the completer API; and
//! [`completion_spec`], the `completion fig|usage` spec exports.

mod alias;
pub mod completion;
pub mod completion_spec;
pub mod config;
mod enums;
pub mod model;
mod root;
mod run;
mod usage;

pub use alias::{
    detect_cycles, validate_alias_name, AliasError, AliasHelp, AliasNameError, AliasNameErrorKind,
    AliasNameRules, AliasOptions, SHIM_ARGS,
};
pub use config::{Config, Disable, GlobalFlag, GlobalKind, GroupConfig};
pub use model::{ArgInfo, CommandInfo, FlagInfo, FlagKind, SIDE_EFFECT};
pub use root::Root;
pub use run::{Globals, HandlerFn, Invocation, AUTOCORRECT_ENV};
pub use usage::AutocorrectMode;
