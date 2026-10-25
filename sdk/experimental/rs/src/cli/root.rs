//! The minimal clap root that completion hangs on. Ports the parts of Go
//! `console/cli` that shell completion reads: the eagerly registered
//! `completion` command in the management group, root aliases
//! (`alias.go`) and the flag value-enum registry (`flag_enum.go`).

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

const MANAGEMENT: &str = "management";

/// A clap command tree with kit's completion surface.
///
/// [`Root::new`] takes the finished tree and adds the hidden
/// `completion bash|zsh|fish|powershell` command (in the `management`
/// group, which default help hides) unless the tree already has one.
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
    cmd: Command,
    aliases: BTreeMap<String, String>,
    enums: HashMap<(String, String), Vec<String>>,
    flag_completers: HashMap<(String, String), Arc<dyn Completer>>,
    args_completers: HashMap<String, Arc<dyn Completer>>,
    groups: BTreeMap<String, String>,
}

impl fmt::Debug for Root {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Root")
            .field("name", &self.cmd.get_name())
            .field("aliases", &self.aliases)
            .field("groups", &self.groups)
            .finish_non_exhaustive()
    }
}

/// An alias that cannot be registered. The texts are Go's.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AliasError(String);

impl fmt::Display for AliasError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for AliasError {}

impl Root {
    /// Wraps a finished command tree.
    pub fn new(cmd: Command) -> Root {
        let mut root = Root {
            cmd: cmd.disable_help_subcommand(true),
            aliases: BTreeMap::new(),
            enums: HashMap::new(),
            flag_completers: HashMap::new(),
            args_completers: HashMap::new(),
            groups: BTreeMap::new(),
        };
        let taken = root
            .cmd
            .get_subcommands()
            .any(|c| c.get_name() == COMPLETION || c.get_all_aliases().any(|a| a == COMPLETION));
        if !taken {
            let prog = root.cmd.get_name().to_string();
            root.cmd = std::mem::take(&mut root.cmd).subcommand(completion_command(&prog));
            root.groups.insert(COMPLETION.into(), MANAGEMENT.into());
            // cobra's NoFileCompletions on the command and each shell leaf.
            let none = || Arc::new(func(|_: &Context, _: &str| Ok(Vec::new())));
            root.args_completers.insert(COMPLETION.into(), none());
            for sh in Shell::ALL {
                root.args_completers
                    .insert(format!("{COMPLETION} {sh}"), none());
            }
        }
        root
    }

    /// The command tree to parse with: the adopter's tree, alias
    /// registrations, the `completion` command and enum help suffixes.
    pub fn command(&self) -> Command {
        apply_enum_help(self.cmd.clone(), "", &self.enums)
    }

    /// [`Root::command`] with the management group shown, the analogue
    /// of Go's `--help-all`.
    pub fn help_all_command(&self) -> Command {
        let mut cmd = self.command();
        for name in self.groups.keys() {
            cmd = cmd.mut_subcommand(name, |c| c.hide(false));
        }
        cmd
    }

    /// The help group of a root subcommand, e.g. `management` for
    /// `completion`; `None` for the default group.
    pub fn command_group(&self, name: &str) -> Option<&str> {
        self.groups.get(name).map(String::as_str)
    }

    /// Registers `name` as a root-level alias for the command at `target`
    /// (a space-separated path). A direct child gains a clap alias; a
    /// deeper command is cloned as a hidden root subcommand named `name`,
    /// which the caller dispatches through [`Root::alias_target`].
    pub fn alias(&mut self, name: &str, target: &str) -> Result<(), AliasError> {
        if name.is_empty() || name.contains([' ', '\t', '\n']) {
            return Err(AliasError(format!(
                "alias name {name:?} must be non-empty without whitespace"
            )));
        }
        if let Some(c) = self.cmd.get_subcommands().find(|c| c.get_name() == name) {
            return Err(AliasError(format!(
                "alias {name:?} collides with command {:?}",
                c.get_name()
            )));
        }
        if let Some(t) = self.aliases.get(name) {
            return Err(AliasError(format!(
                "alias {name:?} already registered for {t:?}"
            )));
        }
        let words = resolve_path(&self.cmd, target).map_err(AliasError)?;
        let path = words.join(" ");
        let cmd = std::mem::take(&mut self.cmd);
        self.cmd = if words.len() == 1 {
            cmd.mut_subcommand(&words[0], |c| c.alias(name.to_string()))
        } else {
            let target = find_path(&cmd, &words).expect("resolved above").clone();
            let shim = target
                .name(name.to_string())
                .about(format!("Alias for {path}"))
                .hide(true);
            cmd.subcommand(shim)
        };
        self.aliases.insert(name.to_string(), path);
        Ok(())
    }

    /// Every registered alias with its target path.
    pub fn aliases(&self) -> &BTreeMap<String, String> {
        &self.aliases
    }

    /// The target path of alias `name`.
    pub fn alias_target(&self, name: &str) -> Option<&str> {
        self.aliases.get(name).map(String::as_str)
    }

