//! Kit CLI surface. Gated behind the `cli` feature.
//!
//! Ports Go `console/cli`: [`Root`], the kit root built from a
//! [`CommandSpec`] tree (the kit-owned builder an adopter defines a CLI
//! with; clap is the parsing provider behind it and never appears in the
//! API), with the persistent flags, help groups, flag-error surfaces,
//! autocorrect, delegation gating, validators, aliases and the
//! hidden `completion` command; [`completion`], the completer API; and
//! [`completion_spec`], the `completion fig|usage` spec exports.

// `CliError` is the crate's one error envelope, returned by value across
// the root; boxing it here would differ from every other module.
#![allow(clippy::result_large_err)]

mod adapter;
mod alias;
#[cfg(test)]
mod alias_conformance;
mod alias_store;
mod autocorrect_prompt;
pub mod completion;
pub mod completion_spec;
pub mod config;
mod enums;
mod gate;
pub mod model;
mod paths_cmd;
mod plan;
pub mod policy;
mod root;
mod run;
mod spec;
mod usage;
mod validate;
mod validate_flag;

pub use alias::{
    detect_cycles, validate_alias_name, AliasError, AliasHelp, AliasNameError, AliasNameErrorKind,
    AliasNameRules, AliasOptions, SHIM_ARGS,
};
pub use alias_store::{alias_command, aliases_command, AliasStore, SharedAliasStore};
pub use config::{ChdirResolver, Config, Disable, GlobalFlag, GlobalKind, GroupConfig};
pub use gate::{destructive_token, PromptSource, Prompter};
pub use model::{
    inferred_destructive, ArgInfo, CommandInfo, FlagInfo, FlagKind, DESTRUCTIVE_NAMES, SIDE_EFFECT,
};
pub use paths_cmd::{config_command, ConfigResolver, ResolvedPath};
pub use plan::{Effect, Plan};
pub use policy::{default_policy_loader, Policy, PolicyLoader};
pub use root::Root;
pub use run::{Globals, HandlerFn, Invocation, AUTOCORRECT_ENV};
pub use spec::{ArgSpec, CommandSpec, FlagSpec, ValueSpec};
pub use usage::AutocorrectMode;
pub use validate::{
    PassthroughStrictness, SignatureReport, SignatureStrictness, SignatureViolation,
    ValidationError, ValidationFailureMode,
};
pub use validate_flag::FlagValidator;
