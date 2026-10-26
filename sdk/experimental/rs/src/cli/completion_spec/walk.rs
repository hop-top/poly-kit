//! The walker: the spec model from a [`Root`]'s built clap tree, and the
//! `completion fig|usage` commands that print it.

use std::sync::Arc;

use clap::{Arg as ClapArg, ArgAction, Command as ClapCommand};

use super::{Arg, Command, Completion, Flag, Spec};
use crate::cli::completion::{Completer, FileFilter};
use crate::cli::root::{flag_name, Root};

/// Name of the Fig export leaf under `completion`.
pub(crate) const FIG: &str = "fig";
/// Name of the Usage export leaf under `completion`.
pub(crate) const USAGE: &str = "usage";

/// The `fig` and `usage` leaves of the built-in `completion` command.
pub(crate) fn export_commands() -> [ClapCommand; 2] {
    [
        ClapCommand::new(FIG)
            .about("Generate a Fig completion spec")
            .long_about(
                "Print a Fig completion spec for this tool: an ES module whose default\n\
                 export is the spec. VS Code terminal suggest, Kiro CLI, inshellisense\n\
                 and Warp read this format. Values a completion function computes are\n\
                 fetched from this binary when the user asks.",
            ),
        ClapCommand::new(USAGE)
            .about("Generate a Usage (usage.jdx.dev) completion spec")
            .long_about(
                "Print a Usage KDL spec for this tool, for usage.jdx.dev and mise:\n\
                 `usage generate completion <shell>` turns it into a shell script, and\n\
                 `usage generate markdown` into reference docs. Values a completion\n\
                 function computes are fetched from this binary when the user asks.",
            ),
    ]
}

impl Spec {
    /// The spec of `root`'s tree as [`Root::command`] builds it, bound
    /// completers and enums included.
    ///
    /// What it reads, and the rule each follows:
    /// - Commands: every subcommand, the built-in `completion` included.
    ///   A hidden one is kept and marked hidden: it still parses.
    /// - Flags: those a command declares, its `global` ones included;
    ///   a global flag a subcommand inherits belongs to the ancestor. The
    ///   help flag is left out: terminals offer help on their own.
    /// - Positionals: the visible ones, in order. A command with none but
    ///   a bound completer gets one optional, variadic `arg` to hang it
    ///   on.
    /// - Flag candidates, first match wins: a declared enum (scoped, then
    ///   tree-wide) or clap's possible values, then the bound completer.
    ///   Positional candidates: its possible values, then the command's
    ///   bound completer. A [`file()`] or [`dir`] completer is written
    ///   into the spec; any other calls back.
    ///
    /// [`file()`]: crate::cli::completion::file
    /// [`dir`]: crate::cli::completion::dir
    pub fn from_root(root: &Root) -> Spec {
        let mut cmd = root.command();
        cmd.build();
        let w = Walker {
            root,
            root_name: cmd.get_name().to_string(),
        };
        Spec {
            version: cmd.get_version().unwrap_or_default().to_string(),
            root: w.command(&cmd, None, &[]),
        }
    }
}

struct Walker<'a> {
    root: &'a Root,
    root_name: String,
}

