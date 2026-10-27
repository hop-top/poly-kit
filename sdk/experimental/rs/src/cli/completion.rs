//! Dynamic value completion for kit CLIs. Ports Go
//! `console/cli/completion`.
//!
//! A [`Completer`] turns the word being completed into [`Item`]s.
//! [`bind_flag`] and [`bind_args`] attach one to a flag or to the
//! positional arguments of a command in a [`Root`]; the shell scripts
//! that `completion <shell>` writes call back into the program's hidden
//! `__complete` entry point, which runs the bound completer and prints
//! the result in cobra's wire format (see [`Root::complete`]).
//!
//! # Example
//!
//! ```
//! use clap::Command;
//! use hop_top_kit::cli::completion::{bind_args, prefixed, static_values};
//! use hop_top_kit::cli::Root;
//!
//! let mut root = Root::new(Command::new("tool").subcommand(Command::new("deploy")));
//! bind_args(&mut root, "deploy", prefixed("env", static_values(["prod", "preview", "staging"])));
//!
//! let (mut out, mut err) = (Vec::new(), Vec::new());
//! root.try_builtin(["tool", "__completeNoDesc", "deploy", "env:pr"], &mut out, &mut err)
//!     .unwrap()
//!     .unwrap();
//! assert_eq!(String::from_utf8(out).unwrap(), "env:prod\nenv:preview\n:4\n");
//! ```
//!
//! # Contract
//!
//! - Prefix matching in [`static_items`], [`static_values`],
//!   [`config_keys`] and the dimension half of [`prefixed`] is
//!   case-insensitive. An empty prefix returns every item.
//! - [`prefixed`] with no colon suggests `dimension:` only; with a colon
//!   it delegates the remainder to the inner completer and re-prepends
//!   `dimension:` to each result.
//! - [`resolve`] joins `Item::description` to `Item::value` with a tab.
//!   The directive is [`Directive::NO_FILE_COMP`] on success and
//!   [`Directive::ERROR`] when the completer fails.
//! - [`file()`] and [`dir`] yield a directive, not suggestions: filter by
//!   extension (the extensions travel as the completions), directories
//!   only, or plain file completion when no extension is given.
//! - [`bind_flag`] on an unknown command or flag is silent, and the first
//!   binding of a flag wins, as with cobra's `RegisterFlagCompletionFunc`.
//!
//! # Differences from Go
//!
//! - Completers receive a [`Context`] carrying the command path and the
//!   positional arguments already typed, not a `context.Context`.
//! - [`config_keys`] reads a [`KeySource`]: a key list, or (feature
//!   `config`) a [`crate::config::Value`] tree, in place of a viper
//!   instance. Keys come back sorted; viper's order is unspecified.
//! - Binding is by command path on a [`Root`] (`""` is the root itself),
//!   since a clap `Command` cannot carry closures.
//!
//! [`Root`]: crate::cli::Root
//! [`Root::complete`]: crate::cli::Root::complete

use std::collections::HashMap;
use std::sync::Arc;

use super::Root;

mod args;
mod command;
mod directive;
mod engine;
mod scripts;

pub use directive::{Directive, FileFilter};
pub use scripts::Shell;

pub(crate) use command::{completion_command, COMPLETION, NO_DESC_FLAG};

pub(crate) use engine::complete_request;
pub(crate) use scripts::write_script;

/// Name of the hidden command a shell script calls for completions.
pub const REQUEST_CMD: &str = "__complete";
/// Name of the hidden command that returns completions without descriptions.
pub const NO_DESC_REQUEST_CMD: &str = "__completeNoDesc";

/// A single completion suggestion.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Item {
    /// The text the shell inserts.
    pub value: String,
    /// Shown next to the value by shells that support descriptions.
    pub description: String,
}

impl Item {
    /// An item without a description.
    pub fn new(value: impl Into<String>) -> Item {
        Item {
            value: value.into(),
            description: String::new(),
        }
    }

