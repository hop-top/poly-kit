//! The minimal clap root that completion hangs on. Ports the parts of Go
//! `console/cli` that shell completion reads: the `completion` command in
//! the management group, plus root aliases (`alias.rs`) and the flag
//! value-enum registry (`enums.rs`).

use std::collections::{BTreeMap, HashMap};
use std::ffi::OsString;
use std::fmt;
use std::io::{self, Write};
use std::sync::Arc;

use clap::{Arg, Command};

use super::completion::{
    self, completion_command, func, resolve, Completer, Context, Directive, Shell, COMPLETION,
    NO_DESC_FLAG, NO_DESC_REQUEST_CMD, REQUEST_CMD,
};
use super::completion_spec::{write_export, FIG, USAGE};
use super::enums::apply_enum_help;

const MANAGEMENT: &str = "management";

/// A clap command tree with kit's completion surface.
///
/// [`Root::new`] takes the finished tree; [`Root::command`] adds the
/// hidden `completion bash|zsh|fish|powershell` command (in the
/// `management` group, which default help hides) unless a command or
/// alias already answers to `completion`.
/// Like Go kit, the root has no `help` subcommand; `-h/--help` remain.
///
/// Wire it in `main` ahead of normal dispatch:
///
/// ```no_run
/// use clap::Command;
/// use hop_top_kit::cli::Root;
///
/// let root = Root::new(Command::new("mytool").subcommand(Command::new("deploy")));
/// let (mut out, mut err) = (std::io::stdout(), std::io::stderr());
/// if let Some(done) = root.try_builtin(std::env::args_os(), &mut out, &mut err) {
///     std::process::exit(if done.is_ok() { 0 } else { 1 });
/// }
/// let matches = root.command().get_matches();
/// # let _ = matches;
/// ```
pub struct Root {
    pub(super) cmd: Command,
    pub(super) aliases: BTreeMap<String, String>,
    pub(super) enums: HashMap<(String, String), Vec<String>>,
    flag_completers: HashMap<(String, String), Arc<dyn Completer>>,
    args_completers: HashMap<String, Arc<dyn Completer>>,
    completion_disabled: bool,
}

impl fmt::Debug for Root {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Root")
            .field("name", &self.cmd.get_name())
            .field("aliases", &self.aliases)
            .finish_non_exhaustive()
    }
}

impl Root {
    /// Wraps a finished command tree.
    pub fn new(cmd: Command) -> Root {
        Root {
            cmd: cmd.disable_help_subcommand(true),
            aliases: BTreeMap::new(),
            enums: HashMap::new(),
            flag_completers: HashMap::new(),
            args_completers: HashMap::new(),
            completion_disabled: false,
        }
    }

    /// Drops the built-in `completion` command, its spec exports
    /// included: cobra's `CompletionOptions.DisableDefaultCmd`.
    /// `__complete` requests are still answered.
    pub fn disable_completion_command(&mut self) -> &mut Root {
        self.completion_disabled = true;
        self
    }

    /// Whether the built-in `completion` command applies: like cobra, it
    /// is added only when no command or alias already answers to
    /// `completion`, so an adopter's own (or an alias) wins.
    fn has_builtin_completion(&self) -> bool {
        !self.completion_disabled
            && !self
                .cmd
                .get_subcommands()
                .any(|c| c.get_name() == COMPLETION || c.get_all_aliases().any(|a| a == COMPLETION))
    }

    /// Whether `path` is the built-in `completion` command or one of its
    /// leaves, which complete no arguments.
    pub(crate) fn is_builtin_completion_path(&self, path: &str) -> bool {
        self.has_builtin_completion() && is_builtin_completion_path(path)
    }

    /// The command tree to parse with: the adopter's tree, alias
    /// registrations, the built-in `completion` command when it applies
    /// and enum help suffixes.
    pub fn command(&self) -> Command {
        let mut cmd = self.cmd.clone();
        if self.has_builtin_completion() {
            cmd = cmd.subcommand(completion_command(self.cmd.get_name()));
        }
        apply_enum_help(cmd, "", &self.enums)
    }

    /// [`Root::command`] with the management group shown, the analogue
    /// of Go's `--help-all`.
    pub fn help_all_command(&self) -> Command {
        let cmd = self.command();
        if self.has_builtin_completion() {
            return cmd.mut_subcommand(COMPLETION, |c| c.hide(false));
        }
        cmd
    }

