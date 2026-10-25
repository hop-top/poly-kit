//! Kit CLI surface on clap. Gated behind the `cli` feature.
//!
//! Today this is the part of Go `console/cli` that shell completion
//! needs: [`Root`], a wrapper over a finished clap command tree that adds
//! the hidden `completion bash|zsh|fish|powershell` command, root aliases
//! and flag value enums, and answers the `__complete` requests the
//! generated scripts make; and [`completion`], the completer API.

mod alias;
pub mod completion;
mod enums;
mod root;

pub use alias::{AliasError, SHIM_ARGS};
pub use root::Root;
