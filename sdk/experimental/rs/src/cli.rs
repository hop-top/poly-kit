//! Kit CLI surface on clap. Gated behind the `cli` feature.
//!
//! Today this is the part of Go `console/cli` that shell completion
//! needs: [`Root`], a wrapper over a finished clap command tree that adds
//! the hidden `completion bash|zsh|fish|powershell` command, root aliases
//! and flag value enums, and answers the `__complete` requests the
//! generated scripts make; and [`completion`], the completer API.

pub mod completion;
mod root;

pub use root::{AliasError, Root};
