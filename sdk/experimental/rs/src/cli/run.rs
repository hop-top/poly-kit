//! Parsing and dispatch of a kit root: [`Root::execute`] resolves
//! aliases, answers the help-group flags, classifies parse failures as
//! USAGE envelopes, applies opt-in autocorrect and runs the command's
//! handler. In-process and re-entrant: it writes to the streams it is
//! given and never exits or changes the process directory, so a served
//! surface can drive the same tree.

use std::collections::BTreeMap;
use std::io::Write;
use std::sync::{Arc, Mutex};

use clap::{ArgMatches, Command};

use crate::output::{render_error, CliError};

use super::autocorrect_prompt::{autocorrect_allowed, prompt_autocorrect};
use super::gate::Leaf;
use super::help::{color_enabled, Scope, Theme};
use super::root::Root;
use super::usage::{
    classify, correct_args, correction_candidate, correction_notice, find_cmd, flag_candidates,
    AutocorrectMode,
};
use super::validate::{SignatureStrictness, ValidationFailureMode};

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
    /// `-C/--chdir`, as typed; [`Root::execute`] applies it for the
    /// duration of the run.
    pub chdir: Option<String>,
    /// `-c/--config` tokens, in order; see [`Invocation::config_args`].
    pub config: Vec<String>,
    /// `--autocorrect`, as typed.
    pub autocorrect: Option<String>,
    /// `--confirm`, as typed (`auto|yes|no|prompt`; empty when unset).
    pub confirm: String,
    /// `--max-ops`: the cap on mutating operations (0 unlimited).
    pub max_ops: i64,
    /// `--policy`, as typed: the delegation policy name.
    pub policy: String,
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
    /// Every flag given on the command line, the command's own and the
    /// persistent ones, by long name, values in order.
    pub given: BTreeMap<String, Vec<String>>,
    /// Whether kit applied `--dry-run` to this run: the flag was given
    /// and the command's tier honors it. A read command accepts the
    /// flag and still runs for real.
    pub dry_run: bool,
    /// Whether kit renders this run's `--dry-run` preview itself and
    /// skips the handler: an unannotated destructive-named leaf, which
    /// is gated as destructive but never promised to honor `--dry-run`.
    pub(super) kit_preview: bool,
    /// The root's aliases (alias to target path) as they stand.
    pub aliases: BTreeMap<String, String>,
    /// The root's config project marker (see
    /// [`Config::project_marker`](super::Config)).
    pub project_marker: String,
}

