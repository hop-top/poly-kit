//! Kit CLI surface. Gated behind the `cli` feature.
//!
//! Ports Go `console/cli`: [`Root`], the kit root built from a
//! [`CommandSpec`] tree (the kit-owned builder an adopter defines a CLI
//! with; clap is the parsing provider behind it and never appears in the
//! API), with the persistent flags, help groups, flag-error surfaces,
//! autocorrect, delegation gating, validators, aliases and the
//! hidden `completion` command; [`completion`], the completer API; and
//! [`completion_spec`], the `completion fig|usage` spec exports.

mod adapter;
mod alias;
#[cfg(test)]
mod alias_conformance;
pub mod completion;
pub mod completion_spec;
pub mod config;
mod enums;
pub mod model;
mod root;
mod run;
mod spec;
mod usage;

pub use alias::{
    detect_cycles, validate_alias_name, AliasError, AliasHelp, AliasNameError, AliasNameErrorKind,
    AliasNameRules, AliasOptions, SHIM_ARGS,
};
pub use config::{Config, Disable, GlobalFlag, GlobalKind, GroupConfig};
pub use model::{ArgInfo, CommandInfo, FlagInfo, FlagKind, SIDE_EFFECT};
pub use root::Root;
pub use run::{Globals, HandlerFn, Invocation, AUTOCORRECT_ENV};
pub use spec::{ArgSpec, CommandSpec, FlagSpec, ValueSpec};
pub use usage::AutocorrectMode;