    /// An item with a description.
    pub fn with_description(value: impl Into<String>, description: impl Into<String>) -> Item {
        Item {
            value: value.into(),
            description: description.into(),
        }
    }
}

/// The error a completer may return; the bridge turns it into
/// [`Directive::ERROR`].
pub type Error = Box<dyn std::error::Error + Send + Sync>;

/// What a completer knows about the command line it completes.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct Context {
    /// Space-separated path of the command below the root; empty for the root.
    pub command_path: String,
    /// Positional arguments already given, flags removed.
    pub args: Vec<String>,
}

impl Context {
    /// A context for `command_path` with `args` already given.
    pub fn new(command_path: impl Into<String>, args: Vec<String>) -> Context {
        Context {
            command_path: command_path.into(),
            args,
        }
    }
}

/// Produces completion suggestions for a prefix.
pub trait Completer: Send + Sync {
    /// The items matching `prefix`.
    fn complete(&self, ctx: &Context, prefix: &str) -> Result<Vec<Item>, Error>;

    /// Set by [`file()`] and [`dir`]: the bridge emits a file directive
    /// instead of the items.
    fn file_filter(&self) -> Option<FileFilter> {
        None
    }
}

impl<C: Completer + ?Sized> Completer for Box<C> {
    fn complete(&self, ctx: &Context, prefix: &str) -> Result<Vec<Item>, Error> {
        (**self).complete(ctx, prefix)
    }
    fn file_filter(&self) -> Option<FileFilter> {
        (**self).file_filter()
    }
}

impl<C: Completer + ?Sized> Completer for Arc<C> {
    fn complete(&self, ctx: &Context, prefix: &str) -> Result<Vec<Item>, Error> {
        (**self).complete(ctx, prefix)
    }
    fn file_filter(&self) -> Option<FileFilter> {
        (**self).file_filter()
    }
}

// --- Static ---

/// Fixed items, filtered by prefix. See [`static_items`].
#[derive(Clone, Debug)]
pub struct Static {
    items: Vec<Item>,
}

/// A completer over fixed items, filtered by case-insensitive prefix.
pub fn static_items(items: impl IntoIterator<Item = Item>) -> Static {
    Static {
        items: items.into_iter().collect(),
    }
}

/// [`static_items`] with description-less items.
pub fn static_values<S: Into<String>>(values: impl IntoIterator<Item = S>) -> Static {
    static_items(values.into_iter().map(Item::new))
}

impl Completer for Static {
    fn complete(&self, _: &Context, prefix: &str) -> Result<Vec<Item>, Error> {
        Ok(filter_prefix(&self.items, prefix))
    }
}

// --- Func ---

/// A callback completer. See [`func`].
pub struct Func<F>(F);

/// Wraps a callback as a completer. The callback sees the raw prefix;
/// nothing is filtered for it.
pub fn func<F>(f: F) -> Func<F>
where
    F: Fn(&Context, &str) -> Result<Vec<Item>, Error> + Send + Sync,
{
    Func(f)
}

impl<F> Completer for Func<F>
where
    F: Fn(&Context, &str) -> Result<Vec<Item>, Error> + Send + Sync,
{
    fn complete(&self, ctx: &Context, prefix: &str) -> Result<Vec<Item>, Error> {
        (self.0)(ctx, prefix)
    }
}

// --- Prefixed ---

/// `dimension:value` completion. See [`prefixed`].
pub struct Prefixed {
    dimension: String,
    values: Box<dyn Completer>,
}

/// Completes `dimension:value` patterns: suggests `dimension:` while no
/// colon is typed, then delegates the part after the first colon to
/// `values`.
pub fn prefixed(dimension: impl Into<String>, values: impl Completer + 'static) -> Prefixed {
    Prefixed {
        dimension: dimension.into(),
        values: Box::new(values),
    }
}

