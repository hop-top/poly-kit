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
mod enums;
mod root;

pub use alias::{AliasError, SHIM_ARGS};
pub use root::Root;
