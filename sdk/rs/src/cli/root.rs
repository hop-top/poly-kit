//! The kit root: a [`CommandSpec`] tree under kit's persistent flags,
//! the `completion` command in the management group, root aliases
//! (`alias.rs`) and the flag value-enum registry (`enums.rs`). Ports Go
//! `console/cli`'s `Root`; clap is the parsing provider behind it and
//! never appears in its API.

use std::collections::{BTreeMap, HashMap};
use std::ffi::OsString;
use std::fmt;
use std::io::{self, Write};
use std::sync::Arc;

use clap::{Arg, ArgAction, Command};

use super::adapter::to_clap;
use super::completion::{
    self, completion_command, func, resolve, Completer, Context, Directive, Shell, COMPLETION,
    NO_DESC_FLAG, NO_DESC_REQUEST_CMD, REQUEST_CMD,
};
use super::completion_spec::{write_export, FIG, USAGE};
use super::config::{Config, GlobalKind};
use super::enums::apply_enum_help;
use super::gate::PromptSource;
use super::help::{GroupDef, Help, Scope, Theme};
use super::policy::PolicyLoader;
use super::run::HandlerFn;
use super::spec::CommandSpec;
use super::validate_flag::FlagValidator;

const MANAGEMENT: &str = "management";

/// A command tree with kit's completion surface.
///
/// [`Root::new`] takes the root [`CommandSpec`]; the built tree adds the
/// hidden `completion bash|zsh|fish|powershell` command (in the
/// `management` group, which default help hides) unless a command or
/// alias already answers to `completion`.
/// Like Go kit, the root has no `help` subcommand; `-h/--help` remain.
///
/// Wire it in `main`:
///
/// ```no_run
/// use hop_top_kit::cli::{CommandSpec, Root};
///
/// let root = Root::new(
///     CommandSpec::new("mytool")
///         .subcommand(CommandSpec::new("deploy").handler(|_, _, _| Ok(()))),
/// );
/// let (mut out, mut err) = (std::io::stdout(), std::io::stderr());
/// std::process::exit(root.run(std::env::args(), &mut out, &mut err));
/// ```
pub struct Root {
    pub(super) cmd: Command,
    pub(super) aliases: BTreeMap<String, String>,
    pub(super) enums: HashMap<(String, String), Vec<String>>,
    flag_completers: HashMap<(String, String), Arc<dyn Completer>>,
    args_completers: HashMap<String, Arc<dyn Completer>>,
    completion_disabled: bool,
    pub(super) config: Option<Config>,
    pub(super) annotations: HashMap<String, BTreeMap<String, String>>,
    pub(super) handlers: HashMap<String, Arc<HandlerFn>>,
    pub(super) groups: HashMap<String, String>,
    pub(super) policy_loader: Option<PolicyLoader>,
    pub(super) prompt_source: PromptSource,
    pub(super) flag_validators: Vec<(String, FlagValidator)>,
    pub(super) help_theme: Option<Theme>,
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
    /// Wraps the root command `spec`, its subtree included.
    pub fn new(spec: CommandSpec) -> Root {
        let mut root = Root::bare(to_clap(&spec));
        root.register(&spec, "");
        root
    }

    /// Wraps a raw clap tree: for the adapter's own tests, which probe
    /// clap rules a [`CommandSpec`] cannot state.
    #[cfg(test)]
    pub(crate) fn from_clap(cmd: Command) -> Root {
        Root::bare(cmd)
    }

    fn bare(cmd: Command) -> Root {
        Root {
            cmd: cmd.disable_help_subcommand(true),
            aliases: BTreeMap::new(),
            enums: HashMap::new(),
            flag_completers: HashMap::new(),
            args_completers: HashMap::new(),
            completion_disabled: false,
            config: None,
            annotations: HashMap::new(),
            handlers: HashMap::new(),
            groups: HashMap::new(),
            policy_loader: None,
            prompt_source: PromptSource::controlling_tty(),
            flag_validators: Vec::new(),
            help_theme: None,
        }
    }