impl Walker<'_> {
    fn command(&self, c: &ClapCommand, parent: Option<&ClapCommand>, words: &[String]) -> Command {
        let mut flags: Vec<Flag> = c
            .get_arguments()
            .filter(|a| !a.is_positional() && !inherited(parent, a) && flag_name(a) != "help")
            .map(|a| self.flag(c, words, a))
            .collect();
        flags.sort_by(|a, b| a.name.cmp(&b.name));
        let mut commands: Vec<Command> = c
            .get_subcommands()
            .map(|sub| {
                let mut path = words.to_vec();
                path.push(sub.get_name().to_string());
                self.command(sub, Some(c), &path)
            })
            .collect();
        commands.sort_by(|a, b| a.name.cmp(&b.name));
        Command {
            name: c.get_name().to_string(),
            aliases: c.get_all_aliases().map(str::to_string).collect(),
            description: c
                .get_about()
                .map(|s| s.ansi().to_string())
                .unwrap_or_default(),
            hidden: c.is_hide_set(),
            flags,
            args: self.args(c, words),
            commands,
        }
    }

    fn flag(&self, c: &ClapCommand, words: &[String], a: &ClapArg) -> Flag {
        let action = a.get_action();
        let description = match action {
            ArgAction::Version => format!("version for {}", self.root_name),
            _ => a
                .get_help()
                .map(|h| h.ansi().to_string())
                .unwrap_or_default(),
        };
        let mut out = Flag {
            name: flag_name(a),
            long: a.get_long().is_some(),
            short: a.get_short(),
            description,
            persistent: a.is_global_set(),
            hidden: a.is_hide_set(),
            required: a.is_required_set(),
            repeatable: matches!(action, ArgAction::Append | ArgAction::Count),
            ..Flag::default()
        };
        if action.takes_values() {
            // Attached-only, as pflag's NoOptDefVal: without require_equals
            // clap binds the next word, so the value is a plain one.
            let optional =
                a.is_require_equals_set() && a.get_num_args().is_some_and(|r| r.min_values() == 0);
            if optional {
                out.default_missing = default_missing(c, a);
            }
            out.arg = Some(Arg {
                name: value_name(a),
                optional,
                variadic: false,
                complete: self.flag_completion(words, a),
            });
        }
        out
    }

    fn flag_completion(&self, words: &[String], a: &ClapArg) -> Completion {
        if let Some((values, _)) = self.root.enum_completion(words, a, "") {
            return Completion::Choices(values);
        }
        self.root
            .flag_completer(words, a)
            .map_or(Completion::None, completer_kind)
    }

    fn args(&self, c: &ClapCommand, words: &[String]) -> Vec<Arg> {
        let bound = self.args_completion(words);
        let mut out: Vec<Arg> = c
            .get_positionals()
            .filter(|a| !a.is_hide_set())
            .map(|a| Arg {
                name: value_name(a),
                optional: !a.is_required_set(),
                variadic: matches!(a.get_action(), ArgAction::Append)
                    || a.get_num_args().is_some_and(|r| r.max_values() > 1),
                complete: match possible_values(a) {
                    v if !v.is_empty() => Completion::Choices(v),
                    _ => bound.clone(),
                },
            })
            .collect();
        if out.is_empty() && bound != Completion::None {
            out.push(Arg {
                name: "arg".into(),
                optional: true,
                variadic: true,
                complete: bound,
            });
        }
        out
    }

    /// The command's positional completer as the spec sees it. Root
    /// aliases are completed among the root's positionals, so a root
    /// with aliases calls back; the built-in completion leaves complete
    /// nothing (cobra's `NoFileCompletions`).
    fn args_completion(&self, words: &[String]) -> Completion {
        let path = words.join(" ");
        if self.root.is_builtin_completion_path(&path) {
            return Completion::None;
        }
        if path.is_empty() && !self.root.aliases().is_empty() {
            return Completion::Dynamic;
        }
        self.root
            .args_completer(&path)
            .map_or(Completion::None, completer_kind)
    }
}

/// A global flag `c` got from its parent rather than declared.
fn inherited(parent: Option<&ClapCommand>, a: &ClapArg) -> bool {
    a.is_global_set()
        && parent.is_some_and(|p| {
            p.get_arguments()
                .any(|x| x.get_id() == a.get_id() && x.is_global_set())
        })
}

fn completer_kind(c: &Arc<dyn Completer>) -> Completion {
    match c.file_filter() {
        Some(FileFilter::Dirs) => Completion::Dir,
        Some(FileFilter::Extensions(exts)) => Completion::File(
            exts.iter()
                .map(|e| e.strip_prefix('.').unwrap_or(e).to_string())
                .collect(),
        ),
        None => Completion::Dynamic,
    }
}

fn possible_values(a: &ClapArg) -> Vec<String> {
    a.get_possible_values()
        .iter()
        .filter(|p| !p.is_hide_set())
        .map(|p| p.get_name().to_string())
        .collect()
}

/// The first value name, else the id, as clap's help names the value;
/// an empty one is cobra's `value`.
fn value_name(a: &ClapArg) -> String {
    let name = a
        .get_value_names()
        .and_then(|n| n.first())
        .map(|n| n.to_string())
        .unwrap_or_else(|| a.get_id().to_string());
    if name.is_empty() {
        return "value".into();
    }
    name
}

/// What a bare optional-value flag stands for. clap keeps
/// `default_missing_value` private, so the flag is parsed alone on a
/// copy of its command, errors (missing required arguments) ignored.
/// That runs the flag's value parser once; `None` when it rejects the
/// value.
fn default_missing(c: &ClapCommand, a: &ClapArg) -> Option<String> {
    let word = match (a.get_long(), a.get_short()) {
        (Some(l), _) => format!("--{l}"),
        (None, Some(s)) => format!("-{s}"),
        (None, None) => return None,
    };
    let probe = c.clone().ignore_errors(true);
    probe
        .try_get_matches_from([c.get_name().to_string(), word])
        .ok()
        .and_then(|m| {
            m.get_raw(a.get_id().as_str())
                .and_then(|mut v| v.next())
                .map(|v| v.to_string_lossy().into_owned())
        })
}

/// Writes the export of `root` that `leaf` (`fig` or `usage`) names.
pub(crate) fn write_export(
    root: &Root,
    leaf: &str,
    out: &mut dyn std::io::Write,
) -> std::io::Result<()> {
    let spec = Spec::from_root(root);
    let text = if leaf == FIG {
        super::render_fig(&spec)
    } else {
        super::render_usage(&spec)
    };
    out.write_all(text.as_bytes())
}