impl Completer for Prefixed {
    fn complete(&self, ctx: &Context, prefix: &str) -> Result<Vec<Item>, Error> {
        let Some(idx) = prefix.find(':') else {
            let dim = format!("{}:", self.dimension);
            if dim.to_lowercase().starts_with(&prefix.to_lowercase()) {
                let desc = format!("{} values", self.dimension);
                return Ok(vec![Item::with_description(dim, desc)]);
            }
            return Ok(Vec::new());
        };
        if !eq_fold(&prefix[..idx], &self.dimension) {
            return Ok(Vec::new());
        }
        let items = self.values.complete(ctx, &prefix[idx + 1..])?;
        Ok(items
            .into_iter()
            .map(|it| Item {
                value: format!("{}:{}", self.dimension, it.value),
                description: it.description,
            })
            .collect())
    }
}

// --- ConfigKeys ---

/// Where [`config_keys`] reads key names.
pub trait KeySource: Send + Sync {
    /// Every key, dotted for nested values.
    fn all_keys(&self) -> Vec<String>;
}

impl KeySource for Vec<String> {
    fn all_keys(&self) -> Vec<String> {
        self.clone()
    }
}

impl KeySource for Vec<&'static str> {
    fn all_keys(&self) -> Vec<String> {
        self.iter().map(|s| s.to_string()).collect()
    }
}

/// The dotted leaf keys of a config tree, lowercased like viper's
/// `AllKeys`, sorted.
#[cfg(feature = "config")]
impl KeySource for crate::config::Value {
    fn all_keys(&self) -> Vec<String> {
        fn walk(
            v: &crate::config::Value,
            prefix: &str,
            out: &mut std::collections::BTreeSet<String>,
        ) {
            match v {
                crate::config::Value::Map(m) => {
                    for (k, child) in m {
                        let key = if prefix.is_empty() {
                            k.to_lowercase()
                        } else {
                            format!("{prefix}.{}", k.to_lowercase())
                        };
                        walk(child, &key, out);
                    }
                }
                _ if !prefix.is_empty() => {
                    out.insert(prefix.to_string());
                }
                _ => {}
            }
        }
        let mut out = std::collections::BTreeSet::new();
        walk(self, "", &mut out);
        out.into_iter().collect()
    }
}

impl<K: KeySource + ?Sized> KeySource for Arc<K> {
    fn all_keys(&self) -> Vec<String> {
        (**self).all_keys()
    }
}

/// Config key names. See [`config_keys`].
pub struct ConfigKeys(Box<dyn KeySource>);

/// Completes config key names read from `source` at completion time.
pub fn config_keys(source: impl KeySource + 'static) -> ConfigKeys {
    ConfigKeys(Box::new(source))
}

impl Completer for ConfigKeys {
    fn complete(&self, _: &Context, prefix: &str) -> Result<Vec<Item>, Error> {
        let items: Vec<Item> = self.0.all_keys().into_iter().map(Item::new).collect();
        Ok(filter_prefix(&items, prefix))
    }
}

// --- File / Dir ---

/// File or directory completion. See [`file()`] and [`dir`].
#[derive(Clone, Debug)]
pub struct File {
    extensions: Vec<String>,
    dir_only: bool,
}

/// Asks the shell for files with these extensions; none means any file.
pub fn file<S: Into<String>>(extensions: impl IntoIterator<Item = S>) -> File {
    File {
        extensions: extensions.into_iter().map(Into::into).collect(),
        dir_only: false,
    }
}

/// Asks the shell for directories only.
pub fn dir() -> File {
    File {
        extensions: Vec::new(),
        dir_only: true,
    }
}

impl Completer for File {
    /// The declared extensions, which travel as the completions of a
    /// filter-by-extension directive. [`dir`] declares none.
    fn complete(&self, _: &Context, _: &str) -> Result<Vec<Item>, Error> {
        Ok(self.extensions.iter().map(Item::new).collect())
    }