impl Invocation {
    /// Renders `data` in the active `--format` (default `table`), the
    /// way Go's `output.Dispatch` does for a handler's result. The
    /// `--format-opt`, `--cols`, `--template` and `-o` refinements of
    /// the full dispatcher are not applied.
    #[allow(clippy::result_large_err)]
    pub fn render(&self, out: &mut dyn Write, data: &serde_json::Value) -> Result<(), CliError> {
        let format = if self.globals.format.is_empty() {
            "table"
        } else {
            self.globals.format.as_str()
        };
        let registry = crate::output::default_registry();
        let f = registry.lookup(format).ok_or_else(|| {
            CliError::usage(format!(
                "unknown output format '{format}' (valid: {})",
                registry.keys().join(", ")
            ))
        })?;
        f.render(out, data, &Default::default(), &[])
            .map_err(|e| CliError::generic(e.to_string()))
    }

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
        if let Some(stop) = self.preflight(err, &active_format(argv)) {
            return Err(stop);
        }
        if let Some(done) = self.help_topic(argv, out, err) {
            return done;
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
                        if e.kind() == clap::error::ErrorKind::DisplayHelp {
                            let words = help_target_words(&built, &args);
                            let text = self.help_text(
                                Scope::Default,
                                &words,
                                self.theme_for(&args).as_ref(),
                            );
                            if let Some(text) = text {
                                let _ = write!(out, "{text}");
                                return Ok(());
                            }
                        }
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

    /// The theme help is colored with, `None` for plain text.
    fn theme_for(&self, args: &[String]) -> Option<Theme> {
        color_enabled(args).then(|| self.help_theme.clone().unwrap_or_default())
    }

    /// The hidden `help [topic...]` form, resolved as Go does: no
    /// topic is root help; `all`; a command path that resolves fully; a
    /// group id; else a usage error. `None` when `argv` is not a help
    /// topic request (or the tree owns a `help` command).
    #[allow(clippy::result_large_err)]
    fn help_topic(
        &self,
        argv: &[String],
        out: &mut dyn Write,
        err: &mut dyn Write,
    ) -> Option<Result<(), CliError>> {
        let at = argv
            .iter()
            .skip(1)
            .position(|a| !a.starts_with('-'))
            .map(|i| i + 1)?;
        if argv[at] != "help" || argv[1..at].iter().any(|a| a == "--") {
            return None;
        }
        let args = self.expand_alias(argv);
        let mut built = self.command();
        built.build();
        if built.get_subcommands().any(|c| c.get_name() == "help") {
            return None;
        }
        let operands: Vec<String> = argv[at + 1..]
            .iter()
            .take_while(|a| *a != "--")
            .filter(|a| !a.starts_with('-'))
            .cloned()
            .collect();
        let theme = self.theme_for(&args);
        let text = if operands.is_empty() {
            self.help_text(Scope::Default, &[], theme.as_ref())
        } else if operands == ["all"] {
            self.help_text(Scope::All { plumbing: false }, &[], theme.as_ref())
        } else if let Some(words) = resolve_topic(&built, &operands) {
            self.help_text(Scope::Default, &words, theme.as_ref())
        } else if operands.len() == 1 && self.group_defs().iter().any(|g| g.id == operands[0]) {
            self.help_text(Scope::Group(&operands[0]), &[], theme.as_ref())
        } else {
            None
        };
        Some(match text {
            Some(t) => {
                let _ = write!(out, "{t}");
                Ok(())
            }
            None => {
                let ce = CliError::usage(format!(
                    "unknown help topic {:?} for {:?}",
                    operands.join(" "),
                    built.get_name()
                ));
                let _ = render_error(err, &active_format(&args), &ce);
                Err(ce)
            }
        })
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
        let mode = autocorrect_mode(args);
        let side_effect = self.annotations_of(&fail.path.join(" "));
        let (allowed, default_yes) = autocorrect_allowed(
            side_effect
                .get(super::model::SIDE_EFFECT)
                .map(String::as_str),
            mode,
        );
        if !allowed {
            return None;
        }
        let leaf = find_cmd(built, &fail.path)?;
        let hit = correction_candidate(typed, &flag_candidates(leaf))?;
        if mode == AutocorrectMode::Prompt {
            // No terminal, or a decline: suggest-only stands, and the
            // envelope the caller renders is the uncorrected one.
            let (applied, _asked) =
                prompt_autocorrect(&self.prompt_source, typed, &hit, default_yes);
            if !applied {
                return None;
            }
        }
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
        let theme = self.theme_for(args);
        if m.try_get_one::<bool>("help-all").ok().flatten() == Some(&true) {
            let text = self.help_text(Scope::All { plumbing: true }, &[], theme.as_ref());
            let _ = write!(out, "{}", text.unwrap_or_default());
            return Ok(());
        }
        for def in self.group_defs() {
            let id = def.id;
            if m.try_get_one::<bool>(&format!("help-{id}")).ok().flatten() == Some(&true) {
                let text = self.help_text(Scope::Group(&id), &[], theme.as_ref());
                let _ = write!(out, "{}", text.unwrap_or_default());
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
                let text = self.help_text(Scope::Default, &words, theme.as_ref());
                let _ = write!(out, "{}", text.unwrap_or_default());
                return Ok(());
            }
            let ce = CliError::generic(format!("no handler registered for {path:?}"));
            let _ = render_error(err, format, &ce);
            return Err(ce);
        };
        let mut inv = invocation(cmd, cur, m, path.clone());
        inv.aliases = self.aliases.clone();
        if let Some(cfg) = &self.config {
            inv.project_marker = cfg.project_marker.clone();
        }
        let _ = args;
        let result = self.run_leaf(handler, &mut inv, cmd, format, out, err);
        match result {
            Ok(()) => Ok(()),
            Err(e) => {
                let _ = render_error(err, format, &e);
                Err(e)
            }
        }
    }

    /// The pre-run chain of a leaf: `-C`, then (on a kit root) the
    /// delegation gates, the flag validators and the handler.
    #[allow(clippy::result_large_err)]
    fn run_leaf(
        &self,
        handler: &Arc<HandlerFn>,
        inv: &mut Invocation,
        cmd: &Command,
        format: &str,
        out: &mut dyn Write,
        err: &mut dyn Write,
    ) -> Result<(), CliError> {
        let _cwd = self.apply_chdir(inv)?;
        if self.config.is_none() {
            return handler(inv, out, err);
        }
        let annotations = self.annotations_of(&inv.path);
        let leaf = Leaf {
            full_path: format!("{} {}", self.cmd.get_name(), inv.path)
                .trim()
                .to_string(),
            verb: &inv.path.clone(),
            annotations: &annotations,
            format,
            local_policy: local_arg(cmd, "policy"),
            local_confirm_value: local_arg(cmd, "confirm")
                .then(|| inv.flag("confirm").unwrap_or("").to_string()),
        };
        self.gated(&leaf, inv, err, |inv, err| {
            for (name, validate) in &self.flag_validators {
                if let Some(e) = inv
                    .given
                    .get(name)
                    .and_then(|v| v.last())
                    .and_then(|v| validate(v))
                {
                    return Err(e);
                }
            }
            if inv.kit_preview {
                return inv.render_inferred_preview(&leaf.full_path, out);
            }
            handler(inv, out, err)
        })
    }

    /// Applies `-C/--chdir` for the duration of the run: the target is
    /// used as is when it is a directory, else resolved by the
    /// configured resolver. The returned guard restores the previous
    /// directory, so `execute` leaves the process where it found it.
    #[allow(clippy::result_large_err)]
    fn apply_chdir(&self, inv: &Invocation) -> Result<Option<CwdGuard>, CliError> {
        let Some(cfg) = &self.config else {
            return Ok(None);
        };
        let target = match (&inv.globals.chdir, cfg.disable.chdir) {
            (Some(t), false) if !t.is_empty() => t,
            _ => return Ok(None),
        };
        let dir = if std::path::Path::new(target).is_dir() {
            std::path::PathBuf::from(target)
        } else if let Some(r) = &cfg.chdir_resolver {
            r.resolve(target).map_err(CliError::generic)?
        } else {
            return Err(CliError::generic(format!(
                "cannot chdir to {target:?}: not a directory"
            )));
        };
        CwdGuard::enter(&dir)
            .map(Some)
            .map_err(|e| CliError::generic(e.to_string()))
    }

    /// The pre-flight of a kit root: `Root::validate` and the
    /// signature validators, per their configured modes. `Some` is the
    /// failure to stop with, already rendered.
    fn preflight(&self, err: &mut dyn Write, format: &str) -> Option<CliError> {
        let cfg = self.config.as_ref()?;
        let fail = |e: CliError, render: bool, err: &mut dyn Write| {
            if render {
                let _ = render_error(err, format, &e);
            }
            Some(e)
        };
        if !cfg.disable_validate {
            if let Err(ve) = self.validate() {
                match cfg.validation_failure {
                    ValidationFailureMode::Exit => return fail(ve.to_cli_error(), true, err),
                    ValidationFailureMode::Error => return fail(ve.to_cli_error(), false, err),
                    ValidationFailureMode::Silent => {
                        let _ = writeln!(err, "[warn] cli validation failure (silent mode): {ve}");
                    }
                }
            }
        }
        if cfg.signature_strictness != SignatureStrictness::Silent {
            let report = self.validate_signature();
            if report.has_violations() {
                match cfg.signature_strictness {
                    SignatureStrictness::Warn => {
                        for v in &report.violations {
                            let _ = writeln!(
                                err,
                                "[warn] cli signature violation: {} [{}] {}",
                                v.path, v.check, v.detail
                            );
                        }
                    }
                    _ => {
                        let e = CliError::usage(report.summary());
                        match cfg.validation_failure {
                            ValidationFailureMode::Exit => return fail(e, true, err),
                            ValidationFailureMode::Error => return fail(e, false, err),
                            ValidationFailureMode::Silent => {
                                let _ = writeln!(err, "[warn] {}", e.message);
                            }
                        }
                    }
                }
            }
        }
        None
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
    let mut given: BTreeMap<String, Vec<String>> = BTreeMap::new();
    for a in cmd.get_arguments() {
        let id = a.get_id().as_str();
        if a.is_positional() || m.value_source(id) != Some(clap::parser::ValueSource::CommandLine) {
            continue;
        }
        let vals: Vec<String> = match m.try_get_raw(id) {
            Ok(Some(v)) => v.map(|v| v.to_string_lossy().into_owned()).collect(),
            _ => Vec::new(),
        };
        given.insert(
            super::root::flag_name(a),
            if vals.is_empty() {
                vec!["true".to_string()]
            } else {
                vals
            },
        );
    }
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
        confirm: s("confirm").unwrap_or_default(),
        max_ops: m
            .try_get_one::<i64>("max-ops")
            .ok()
            .flatten()
            .copied()
            .unwrap_or(0),
        policy: s("policy").unwrap_or_default(),
    };
    Invocation {
        path,
        flags,
        args,
        globals,
        given,
        ..Invocation::default()
    }
}

/// Whether `cmd` declares its own (non-persistent) flag `name`, which
/// shadows the root's persistent flag of that name.
fn local_arg(cmd: &Command, name: &str) -> bool {
    cmd.get_arguments()
        .any(|a| a.get_id().as_str() == name && !a.is_global_set())
}

/// Restores the process directory when dropped. Directory changes are
/// process-wide, so concurrent `-C` runs queue on one lock; a run
/// without `-C` is not held back, and sees the changed directory while
/// one is in flight.
struct CwdGuard {
    prev: std::path::PathBuf,
    _lock: std::sync::MutexGuard<'static, ()>,
}

impl CwdGuard {
    fn enter(dir: &std::path::Path) -> std::io::Result<CwdGuard> {
        static LOCK: Mutex<()> = Mutex::new(());
        let lock = LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let prev = std::env::current_dir()?;
        std::env::set_current_dir(dir)?;
        Ok(CwdGuard { prev, _lock: lock })
    }
}

impl Drop for CwdGuard {
    fn drop(&mut self) {
        let _ = std::env::set_current_dir(&self.prev);
    }
}

#[cfg(feature = "config")]
impl Invocation {
    /// The `-c` tokens split into extra config files and dotted
    /// `key=value` overrides, as Go's `Root.ConfigArgs`. A bare
    /// directory resolves through the root's project marker; a token
    /// that is neither a pair nor a file is an error.
    pub fn config_args(
        &self,
    ) -> Result<(Vec<std::path::PathBuf>, Option<crate::config::Map>), crate::config::Error> {
        let tokens: Vec<&str> = self
            .globals
            .config
            .iter()
            .map(|t| t.trim())
            .filter(|t| !t.is_empty() && *t != "[]")
            .collect();
        let mut opts = Vec::new();
        if !self.project_marker.is_empty() {
            opts.push(crate::config::with_project_marker(
                self.project_marker.clone(),
            ));
        }
        crate::config::parse_config_args(&tokens, &opts)
    }