    /// Declares the closed value set of every flag named `name` in the
    /// tree: completion offers the values and help gains
    /// `(one of: …)`. Last registration wins; no values clears it.
    pub fn with_flag_enum<S: Into<String>>(
        &mut self,
        name: &str,
        values: impl IntoIterator<Item = S>,
    ) -> &mut Root {
        self.set_enum(String::new(), name, values)
    }

    /// [`Root::with_flag_enum`] for one command's flag; wins over a
    /// tree-wide declaration of the same name.
    pub fn with_command_flag_enum<S: Into<String>>(
        &mut self,
        path: &str,
        name: &str,
        values: impl IntoIterator<Item = S>,
    ) -> &mut Root {
        self.set_enum(normalize(path), name, values)
    }

    fn set_enum<S: Into<String>>(
        &mut self,
        path: String,
        name: &str,
        values: impl IntoIterator<Item = S>,
    ) -> &mut Root {
        if name.is_empty() {
            return self;
        }
        let values: Vec<String> = values.into_iter().map(Into::into).collect();
        let key = (path, name.to_string());
        if values.is_empty() {
            self.enums.remove(&key);
        } else {
            self.enums.insert(key, values);
        }
        self
    }

    /// The tree-wide value set declared for `name`.
    pub fn flag_enum(&self, name: &str) -> Option<&[String]> {
        self.enums
            .get(&(String::new(), name.to_string()))
            .map(Vec::as_slice)
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
        let bound = self.args_completers.get(&path);
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
        if first != COMPLETION || !self.groups.contains_key(COMPLETION) {
            return None;
        }
        let mut cmd = self.command();
        let matches = cmd.try_get_matches_from_mut(&argv).ok()?;
        let (_, sub) = matches.subcommand()?;
        Some(match sub.subcommand() {
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
        if path.is_empty() || resolve_path(&self.cmd, &path).is_ok() {
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
        let name = flag_name(arg);
        let depth = if arg.is_global_set() { 0 } else { words.len() };
        for end in (depth..=words.len()).rev() {
            let key = (words[..end].join(" "), name.clone());
            if let Some(c) = self.flag_completers.get(&key) {
                return Some(resolve(ctx, c.as_ref(), to_complete));
            }
        }
        let values = self
            .enums
            .get(&(words.join(" "), name.clone()))
            .or_else(|| self.enums.get(&(String::new(), name)))
            .cloned()
            .unwrap_or_else(|| {
                arg.get_possible_values()
                    .iter()
                    .filter(|p| !p.is_hide_set())
                    .map(|p| p.get_name().to_string())
                    .collect()
            });
        if values.is_empty() {
            return None;
        }
        let lower = to_complete.to_lowercase();
        let out = values
            .into_iter()
            .filter(|v| v.to_lowercase().starts_with(&lower))
            .collect();
        Some((out, Directive::NO_FILE_COMP))
    }
}

/// A flag's name for binding and enums: its long name, else its id.
pub(crate) fn flag_name(arg: &Arg) -> String {
    arg.get_long()
        .map(str::to_string)
        .unwrap_or_else(|| arg.get_id().as_str().to_string())
}

fn normalize(path: &str) -> String {
    path.split_whitespace().collect::<Vec<_>>().join(" ")
}

/// Walks `path` by subcommand name (Go `resolveCommand`).
fn resolve_path(root: &Command, path: &str) -> Result<Vec<String>, String> {
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

fn find_path<'a>(root: &'a Command, words: &[String]) -> Option<&'a Command> {
    words.iter().try_fold(root, |cur, w| {
        cur.get_subcommands().find(|c| c.get_name() == w)
    })
}

/// Appends `(one of: …)` to the help of every flag with declared enum
/// values, from the unsuffixed help each time so re-declaring replaces.
fn apply_enum_help(
    mut cmd: Command,
    path: &str,
    enums: &HashMap<(String, String), Vec<String>>,
) -> Command {
    if enums.is_empty() {
        return cmd;
    }
    let edits: Vec<(String, String)> = cmd
        .get_arguments()
        .filter(|a| !a.is_positional())
        .filter_map(|a| {
            let name = flag_name(a);
            let values = enums
                .get(&(path.to_string(), name.clone()))
                .or_else(|| enums.get(&(String::new(), name)))?;
            let suffix = format!("(one of: {})", values.join(", "));
            let help = match a.get_help().map(|h| h.to_string()) {
                Some(h) if !h.is_empty() => format!("{h} {suffix}"),
                _ => suffix,
            };
            Some((a.get_id().as_str().to_string(), help))
        })
        .collect();
    for (id, help) in edits {
        cmd = cmd.mut_arg(id, |a| a.help(help));
    }
    let subs: Vec<String> = cmd
        .get_subcommands()
        .map(|c| c.get_name().to_string())
        .collect();
    for s in subs {
        let p = if path.is_empty() {
            s.clone()
        } else {
            format!("{path} {s}")
        };
        cmd = cmd.mut_subcommand(&s, |c| apply_enum_help(c, &p, enums));
    }
    cmd
}
