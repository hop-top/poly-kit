//! Parsing and dispatch of a kit root: [`Root::execute`] resolves
//! aliases, answers the help-group flags, classifies parse failures as
//! USAGE envelopes, applies opt-in autocorrect and runs the command's
//! handler. In-process and re-entrant: it writes to the streams it is
//! given and never exits or changes the process directory, so a served
//! surface can drive the same tree.

use std::collections::BTreeMap;
use std::io::Write;

use clap::{ArgMatches, Command};

use crate::output::{render_error, CliError};

use super::root::Root;
use super::usage::{
    classify, correct_args, correction_candidate, correction_notice, find_cmd, flag_candidates,
    AutocorrectMode,
};

/// The environment variable that sets the autocorrect policy.
pub const AUTOCORRECT_ENV: &str = "KIT_AUTOCORRECT";

/// A command handler: what an invocation ran with, the stdout and the
/// stderr streams. Its error is rendered as the envelope and returned.
pub type HandlerFn =
    dyn Fn(&Invocation, &mut dyn Write, &mut dyn Write) -> Result<(), CliError> + Send + Sync;

/// The resolved persistent flags of an invocation.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Globals {
    /// `--format` (empty when the flag is disabled).
    pub format: String,
    /// `--quiet`.
    pub quiet: bool,
    /// `-V` count.
    pub verbose: u8,
    /// `--no-color`.
    pub no_color: bool,
    /// `--no-hints`.
    pub no_hints: bool,
    /// `--offline`.
    pub offline: bool,
    /// `--dry-run`.
    pub dry_run: bool,
    /// `-C/--chdir`; carried, not applied (see [`Root::execute`]).
    pub chdir: Option<String>,
    /// `-c/--config` tokens, in order.
    pub config: Vec<String>,
    /// `--autocorrect`, as typed.
    pub autocorrect: Option<String>,
}

/// What a command was invoked with.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Invocation {
    /// The command path, space-separated, below the root.
    pub path: String,
    /// The command's own flags that were given, by long name, values in
    /// order (a switch has `"true"`).
    pub flags: BTreeMap<String, Vec<String>>,
    /// The positional arguments.
    pub args: Vec<String>,
    /// The persistent flags.
    pub globals: Globals,
}

impl Invocation {
    /// The last value of flag `name`, if given.
    pub fn flag(&self, name: &str) -> Option<&str> {
        self.flags
            .get(name)
            .and_then(|v| v.last())
            .map(String::as_str)
    }
}

impl Root {
    /// Runs `argv` (program name first) and returns the exit code: the
    /// error's `exit_code`, else 0.
    pub fn run<I, T>(&self, argv: I, out: &mut dyn Write, err: &mut dyn Write) -> i32
    where
        I: IntoIterator<Item = T>,
        T: Into<String>,
    {
        let argv: Vec<String> = argv.into_iter().map(Into::into).collect();
        match self.execute(&argv, out, err) {
            Ok(()) => 0,
            Err(e) => e.exit_code,
        }
    }

    /// Runs `argv` (program name first) against the tree. A failure is
    /// rendered to `err` in the active `--format` and returned.
    ///
    /// `-C/--chdir` and `-c/--config` are parsed and carried in
    /// [`Globals`]; applying them is the caller's, since a process-wide
    /// directory change does not belong in a re-entrant call.
    #[allow(clippy::result_large_err)]
    pub fn execute(
        &self,
        argv: &[String],
        out: &mut dyn Write,
        err: &mut dyn Write,
    ) -> Result<(), CliError> {
        if let Some(done) = self.try_builtin(argv.iter().cloned(), out, err) {
            return done.map_err(|e| {
                let ce = CliError::generic(e.to_string());
                let _ = render_error(err, &active_format(argv), &ce);
                ce
            });
        }
        let mut argv = argv.to_vec();
        let mut corrected = false;
        loop {
            let args = self.expand_alias(&argv);
            let mut built = self.command();
            built.build();
            let format = active_format(&args);
            match built.clone().try_get_matches_from(&args) {
                Ok(m) => return self.dispatch(&built, &m, &args, &format, out, err),
                Err(e) => {
                    let enums = |words: &[String], flag: &str| self.enum_values(words, flag);
                    let Some(fail) = classify(&built, built.get_name(), &args, &e, &enums) else {
                        // help or version: not a failure
                        let _ = write!(out, "{}", e.render());
                        return Ok(());
                    };
                    if !corrected {
                        if let Some(next) = self.autocorrection(&built, &args, &fail, err, &format)
                        {
                            argv = next;
                            corrected = true;
                            continue;
                        }
                    }
                    let _ = render_error(err, &format, &fail.env);
                    return Err(fail.env);
                }
            }
        }
    }