    /// A kit root: the persistent flags (`--format`, `--quiet`,
    /// `-V/--verbose`, `--no-color`, `--no-hints`, `--offline`, the
    /// hidden `-C/--chdir`, `-c/--config`, `--dry-run`, `--autocorrect`),
    /// the tool's own globals, `-v/--version` and the `--help-all` /
    /// `--help-<group>` flags, as Go's `cli.New` registers them. Add
    /// the CLI's commands with [`Root::add_command`].
    pub fn with_config(cfg: Config) -> Root {
        let mut root = Root::bare(base_command(&cfg));
        root.config = Some(cfg);
        root
    }

    /// Adds a command (with its subtree) to the root.
    pub fn add_command(&mut self, spec: CommandSpec) -> &mut Root {
        let c = std::mem::take(&mut self.cmd);
        self.cmd = c.subcommand(to_clap(&spec));
        self.register(&spec, &spec.name);
        self
    }

    /// Records the handlers, annotations, groups and help groups of
    /// `spec` and its subtree, `path` being the space-separated path
    /// of `spec` (empty for the root).
    fn register(&mut self, spec: &CommandSpec, path: &str) {
        if let Some(h) = &spec.handler {
            self.handlers.insert(path.to_string(), Arc::clone(h));
        }
        if !spec.annotations.is_empty() {
            self.annotations
                .entry(path.to_string())
                .or_default()
                .extend(spec.annotations.clone());
        }
        if let (Some(id), false) = (&spec.group, path.is_empty()) {
            if !path.contains(' ') {
                self.groups.insert(path.to_string(), id.clone());
            }
        }
        for s in &spec.subcommands {
            let p = if path.is_empty() {
                s.name.clone()
            } else {
                format!("{path} {}", s.name)
            };
            self.register(s, &p);
        }
    }

    /// Puts the root subcommand `name` in help group `id`; a hidden
    /// group (`management`, or one declared hidden in [`Config`]) keeps
    /// it out of default help.
    pub fn set_group(&mut self, name: &str, id: &str) -> &mut Root {
        self.groups.insert(name.to_string(), id.to_string());
        self
    }

    /// Sets a `kit/*` annotation on the command at `path`
    /// (space-separated), as Go's `cmd.Annotations`.
    pub fn annotate(&mut self, path: &str, key: &str, value: &str) -> &mut Root {
        self.annotations
            .entry(normalize(path))
            .or_default()
            .insert(key.to_string(), value.to_string());
        self
    }

    /// Declares the `kit/side-effect` tier of the command at `path`
    /// (`read`, `write`, `destructive`, ...).
    pub fn set_side_effect(&mut self, path: &str, tier: &str) -> &mut Root {
        self.annotate(path, super::model::SIDE_EFFECT, tier)
    }

    /// Registers the handler [`Root::execute`] runs for the command at
    /// `path`: a command with a handler is invocable, one without is
    /// only a group.
    pub fn handle<F>(&mut self, path: &str, f: F) -> &mut Root
    where
        F: Fn(
                &super::run::Invocation,
                &mut dyn Write,
                &mut dyn Write,
            ) -> Result<(), crate::output::CliError>
            + Send
            + Sync
            + 'static,
    {
        self.handlers.insert(normalize(path), Arc::new(f));
        self
    }

    pub(super) fn group_of(&self, path: &[String]) -> Option<String> {
        match path {
            [name] => self
                .groups
                .get(name)
                .cloned()
                .or_else(|| self.command_group(name).map(str::to_string)),
            _ => None,
        }
    }

    pub(super) fn annotations_of(&self, path: &str) -> BTreeMap<String, String> {
        self.annotations.get(path).cloned().unwrap_or_default()
    }