    /// The help group of a root subcommand: `management` for the
    /// built-in `completion`; `None` for the default group.
    pub fn command_group(&self, name: &str) -> Option<&str> {
        (name == COMPLETION && self.has_builtin_completion()).then_some(MANAGEMENT)
    }

    /// What completing the value of flag `flag` on the command at `path`
    /// returns for `to_complete`: the bound completer, else the enum
    /// values. `None` when neither exists.
    pub fn flag_completion(
        &self,
        path: &str,
        flag: &str,
        to_complete: &str,
    ) -> Option<(Vec<String>, Directive)> {
        let mut built = self.command();
        built.build();
        let words = resolve_path_words(&built, path)?;
        let cmd = find_path(&built, &words)?;
        let arg = cmd.get_arguments().find(|a| flag_name(a) == flag)?;
        let ctx = Context::new(words.join(" "), Vec::new());
        self.run_flag_completion(&words, arg, &ctx, to_complete)
    }

    /// What completing a positional argument of the command at `path`
    /// returns, `args` being those already given: the bound completer,
    /// plus alias names at the root. `None` when neither exists.
    pub fn args_completion(
        &self,
        path: &str,
        args: &[String],
        to_complete: &str,
    ) -> Option<(Vec<String>, Directive)> {
        let path = normalize(path);
        let builtin = self.is_builtin_completion_path(&path);
        // cobra's NoFileCompletions on the command and each shell leaf.
        let none: Arc<dyn Completer> = Arc::new(func(|_: &Context, _: &str| Ok(Vec::new())));
        let bound = if builtin {
            Some(&none)
        } else {
            self.args_completers.get(&path)
        };
        let aliases = path.is_empty() && !self.aliases.is_empty();
        if bound.is_none() && !aliases {
            return None;
        }
        let ctx = Context::new(path, args.to_vec());
        let (mut out, mut directive) = match bound {
            Some(c) => resolve(&ctx, c.as_ref(), to_complete),
            None => (Vec::new(), Directive::DEFAULT),
        };
        if aliases {
            directive = Directive::NO_FILE_COMP | directive;
            for (name, target) in &self.aliases {
                if name.starts_with(to_complete) {
                    out.push(format!("{name}\talias for {target}"));
                }
            }
        }
        Some((out, directive))
    }

    /// Answers a completion request: `args` are the words after
    /// `__complete` (the last is the word being completed). Writes one
    /// completion per line, then `:<directive>`, to `out`, and the
    /// directive's name to `err`, in cobra's format.
    pub fn complete(
        &self,
        args: &[String],
        no_desc: bool,
        out: &mut dyn Write,
        err: &mut dyn Write,
    ) -> io::Result<()> {
        completion::complete_request(self, args, no_desc, out, err)
    }

    /// Writes the `completion <shell>` script for this program.
    pub fn write_script(
        &self,
        shell: Shell,
        include_desc: bool,
        out: &mut dyn Write,
    ) -> io::Result<()> {
        completion::write_script(shell, self.cmd.get_name(), include_desc, out)
    }

    /// Runs the builtins completion needs, before normal dispatch:
    /// `__complete` / `__completeNoDesc` requests and the `completion`
    /// command. `None` when `argv` is neither (or does not parse, so the
    /// caller's own parse reports it).
    pub fn try_builtin<I, T>(
        &self,
        argv: I,
        out: &mut dyn Write,
        err: &mut dyn Write,
    ) -> Option<io::Result<()>>
    where
        I: IntoIterator<Item = T>,
        T: Into<OsString>,
    {
        let argv: Vec<OsString> = argv.into_iter().map(Into::into).collect();
        let first = argv.get(1)?.to_str()?;
        if first == REQUEST_CMD || first == NO_DESC_REQUEST_CMD {
            let words: Vec<String> = argv[2..]
                .iter()
                .map(|a| a.to_string_lossy().into_owned())
                .collect();
            if words.is_empty() {
                return Some(Err(io::Error::new(
                    io::ErrorKind::InvalidInput,
                    "requires at least 1 arg(s), only received 0",
                )));
            }
            return Some(self.complete(&words, first == NO_DESC_REQUEST_CMD, out, err));
        }
        if first != COMPLETION || !self.has_builtin_completion() {
            return None;
        }
        let mut cmd = self.command();
        let matches = cmd.try_get_matches_from_mut(&argv).ok()?;
        let (_, sub) = matches.subcommand()?;
        Some(match sub.subcommand() {
            Some((leaf, _)) if leaf == FIG || leaf == USAGE => write_export(self, leaf, out),
            Some((shell, m)) => match shell.parse::<Shell>() {
                Ok(sh) => self.write_script(sh, !m.get_flag(NO_DESC_FLAG), out),
                Err(e) => Err(io::Error::new(io::ErrorKind::InvalidInput, e)),
            },
            // No shell: show the command's help rather than a script.
            None => {
                cmd.build();
                let help = cmd
                    .find_subcommand_mut(COMPLETION)
                    .map(|c| c.render_long_help().to_string())
                    .unwrap_or_default();
                out.write_all(help.as_bytes())
            }
        })
    }