    /// The corrected argv when autocorrect applies to this failure.
    fn autocorrection(
        &self,
        built: &Command,
        args: &[String],
        fail: &super::usage::UsageFailure,
        err: &mut dyn Write,
        format: &str,
    ) -> Option<Vec<String>> {
        let typed = fail.typed_flag.as_deref()?;
        if autocorrect_mode(args) != AutocorrectMode::Read {
            return None;
        }
        if self
            .annotations_of(&fail.path.join(" "))
            .get(super::model::SIDE_EFFECT)
            .map(String::as_str)
            != Some("read")
        {
            return None;
        }
        let leaf = find_cmd(built, &fail.path)?;
        let hit = correction_candidate(typed, &flag_candidates(leaf))?;
        let next = correct_args(args, typed, &hit)?;
        let _ = render_error(err, format, &correction_notice(typed, &hit));
        Some(next)
    }

    #[allow(clippy::result_large_err)]
    fn dispatch(
        &self,
        built: &Command,
        m: &ArgMatches,
        args: &[String],
        format: &str,
        out: &mut dyn Write,
        err: &mut dyn Write,
    ) -> Result<(), CliError> {
        // --help-all / --help-<group>
        if m.try_get_one::<bool>("help-all").ok().flatten() == Some(&true) {
            let mut c = self.help_all_command();
            c.build();
            let _ = write!(out, "{}", c.render_long_help());
            return Ok(());
        }
        let mut ids = vec!["management".to_string()];
        if let Some(cfg) = &self.config {
            ids.extend(cfg.groups.iter().map(|g| g.id.clone()));
        }
        for id in ids {
            if m.try_get_one::<bool>(&format!("help-{id}")).ok().flatten() == Some(&true) {
                let mut c = self.help_group_command(&id);
                c.build();
                let _ = write!(out, "{}", c.render_long_help());
                return Ok(());
            }
        }
        // Resolve the leaf.
        let mut words = Vec::new();
        let mut cur = m;
        while let Some((name, sub)) = cur.subcommand() {
            words.push(name.to_string());
            cur = sub;
        }
        let path = words.join(" ");
        let cmd = find_cmd(built, &words).unwrap_or(built);
        let Some(handler) = self.handlers.get(&path) else {
            if cmd.get_subcommands().next().is_some() || words.is_empty() {
                let mut c = cmd.clone();
                let _ = write!(out, "{}", c.render_long_help());
                return Ok(());
            }
            let ce = CliError::generic(format!("no handler registered for {path:?}"));
            let _ = render_error(err, format, &ce);
            return Err(ce);
        };
        let inv = invocation(cmd, cur, m, path);
        let _ = args;
        match handler(&inv, out, err) {
            Ok(()) => Ok(()),
            Err(e) => {
                let _ = render_error(err, format, &e);
                Err(e)
            }
        }
    }

    /// Replaces a leading alias word with its target path.
    fn expand_alias(&self, argv: &[String]) -> Vec<String> {
        if self.aliases.is_empty() {
            return argv.to_vec();
        }
        let mut built = self.command();
        built.build();
        let mut i = 1;
        while i < argv.len() {
            let a = &argv[i];
            if a == "--" {
                break;
            }
            if let Some(rest) = a.strip_prefix("--") {
                let (name, eq) = rest
                    .split_once('=')
                    .map_or((rest, false), |(n, _)| (n, true));
                let takes = built
                    .get_arguments()
                    .find(|x| x.get_long() == Some(name))
                    .is_some_and(|x| super::model::flag_kind(x).takes_value());
                i += if takes && !eq { 2 } else { 1 };
                continue;
            }
            if let Some(rest) = a.strip_prefix('-') {
                let c = rest.chars().next();
                let takes = rest.chars().count() == 1
                    && c.is_some_and(|c| {
                        built
                            .get_arguments()
                            .find(|x| x.get_short() == Some(c))
                            .is_some_and(|x| super::model::flag_kind(x).takes_value())
                    });
                i += if takes { 2 } else { 1 };
                continue;
            }
            if let Some(target) = self.aliases.get(a) {
                let mut out = argv[..i].to_vec();
                out.extend(target.split(' ').map(str::to_string));
                out.extend_from_slice(&argv[i + 1..]);
                return out;
            }
            break;
        }
        argv.to_vec()
    }