    fn file_filter(&self) -> Option<FileFilter> {
        Some(if self.dir_only {
            FileFilter::Dirs
        } else {
            FileFilter::Extensions(self.extensions.clone())
        })
    }
}

// --- Registry ---

/// Maps flag names and argument positions to completers, for sharing
/// completers across a command tree.
#[derive(Default)]
pub struct Registry {
    flags: HashMap<String, Arc<dyn Completer>>,
    args: HashMap<String, HashMap<usize, Arc<dyn Completer>>>,
}

impl Registry {
    /// An empty registry.
    pub fn new() -> Registry {
        Registry::default()
    }

    /// Associates a completer with a flag name.
    pub fn register(&mut self, flag: impl Into<String>, c: impl Completer + 'static) {
        self.flags.insert(flag.into(), Arc::new(c));
    }

    /// Associates a completer with a command name and argument position.
    pub fn register_arg(
        &mut self,
        cmd: impl Into<String>,
        pos: usize,
        c: impl Completer + 'static,
    ) {
        self.args
            .entry(cmd.into())
            .or_default()
            .insert(pos, Arc::new(c));
    }

    /// The completer for `flag`, if any.
    pub fn for_flag(&self, flag: &str) -> Option<Arc<dyn Completer>> {
        self.flags.get(flag).cloned()
    }

    /// The completer for `cmd` at `pos`, if any.
    pub fn for_arg(&self, cmd: &str, pos: usize) -> Option<Arc<dyn Completer>> {
        self.args.get(cmd)?.get(&pos).cloned()
    }
}

// --- Bridge ---

/// Binds `c` as the completion of flag `flag` (its long name, or its id)
/// on the command at `path`. An unknown command or flag is silent; the
/// first binding of a flag wins.
pub fn bind_flag(root: &mut Root, path: &str, flag: &str, c: impl Completer + 'static) {
    root.bind_flag_completer(path, flag, Arc::new(c));
}

/// Binds `c` as the completion of the positional arguments of the
/// command at `path` (`""` is the root). Replaces an earlier binding.
pub fn bind_args(root: &mut Root, path: &str, c: impl Completer + 'static) {
    root.bind_args_completer(path, Arc::new(c));
}

/// Converts completer output to the shell wire format: values with any
/// description joined by a tab, plus the directive.
pub fn resolve(ctx: &Context, c: &dyn Completer, to_complete: &str) -> (Vec<String>, Directive) {
    match c.file_filter() {
        Some(FileFilter::Dirs) => return (Vec::new(), Directive::FILTER_DIRS),
        // An empty filter list would filter every candidate out.
        Some(FileFilter::Extensions(e)) if e.is_empty() => return (Vec::new(), Directive::DEFAULT),
        Some(FileFilter::Extensions(e)) => return (e, Directive::FILTER_FILE_EXT),
        None => {}
    }
    match c.complete(ctx, to_complete) {
        Ok(items) => (
            items.into_iter().map(with_desc).collect(),
            Directive::NO_FILE_COMP,
        ),
        Err(_) => (Vec::new(), Directive::ERROR),
    }
}

/// `value`, or `value<TAB>description` when there is one.
pub(crate) fn with_desc(it: Item) -> String {
    if it.description.is_empty() {
        it.value
    } else {
        format!("{}\t{}", it.value, it.description)
    }
}

/// Case-insensitive prefix filter; an empty prefix keeps everything.
fn filter_prefix(items: &[Item], prefix: &str) -> Vec<Item> {
    if prefix.is_empty() {
        return items.to_vec();
    }
    let lp = prefix.to_lowercase();
    items
        .iter()
        .filter(|it| it.value.to_lowercase().starts_with(&lp))
        .cloned()
        .collect()
}

/// Go `strings.EqualFold` for the cases completion meets.
fn eq_fold(a: &str, b: &str) -> bool {
    a.to_lowercase() == b.to_lowercase()
}