    /// The ids of the hidden help groups.
    fn hidden_groups(&self) -> Vec<String> {
        let mut out = vec![MANAGEMENT.to_string()];
        if let Some(cfg) = &self.config {
            out.extend(cfg.groups.iter().filter(|g| g.hidden).map(|g| g.id.clone()));
        }
        out
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

    /// Whether `top` names the built-in `completion` command.
    pub(super) fn is_builtin_top(&self, top: &str) -> bool {
        top == COMPLETION && self.has_builtin_completion()
    }

    /// Whether `path` is the built-in `completion` command or one of its
    /// leaves, which complete no arguments.
    pub(crate) fn is_builtin_completion_path(&self, path: &str) -> bool {
        self.has_builtin_completion() && is_builtin_completion_path(path)
    }

    /// The command tree to parse with: the adopter's tree, alias
    /// registrations, the built-in `completion` command when it applies
    /// and enum help suffixes.
    pub(crate) fn command(&self) -> Command {
        let mut cmd = self.cmd.clone();
        if self.has_builtin_completion() {
            cmd = cmd.subcommand(completion_command(self.cmd.get_name()));
        }
        let hidden = self.hidden_groups();
        for (name, id) in &self.groups {
            if hidden.contains(id) {
                cmd = cmd.mut_subcommand(name, |c| c.hide(true));
            }
        }
        let cmd = self.with_confirm_tokens(cmd, "");
        apply_enum_help(cmd, "", &self.enums)
    }

    /// Registers `--confirm-token` on every leaf that demands a typed
    /// confirmation (`kit/destructive-token`).
    fn with_confirm_tokens(&self, cmd: Command, path: &str) -> Command {
        let names: Vec<String> = cmd
            .get_subcommands()
            .map(|c| c.get_name().to_string())
            .collect();
        let leaf = names.is_empty();
        let mut cmd = cmd;
        for n in names {
            let p = if path.is_empty() {
                n.clone()
            } else {
                format!("{path} {n}")
            };
            cmd = cmd.mut_subcommand(&n, |c| self.with_confirm_tokens(c, &p));
        }
        if leaf
            && !path.is_empty()
            && super::gate::requires_token(&self.annotations_of(path))
            && !cmd
                .get_arguments()
                .any(|a| a.get_id().as_str() == super::gate::CONFIRM_TOKEN_FLAG)
        {
            cmd = cmd.arg(
                Arg::new(super::gate::CONFIRM_TOKEN_FLAG)
                    .long(super::gate::CONFIRM_TOKEN_FLAG)
                    .help(
                        "Typed confirmation token (sha) printed by the command's prompt. \
                         Required for kit/destructive-token commands.",
                    ),
            );
        }
        cmd
    }

    /// [`Root::command`] with the management group shown, the analogue
    /// of Go's `--help-all`.
    pub(crate) fn help_all_command(&self) -> Command {
        let mut cmd = self.command();
        if self.has_builtin_completion() {
            cmd = cmd.mut_subcommand(COMPLETION, |c| c.hide(false));
        }
        for (name, id) in &self.groups {
            if self.hidden_groups().contains(id) {
                cmd = cmd.mut_subcommand(name, |c| c.hide(false));
            }
        }
        cmd
    }

    /// [`Root::command`] showing only the commands of group `id`
    /// (`--help-<id>`): the other root subcommands are hidden.
    pub(crate) fn help_group_command(&self, id: &str) -> Command {
        let mut cmd = self.help_all_command();
        let names: Vec<String> = cmd
            .get_subcommands()
            .map(|c| c.get_name().to_string())
            .collect();
        for n in names {
            if self.command_group(&n) != Some(id) {
                cmd = cmd.mut_subcommand(&n, |c| c.hide(true));
            }
        }
        cmd
    }

    /// The default help, as `-h` prints it: management commands and
    /// hidden flags left out. Plain text; [`Root::render_help_styled`]
    /// colors it.
    pub fn render_help(&self) -> String {
        self.help_text(Scope::Default, &[], None)
            .unwrap_or_default()
    }

    /// The default help colored by `theme`.
    pub fn render_help_styled(&self, theme: &Theme) -> String {
        self.help_text(Scope::Default, &[], Some(theme))
            .unwrap_or_default()
    }

    /// The `--help-all` help: every group and the plumbing flags shown.
    pub fn render_help_all(&self) -> String {
        self.help_text(Scope::All { plumbing: true }, &[], None)
            .unwrap_or_default()
    }

    /// The `--help-<id>` help: only the commands of group `id`.
    pub fn render_help_group(&self, id: &str) -> String {
        self.help_text(Scope::Group(id), &[], None)
            .unwrap_or_default()
    }

    /// The help of the command at `path` (space-separated), `None`
    /// when no such command exists.
    pub fn render_command_help(&self, path: &str) -> Option<String> {
        let mut built = self.command();
        built.build();
        let words = resolve_path_words(&built, path)?;
        self.help_text(Scope::Default, &words, None)
    }

    /// Colors help with `theme` instead of the Neon default when the
    /// output is a terminal that takes color (`NO_COLOR`, `TERM=dumb`
    /// and `--no-color` still win).
    pub fn set_help_theme(&mut self, theme: Theme) -> &mut Root {
        self.help_theme = Some(theme);
        self
    }

    /// The help groups: the built-in hidden `management`, then the
    /// configured ones (a configured group of the same id replaces it).
    pub(super) fn group_defs(&self) -> Vec<GroupDef> {
        let mut out = vec![GroupDef {
            id: MANAGEMENT.into(),
            title: "MANAGEMENT".into(),
            hidden: true,
        }];
        if let Some(cfg) = &self.config {
            for g in &cfg.groups {
                let def = GroupDef {
                    id: g.id.clone(),
                    title: if g.title.is_empty() {
                        g.id.to_uppercase()
                    } else {
                        g.title.clone()
                    },
                    hidden: g.hidden,
                };
                match out.iter_mut().find(|d| d.id == g.id) {
                    Some(d) => *d = def,
                    None => out.push(def),
                }
            }
        }
        out
    }

    /// The help screen for `scope` of the command at `path` (empty for
    /// the root); `None` when `path` names no command.
    pub(super) fn help_text(
        &self,
        scope: Scope<'_>,
        path: &[String],
        theme: Option<&Theme>,
    ) -> Option<String> {
        let mut cmd = match scope {
            Scope::Default => self.command(),
            Scope::All { .. } => self.help_all_command(),
            Scope::Group(id) => self.help_group_command(id),
        };
        cmd.build();
        let model = self.describe(&cmd, &[], true);
        let target = model.find(&path.join(" "))?;
        let groups = self.group_defs();
        Some(
            Help {
                root_name: &model.name,
                target,
                scope,
                groups: &groups,
                theme,
            }
            .render(),
        )
    }

    /// The help group of a root subcommand: `management` for the
    /// built-in `completion`; `None` for the default group.
    pub fn command_group(&self, name: &str) -> Option<&str> {
        if let Some(id) = self.groups.get(name) {
            return Some(id.as_str());
        }
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
    /// plus alias names at the root, but for visible proxies, which
    /// complete as subcommands. `None` when neither exists.
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
                // A visible proxy completes as a subcommand already.
                if self.is_visible_proxy(name) {
                    continue;
                }
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

    /// Whether alias `name` is a visible proxy: a root subcommand that
    /// completion offers among the subcommands, so the alias list
    /// leaves it out rather than offer it twice.
    fn is_visible_proxy(&self, name: &str) -> bool {
        self.cmd
            .get_subcommands()
            .any(|c| c.get_name() == name && !c.is_hide_set())
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

/// The root command of a kit config: persistent flags, version, help
/// group flags.
fn base_command(cfg: &Config) -> Command {
    let version = cfg.version.trim_start_matches('v').to_string();
    let mut cmd = Command::new(cfg.name.clone())
        .about(cfg.short.clone())
        .version(format!("v{version}"))
        .disable_version_flag(true)
        .arg(
            Arg::new("version")
                .short('v')
                .long("version")
                .action(ArgAction::Version)
                .help(format!("Version for {}", cfg.name)),
        );
    if !cfg.disclaimer.is_empty() {
        // Go: Long = Short + "\n\n" + Help.Disclaimer.
        cmd = cmd.long_about(format!("{}\n\n{}", cfg.short, cfg.disclaimer));
    }
    let d = cfg.disable;
    if !d.quiet {
        cmd = cmd.arg(
            Arg::new("quiet")
                .long("quiet")
                .action(ArgAction::SetTrue)
                .global(true)
                .help("Suppress non-essential output"),
        );
    }
    cmd = cmd.arg(
        Arg::new("verbose")
            .long("verbose")
            .short('V')
            .action(ArgAction::Count)
            .global(true)
            .help("Increase log verbosity (-V=debug, -VV=trace)"),
    );
    if !d.no_color {
        cmd = cmd.arg(
            Arg::new("no-color")
                .long("no-color")
                .action(ArgAction::SetTrue)
                .global(true)
                .help("Disable ANSI color"),
        );
    }
    if !d.chdir {
        cmd = cmd.arg(
            Arg::new("chdir")
                .long("chdir")
                .short('C')
                .global(true)
                .hide(true)
                .help("Change directory before running (path or tool-specific target)"),
        );
    }
    if !d.config {
        cmd = cmd.arg(
            Arg::new("config")
                .long("config")
                .short('c')
                .action(ArgAction::Append)
                .global(true)
                .hide(true)
                .help(
                    "Layer extra config (repeatable). key=value overrides a single value \
                     (dotted keys for nesting; value parses as YAML, falls back to literal). \
                     A bare path loads an additional config file after the discovered ones. \
                     -c tokens win over file layers.",
                ),
        );
    }
    if !d.format {
        let (c, _) = crate::output::register_output_flags(cmd, Default::default());
        cmd = c;
    }
    if !d.hints {
        cmd = cmd.arg(
            Arg::new("no-hints")
                .long("no-hints")
                .action(ArgAction::SetTrue)
                .global(true)
                .help("Suppress next-step hints after command output"),
        );
    }
    if !d.dry_run {
        cmd = cmd.arg(
            Arg::new("dry-run")
                .long("dry-run")
                .action(ArgAction::SetTrue)
                .global(true)
                .hide(true)
                .help("Preview side effects without applying them (must be supported by the command)."),
        );
    }
    // Delegation-safety globals: always registered, names reserved.
    cmd = cmd
        .arg(
            Arg::new("confirm")
                .long("confirm")
                .global(true)
                .hide(true)
                .help(
                    "Confirmation policy for destructive commands (auto|yes|no|prompt). \
                     Default: prompt on a TTY, no otherwise.",
                ),
        )
        .arg(
            Arg::new("max-ops")
                .long("max-ops")
                .global(true)
                .hide(true)
                .value_parser(clap::value_parser!(i64))
                .help("Cap mutating operations per invocation. 0 = unlimited."),
        )
        .arg(
            Arg::new("policy")
                .long("policy")
                .global(true)
                .hide(true)
                .help(
                    "Named delegation policy (loaded from \
                     $XDG_CONFIG_HOME/<tool>/policies/<name>.yaml).",
                ),
        );
    cmd = cmd
        .arg(
            Arg::new("autocorrect")
                .long("autocorrect")
                .global(true)
                .hide(true)
                .help(
                    "Act on a mistyped flag's correction (off|prompt|read). Default: off \
                     (suggest only). read applies it on read-only commands; prompt asks on a TTY.",
                ),
        )
        .arg(
            Arg::new("offline")
                .long("offline")
                .action(ArgAction::SetTrue)
                .global(true)
                .help("Disable network access"),
        );
    for g in &cfg.globals {
        let mut a = Arg::new(g.name.clone())
            .long(g.name.clone())
            .global(true)
            .help(g.usage.clone());
        if let Some(s) = g.short {
            a = a.short(s);
        }
        a = match g.kind {
            GlobalKind::Bool => a.action(ArgAction::SetTrue),
            GlobalKind::Int => a.value_parser(clap::value_parser!(i64)),
            GlobalKind::String => a,
        };
        if let Some(def) = &g.default {
            a = a.default_value(def.clone());
        }
        cmd = cmd.arg(a);
    }
    // Help group flags: root-local switches `--help-all` and one per group.
    cmd = cmd.arg(
        Arg::new("help-all")
            .long("help-all")
            .action(ArgAction::SetTrue)
            .help("Show all commands and global flags"),
    );
    let mut ids = vec![MANAGEMENT.to_string()];
    ids.extend(cfg.groups.iter().map(|g| g.id.clone()));
    for id in ids {
        cmd = cmd.arg(
            Arg::new(format!("help-{id}"))
                .long(format!("help-{id}"))
                .action(ArgAction::SetTrue)
                .help(format!("Show only {id} commands")),
        );
    }
    cmd
}