    /// Layers the `-c` tokens into a config load: files after the
    /// discovered ones, overrides last, winning over every file.
    pub fn layer_config(
        &self,
        mut opts: crate::config::Options,
    ) -> Result<crate::config::Options, crate::config::Error> {
        let (paths, overrides) = self.config_args()?;
        opts.extra_config_paths.extend(paths);
        if let Some(o) = overrides {
            match opts.overrides.as_mut() {
                Some(existing) => existing.extend(o),
                None => opts.overrides = Some(o),
            }
        }
        Ok(opts)
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

/// The command path `operands` name when every word resolves, by name
/// or alias, below the root.
fn resolve_topic(root: &Command, operands: &[String]) -> Option<Vec<String>> {
    let mut cur = root;
    let mut words = Vec::new();
    for w in operands {
        cur = cur
            .get_subcommands()
            .find(|c| c.get_name() == w || c.get_all_aliases().any(|a| a == w))?;
        words.push(cur.get_name().to_string());
    }
    Some(words)
}

/// The command a help request addresses: the subcommand words of
/// `args`, skipping the values of flags that take one.
fn help_target_words(root: &Command, args: &[String]) -> Vec<String> {
    let mut cur = root;
    let mut words = Vec::new();
    let mut i = 1;
    while i < args.len() {
        let a = &args[i];
        if a == "--" {
            break;
        }
        if let Some(long) = a.strip_prefix("--") {
            let takes = !long.contains('=')
                && cur
                    .get_arguments()
                    .any(|x| x.get_long() == Some(long) && x.get_action().takes_values());
            i += usize::from(takes);
        } else if a.starts_with('-') && a.chars().count() == 2 {
            let c = a.chars().nth(1);
            let takes = cur
                .get_arguments()
                .any(|x| x.get_short() == c && x.get_action().takes_values());
            i += usize::from(takes);
        } else if let Some(sub) = cur
            .get_subcommands()
            .find(|s| s.get_name() == a || s.get_all_aliases().any(|x| x == a))
        {
            cur = sub;
            words.push(sub.get_name().to_string());
        }
        i += 1;
    }
    words
}