    pub(super) fn enum_values(&self, words: &[String], flag: &str) -> Vec<String> {
        self.enums
            .get(&(words.join(" "), flag.to_string()))
            .or_else(|| self.enums.get(&(String::new(), flag.to_string())))
            .cloned()
            .unwrap_or_default()
    }
}

fn invocation(cmd: &Command, m: &ArgMatches, root: &ArgMatches, path: String) -> Invocation {
    let mut flags: BTreeMap<String, Vec<String>> = BTreeMap::new();
    let mut args = Vec::new();
    for a in cmd.get_arguments() {
        let id = a.get_id().as_str();
        if a.is_positional() {
            if let Ok(Some(vals)) = m.try_get_raw(id) {
                args.extend(vals.map(|v| v.to_string_lossy().into_owned()));
            }
            continue;
        }
        if a.is_global_set() || m.value_source(id) != Some(clap::parser::ValueSource::CommandLine) {
            continue;
        }
        let vals: Vec<String> = match m.try_get_raw(id) {
            Ok(Some(v)) => v.map(|v| v.to_string_lossy().into_owned()).collect(),
            _ => Vec::new(),
        };
        let vals = if vals.is_empty() {
            vec!["true".to_string()]
        } else {
            vals
        };
        flags.insert(super::root::flag_name(a), vals);
    }
    let _ = root;
    let s = |k: &str| m.try_get_one::<String>(k).ok().flatten().cloned();
    let b = |k: &str| {
        m.try_get_one::<bool>(k)
            .ok()
            .flatten()
            .copied()
            .unwrap_or(false)
    };
    let globals = Globals {
        format: s("format").unwrap_or_default(),
        quiet: b("quiet"),
        verbose: m
            .try_get_one::<u8>("verbose")
            .ok()
            .flatten()
            .copied()
            .unwrap_or(0),
        no_color: b("no-color"),
        no_hints: b("no-hints"),
        offline: b("offline"),
        dry_run: b("dry-run"),
        chdir: s("chdir").filter(|v| !v.is_empty()),
        config: m
            .try_get_many::<String>("config")
            .ok()
            .flatten()
            .map(|v| v.cloned().collect())
            .unwrap_or_default(),
        autocorrect: s("autocorrect"),
    };
    Invocation {
        path,
        flags,
        args,
        globals,
    }
}

/// The `--format` value on the command line (flag form or `=` form),
/// before any `--`; empty when absent.
pub(super) fn active_format(args: &[String]) -> String {
    let mut i = 1;
    while i < args.len() {
        let a = &args[i];
        if a == "--" {
            break;
        }
        if a == "--format" {
            return args.get(i + 1).cloned().unwrap_or_default();
        }
        if let Some(v) = a.strip_prefix("--format=") {
            return v.to_string();
        }
        i += 1;
    }
    String::new()
}

/// `--autocorrect` on the command line, else `KIT_AUTOCORRECT`, else
/// off. An unrecognised value falls through to the next rung.
fn autocorrect_mode(args: &[String]) -> AutocorrectMode {
    let mut i = 1;
    while i < args.len() {
        let a = &args[i];
        if a == "--" {
            break;
        }
        let v = if a == "--autocorrect" {
            args.get(i + 1).map(String::as_str)
        } else {
            a.strip_prefix("--autocorrect=")
        };
        if let Some(m) = v.and_then(AutocorrectMode::parse) {
            return m;
        }
        i += 1;
    }
    std::env::var(AUTOCORRECT_ENV)
        .ok()
        .and_then(|v| AutocorrectMode::parse(&v))
        .unwrap_or(AutocorrectMode::Off)
}