    // --- crate-internal: binding and lookup ---

    pub(crate) fn bind_flag_completer(&mut self, path: &str, flag: &str, c: Arc<dyn Completer>) {
        let mut built = self.command();
        built.build();
        let Some(words) = resolve_path_words(&built, path) else {
            return;
        };
        let Some(cmd) = find_path(&built, &words) else {
            return;
        };
        let Some(arg) = cmd.get_arguments().find(|a| flag_name(a) == flag) else {
            return;
        };
        self.flag_completers
            .entry((words.join(" "), flag_name(arg)))
            .or_insert(c);
    }

    pub(crate) fn bind_args_completer(&mut self, path: &str, c: Arc<dyn Completer>) {
        let path = normalize(path);
        if path.is_empty() || resolve_path(&self.command(), &path).is_ok() {
            self.args_completers.insert(path, c);
        }
    }

    /// The bound completer of `arg` on the command at `words` (an
    /// ancestor's binding serves a global flag), else its enum values:
    /// a command-scoped declaration, a tree-wide one, or clap's possible
    /// values.
    pub(crate) fn run_flag_completion(
        &self,
        words: &[String],
        arg: &Arg,
        ctx: &Context,
        to_complete: &str,
    ) -> Option<(Vec<String>, Directive)> {
        match self.flag_completer(words, arg) {
            Some(c) => Some(resolve(ctx, c.as_ref(), to_complete)),
            None => self.enum_completion(words, arg, to_complete),
        }
    }

    /// The completer bound to `arg` on the command at `words`, or on the
    /// ancestor declaring it when it is global.
    pub(crate) fn flag_completer(
        &self,
        words: &[String],
        arg: &Arg,
    ) -> Option<&Arc<dyn Completer>> {
        let name = flag_name(arg);
        let depth = if arg.is_global_set() { 0 } else { words.len() };
        (depth..=words.len()).rev().find_map(|end| {
            self.flag_completers
                .get(&(words[..end].join(" "), name.clone()))
        })
    }

    /// The completer bound to the positionals of the command at `path`.
    pub(crate) fn args_completer(&self, path: &str) -> Option<&Arc<dyn Completer>> {
        self.args_completers.get(path)
    }
}

fn is_builtin_completion_path(path: &str) -> bool {
    let mut words = path.split(' ');
    words.next() == Some(COMPLETION)
        && match words.next() {
            None => true,
            Some(leaf) => {
                (leaf.parse::<Shell>().is_ok() || leaf == FIG || leaf == USAGE)
                    && words.next().is_none()
            }
        }
}

/// A flag's name for binding and enums: its long name, else its id.
pub(crate) fn flag_name(arg: &Arg) -> String {
    arg.get_long()
        .map(str::to_string)
        .unwrap_or_else(|| arg.get_id().as_str().to_string())
}

pub(super) fn normalize(path: &str) -> String {
    path.split_whitespace().collect::<Vec<_>>().join(" ")
}

/// Walks `path` by subcommand name (Go `resolveCommand`).
pub(super) fn resolve_path(root: &Command, path: &str) -> Result<Vec<String>, String> {
    let words: Vec<String> = path.split_whitespace().map(String::from).collect();
    if words.is_empty() {
        return Err("empty command path".into());
    }
    let mut cur = root;
    for w in &words {
        cur = cur
            .get_subcommands()
            .find(|c| c.get_name() == w)
            .ok_or_else(|| format!("command {w:?} not found in {:?}", cur.get_name()))?;
    }
    Ok(words)
}

/// [`resolve_path`] that accepts the empty path as the root.
fn resolve_path_words(root: &Command, path: &str) -> Option<Vec<String>> {
    let path = normalize(path);
    if path.is_empty() {
        return Some(Vec::new());
    }
    resolve_path(root, &path).ok()
}

pub(super) fn find_path<'a>(root: &'a Command, words: &[String]) -> Option<&'a Command> {
    words.iter().try_fold(root, |cur, w| {
        cur.get_subcommands().find(|c| c.get_name() == w)
    })
}
