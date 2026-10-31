//! The delegation gates a leaf passes before its handler runs, in Go's
//! order (`policy_runE.go`, `global_dryrun.go`): `--dry-run`
//! enforcement, the named `--policy`, the `--confirm` matrix with the
//! typed `--confirm-token`, and the `--max-ops` budget accounted after
//! a successful mutating run.

use std::collections::BTreeMap;
use std::io::Write;
use std::sync::{Arc, Mutex};

use sha2::{Digest, Sha256};

use crate::output::CliError;

use super::policy::{
    is_destructive_like, is_write_like, CommandRef, Engine, Policy, PolicyLoader, KIT_DEFAULT_NAME,
};
use super::root::Root;
use super::run::Invocation;

/// The `kit/destructive-token` annotation: `required` makes a
/// destructive command also demand `--confirm-token=<sha>`.
pub const DESTRUCTIVE_TOKEN: &str = "kit/destructive-token";
/// The `kit/dry-run` annotation: `supported` (legacy) or `opted-out`.
pub const DRY_RUN: &str = "kit/dry-run";
/// Why a command opted out of `--dry-run`.
pub const DRY_RUN_RATIONALE: &str = "kit/dry-run-rationale";
/// The `kit/permissions` annotation: the scopes a command requires.
pub const PERMISSIONS: &str = "kit/permissions";
/// `--confirm-token`, registered on commands requiring a typed token.
pub const CONFIRM_TOKEN_FLAG: &str = "confirm-token";

/// The terminal an interactive prompt asks on.
pub trait Prompter: Send + Sync {
    /// Writes `question` and reads one line of answer, lowercased and
    /// trimmed; `None` on end of input.
    fn ask(&self, question: &str) -> Option<String>;
}

/// Supplies the prompt terminal: `None` means nobody to ask, and
/// prompts take their non-interactive path.
#[derive(Clone)]
pub struct PromptSource(Arc<dyn Fn() -> Option<Arc<dyn Prompter>> + Send + Sync>);

impl std::fmt::Debug for PromptSource {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("PromptSource")
    }
}

struct Scripted(Mutex<std::collections::VecDeque<String>>);

impl Prompter for Scripted {
    fn ask(&self, _question: &str) -> Option<String> {
        self.0
            .lock()
            .ok()?
            .pop_front()
            .map(|a| a.trim().to_lowercase())
    }
}

#[cfg(unix)]
struct Tty(Mutex<(std::io::BufReader<std::fs::File>, std::fs::File)>);

#[cfg(unix)]
impl Prompter for Tty {
    fn ask(&self, question: &str) -> Option<String> {
        use std::io::BufRead;
        let mut g = self.0.lock().ok()?;
        let _ = g.1.write_all(question.as_bytes());
        let mut line = String::new();
        match g.0.read_line(&mut line) {
            Ok(0) | Err(_) => None,
            Ok(_) => Some(line.trim().to_lowercase()),
        }
    }
}

impl PromptSource {
    /// A source that is `f`'s answer each time it is asked.
    pub fn new<F>(f: F) -> PromptSource
    where
        F: Fn() -> Option<Arc<dyn Prompter>> + Send + Sync + 'static,
    {
        PromptSource(Arc::new(f))
    }

    /// No terminal: every prompt is refused.
    pub fn none() -> PromptSource {
        PromptSource::new(|| None)
    }

    /// A scripted terminal answering from `answers`, one per question.
    pub fn from_answers<S: Into<String>>(answers: impl IntoIterator<Item = S>) -> PromptSource {
        let p: Arc<dyn Prompter> = Arc::new(Scripted(Mutex::new(
            answers.into_iter().map(Into::into).collect(),
        )));
        PromptSource::new(move || Some(Arc::clone(&p)))
    }

    /// The process's controlling terminal (`/dev/tty`), opened once;
    /// none where there is no such terminal. The default.
    pub fn controlling_tty() -> PromptSource {
        PromptSource::new(controlling_tty)
    }

    pub(super) fn get(&self) -> Option<Arc<dyn Prompter>> {
        (self.0)()
    }
}

#[cfg(unix)]
fn controlling_tty() -> Option<Arc<dyn Prompter>> {
    static TTY: std::sync::OnceLock<Option<Arc<dyn Prompter>>> = std::sync::OnceLock::new();
    TTY.get_or_init(|| {
        let r = std::fs::OpenOptions::new()
            .read(true)
            .open("/dev/tty")
            .ok()?;
        let w = std::fs::OpenOptions::new()
            .write(true)
            .open("/dev/tty")
            .ok()?;
        Some(Arc::new(Tty(Mutex::new((std::io::BufReader::new(r), w)))) as Arc<dyn Prompter>)
    })
    .clone()
}

#[cfg(not(unix))]
fn controlling_tty() -> Option<Arc<dyn Prompter>> {
    None
}

/// The parsed `--confirm` value.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum ConfirmMode {
    Auto,
    Yes,
    No,
    Prompt,
}

/// Parses `--confirm`: empty or unknown defaults to `prompt` when a
/// terminal exists to ask on, `no` otherwise. Interactivity is the
/// terminal's availability, not isatty on stdin.
pub(super) fn resolve_confirm_mode(src: &PromptSource, raw: &str) -> ConfirmMode {
    match raw.trim().to_lowercase().as_str() {
        "yes" => ConfirmMode::Yes,
        "no" => ConfirmMode::No,
        "auto" => ConfirmMode::Auto,
        "prompt" => ConfirmMode::Prompt,
        _ if src.get().is_some() => ConfirmMode::Prompt,
        _ => ConfirmMode::No,
    }
}

/// The token a typed-confirmation command demands: the first 12 hex
/// characters of the sha256 of its full command path.
pub fn destructive_token(command_path: &str) -> String {
    let h = Sha256::digest(command_path.as_bytes());
    h[..6].iter().map(|b| format!("{b:02x}")).collect()
}

pub(super) fn requires_token(a: &BTreeMap<String, String>) -> bool {
    matches!(
        a.get(DESTRUCTIVE_TOKEN).map(String::as_str),
        Some("required" | "true")
    )
}

/// What `--dry-run` does on a leaf, by its annotations.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum DryRunPolicy {
    Allow,
    NoOp,
    RejectInteractive,
    RejectOptOut,
    RejectUntagged,
    RejectMalformed,
}

/// The dry-run table: opted-out refuses; legacy `supported` allows;
/// write and destructive tiers allow; read is a silent no-op;
/// interactive refuses; malformed values refuse. An untagged leaf
/// refuses, unless its name is in the destructive-name table: that
/// leaf is gated as `destructive-shared` and previews like one.
/// `leaf_name` is the last word of the command path.
pub(super) fn resolve_dry_run_policy(
    leaf_name: &str,
    a: &BTreeMap<String, String>,
) -> DryRunPolicy {
    match a.get(DRY_RUN).map(String::as_str) {
        Some("opted-out") => return DryRunPolicy::RejectOptOut,
        Some("supported") => return DryRunPolicy::Allow,
        _ => {}
    }
    let declared = a.get(super::model::SIDE_EFFECT).map(String::as_str);
    if super::model::inferred_destructive(leaf_name, declared) {
        return DryRunPolicy::Allow;
    }
    match declared {
        None | Some("") => DryRunPolicy::RejectUntagged,
        Some(s) if is_write_like(s) || is_destructive_like(s) => DryRunPolicy::Allow,
        Some("read") => DryRunPolicy::NoOp,
        Some("interactive") => DryRunPolicy::RejectInteractive,
        Some(_) => DryRunPolicy::RejectMalformed,
    }
}

fn dry_run_refusal(path: &str, a: &BTreeMap<String, String>, p: DryRunPolicy) -> Option<CliError> {
    let (msg, fix) = match p {
        DryRunPolicy::RejectInteractive => (
            format!(
                "{path:?} does not accept --dry-run: interactive sessions have no batch \
                 boundary to scope the preview"
            ),
            "run without --dry-run".to_string(),
        ),
        DryRunPolicy::RejectOptOut => {
            let mut m = format!(
                "{path:?} does not support --dry-run: the command explicitly opted out via \
                 cli.OptOutDryRun"
            );
            if let Some(why) = a.get(DRY_RUN_RATIONALE).filter(|w| !w.is_empty()) {
                m.push_str(&format!(" ({why})"));
            }
            (
                m,
                "run without --dry-run; see the command's documentation for the rationale".into(),
            )
        }
        DryRunPolicy::RejectUntagged => (
            format!(
                "{path:?} cannot apply --dry-run: the command is missing the required \
                 kit/side-effect tag"
            ),
            "adopter must declare the tier with CommandSpec::side_effect".into(),
        ),
        DryRunPolicy::RejectMalformed => (
            format!(
                "{path:?} cannot apply --dry-run: the command has a malformed kit/side-effect \
                 tag {:?} that names no tier",
                a.get(super::model::SIDE_EFFECT)
                    .map(String::as_str)
                    .unwrap_or("")
            ),
            "adopter must declare one of the kit side-effect tiers".into(),
        ),
        _ => return None,
    };
    let mut e = CliError::usage(msg);
    e.suggested_fix = fix;
    Some(e)
}

/// Everything a gate decision reads about the leaf being run.
pub(super) struct Leaf<'a> {
    /// `tool a b`: the root name and the command path.
    pub full_path: String,
    /// `a b`: the path below the root.
    pub verb: &'a str,
    pub annotations: &'a BTreeMap<String, String>,
    pub format: &'a str,
    /// Whether the leaf declares its own `--policy` (which shadows the
    /// delegation flag; its value is not a policy name).
    pub local_policy: bool,
    pub local_confirm_value: Option<String>,
}

fn machine_format(f: &str) -> bool {
    matches!(f, "json" | "yaml")
}

impl Root {
    /// Applies `--dry-run` to a leaf: whether it runs as a preview, or
    /// the refusal. Help and the built-in completion command are exempt.
    pub(super) fn apply_dry_run(
        &self,
        leaf: &Leaf<'_>,
        inv: &Invocation,
    ) -> Result<bool, CliError> {
        if !inv.globals.dry_run || self.dry_run_disabled() {
            return Ok(false);
        }
        let leaf_name = leaf.verb.rsplit(' ').next().unwrap_or("");
        let p = resolve_dry_run_policy(leaf_name, leaf.annotations);
        if let Some(e) = dry_run_refusal(&leaf.full_path, leaf.annotations, p) {
            return Err(e);
        }
        Ok(p == DryRunPolicy::Allow)
    }

    fn dry_run_disabled(&self) -> bool {
        self.config.as_ref().is_some_and(|c| c.disable.dry_run)
    }

    /// The engine for this invocation: `--policy` through the loader,
    /// else an empty (default-permit) policy under `--max-ops`.
    fn policy_engine(&self, name: &str, max_ops: i64) -> Result<Engine, CliError> {
        let policy = if name == KIT_DEFAULT_NAME {
            Policy::kit_default()
        } else if !name.is_empty() {
            let Some(loader) = &self.policy_loader else {
                return Err(CliError::usage(
                    "--policy is set but no policy loader is wired (Root::with_policy)",
                ));
            };
            // A policy that cannot be read is a usage failure, not an
            // authorization refusal: the gate never ran.
            loader(name).map_err(|e| CliError::usage(format!("policy {name:?}: {e}")))?
        } else {
            Policy::default()
        };
        Ok(Engine::new(policy, max_ops))
    }

    /// Runs `inner` (flag validators, then the handler) behind the
    /// gates. `Err` is unrendered; the caller renders it once.
    pub(super) fn gated<F>(
        &self,
        leaf: &Leaf<'_>,
        inv: &mut Invocation,
        err: &mut dyn Write,
        inner: F,
    ) -> Result<(), CliError>
    where
        F: FnOnce(&Invocation, &mut dyn Write) -> Result<(), CliError>,
    {
        let declared = leaf
            .annotations
            .get(super::model::SIDE_EFFECT)
            .map(String::as_str);
        // An unannotated destructive-named leaf is gated as the
        // destructive tier Go's reflection gives it.
        let leaf_name = leaf.verb.rsplit(' ').next().unwrap_or("");
        let se = if super::model::inferred_destructive(leaf_name, declared) {
            Some(super::model::INFERRED_TIER)
        } else {
            declared
        };
        let dry_run = self.apply_dry_run(leaf, inv)?;
        inv.dry_run = dry_run;
        // An inferred-destructive leaf made no promise to honor
        // `--dry-run` (the legacy `supported` opt-in is one): kit renders
        // the preview and the handler never runs.
        inv.kit_preview = dry_run
            && super::model::inferred_destructive(leaf_name, declared)
            && leaf.annotations.get(DRY_RUN).map(String::as_str) != Some("supported");

        let policy_name = if leaf.local_policy {
            ""
        } else {
            inv.globals.policy.as_str()
        };
        let mut engine = self.policy_engine(policy_name, inv.globals.max_ops)?;

        let mut policy_confirm = false;
        if leaf.verb != "serve" {
            let perms = leaf.annotations.get(PERMISSIONS).map(String::as_str);
            let v = engine.authorize(&CommandRef {
                verb: leaf.verb,
                side_effect: se,
                permissions: perms,
            });
            if !v.allowed {
                return Err(CliError::unauthorized(v.reason));
            }
            policy_confirm = v.require_confirm;
        }

        if !dry_run && (se.is_some_and(is_destructive_like) || policy_confirm) {
            self.gate_confirm(leaf, inv, se.unwrap_or(""), err)?;
        }

        inner(inv, err)?;

        if !dry_run && se.is_some_and(|s| is_write_like(s) || is_destructive_like(s)) {
            engine.record_op().map_err(|_| {
                CliError::rate_limited(format!(
                    "max-ops budget exceeded after running {}",
                    leaf.full_path
                ))
            })?;
        }
        Ok(())
    }

    /// The `--confirm` + `--confirm-token` matrix for a destructive (or
    /// policy-confirmed) leaf.
    fn gate_confirm(
        &self,
        leaf: &Leaf<'_>,
        inv: &Invocation,
        se: &str,
        err: &mut dyn Write,
    ) -> Result<(), CliError> {
        let raw = leaf
            .local_confirm_value
            .as_deref()
            .unwrap_or(inv.globals.confirm.as_str());
        let mode = resolve_confirm_mode(&self.prompt_source, raw);
        if requires_token(leaf.annotations) {
            let expected = destructive_token(&leaf.full_path);
            let got = inv.flag(CONFIRM_TOKEN_FLAG).unwrap_or("");
            if got.is_empty() {
                let mut refusal = CliError::unauthorized(format!(
                    "destructive command {} requires --confirm-token={expected}",
                    leaf.full_path
                ));
                if machine_format(leaf.format) {
                    // The preview would make stderr unparseable; the
                    // envelope carries the instruction instead.
                    refusal.suggested_fix =
                        format!("re-run with --confirm-token={expected} to proceed");
                } else {
                    let _ = write!(
                        err,
                        "This is a destructive operation requiring typed confirmation.\n\
                         Token: {expected}\nRe-run with --confirm-token={expected} to proceed.\n"
                    );
                }
                return Err(refusal);
            }
            if got != expected {
                return Err(CliError::unauthorized(format!(
                    "--confirm-token mismatch for {}; expected {expected}",
                    leaf.full_path
                )));
            }
            return Ok(());
        }
        match mode {
            ConfirmMode::Yes | ConfirmMode::Auto => Ok(()),
            // Transient: re-invoking with --confirm=yes clears it.
            ConfirmMode::No => Err(CliError::unauthorized(format!(
                "destructive command {} refused: --confirm=no (or non-TTY default)",
                leaf.full_path
            ))
            .with_transience(crate::output::TRANSIENCE_TRANSIENT)),
            ConfirmMode::Prompt => {
                let q = format!(
                    "This is a {se} operation ({}). Continue? [y/N] ",
                    leaf.full_path
                );
                let yes = self
                    .prompt_source
                    .get()
                    .and_then(|p| p.ask(&q))
                    .is_some_and(|a| a == "y" || a == "yes");
                if yes {
                    Ok(())
                } else {
                    Err(CliError::unauthorized(format!(
                        "aborted by user at confirm prompt for {}",
                        leaf.full_path
                    ))
                    .with_transience(crate::output::TRANSIENCE_TRANSIENT))
                }
            }
        }
    }

    /// Wires the loader `--policy <name>` resolves names through; see
    /// [`default_policy_loader`](super::default_policy_loader).
    pub fn with_policy(&mut self, loader: PolicyLoader) -> &mut Root {
        self.policy_loader = Some(loader);
        self
    }

    /// Sets the terminal prompts use; the default is the controlling
    /// terminal.
    pub fn with_prompt_source(&mut self, src: PromptSource) -> &mut Root {
        self.prompt_source = src;
        self
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn token_is_the_first_twelve_hex_of_sha256() {
        // sha256("kitapp purge") = 3de0...; 6 bytes = 12 hex characters.
        let t = destructive_token("kitapp purge");
        assert_eq!(t.len(), 12);
        assert!(t.chars().all(|c| c.is_ascii_hexdigit()));
        assert_eq!(t, destructive_token("kitapp purge"));
        assert_ne!(t, destructive_token("kitapp rm"));
    }

    #[test]
    fn confirm_mode_defaults_by_terminal_availability() {
        let none = PromptSource::none();
        let tty = PromptSource::from_answers(["y"]);
        assert_eq!(resolve_confirm_mode(&none, ""), ConfirmMode::No);
        assert_eq!(resolve_confirm_mode(&tty, ""), ConfirmMode::Prompt);
        assert_eq!(resolve_confirm_mode(&none, "garbage"), ConfirmMode::No);
        assert_eq!(resolve_confirm_mode(&tty, " YES "), ConfirmMode::Yes);
        assert_eq!(resolve_confirm_mode(&none, "auto"), ConfirmMode::Auto);
        assert_eq!(resolve_confirm_mode(&none, "prompt"), ConfirmMode::Prompt);
    }

    fn ann(pairs: &[(&str, &str)]) -> BTreeMap<String, String> {
        pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect()
    }

    #[test]
    fn dry_run_table() {
        let p = |pairs: &[(&str, &str)]| resolve_dry_run_policy("do", &ann(pairs));
        let se = |v| ("kit/side-effect", v);
        assert_eq!(p(&[se("write")]), DryRunPolicy::Allow);
        assert_eq!(p(&[se("destructive-shared")]), DryRunPolicy::Allow);
        assert_eq!(p(&[se("read")]), DryRunPolicy::NoOp);
        assert_eq!(p(&[se("interactive")]), DryRunPolicy::RejectInteractive);
        assert_eq!(p(&[]), DryRunPolicy::RejectUntagged);
        assert_eq!(p(&[se("")]), DryRunPolicy::RejectUntagged);
        assert_eq!(p(&[se("bogus")]), DryRunPolicy::RejectMalformed);
        assert_eq!(
            p(&[se("write"), ("kit/dry-run", "opted-out")]),
            DryRunPolicy::RejectOptOut
        );
        assert_eq!(p(&[("kit/dry-run", "supported")]), DryRunPolicy::Allow);
    }

    #[test]
    fn dry_run_previews_an_inferred_destructive_name() {
        let a = |pairs: &[(&str, &str)]| ann(pairs);
        for name in super::super::model::DESTRUCTIVE_NAMES {
            assert_eq!(
                resolve_dry_run_policy(name, &a(&[])),
                DryRunPolicy::Allow,
                "{name}"
            );
            assert_eq!(
                resolve_dry_run_policy(name, &a(&[("kit/side-effect", "")])),
                DryRunPolicy::Allow,
                "{name} empty tag"
            );
        }
        // Non-destructive names keep the untagged refusal.
        assert_eq!(
            resolve_dry_run_policy("list", &a(&[])),
            DryRunPolicy::RejectUntagged
        );
        assert_eq!(
            resolve_dry_run_policy("delete-all", &a(&[])),
            DryRunPolicy::RejectUntagged
        );
        // A declaration is never inferred over.
        assert_eq!(
            resolve_dry_run_policy("delete", &a(&[("kit/side-effect", "bogus")])),
            DryRunPolicy::RejectMalformed
        );
        assert_eq!(
            resolve_dry_run_policy("delete", &a(&[("kit/side-effect", "read")])),
            DryRunPolicy::NoOp
        );
        assert_eq!(
            resolve_dry_run_policy("delete", &a(&[("kit/dry-run", "opted-out")])),
            DryRunPolicy::RejectOptOut
        );
    }

    #[test]
    fn opt_out_refusal_carries_the_rationale() {
        let a = ann(&[("kit/dry-run-rationale", "talks to a remote")]);
        let e = dry_run_refusal("t sync", &a, DryRunPolicy::RejectOptOut).unwrap();
        assert!(e.message.ends_with("(talks to a remote)"), "{}", e.message);
        assert_eq!(e.exit_code, 2);
        assert!(dry_run_refusal("t ls", &a, DryRunPolicy::NoOp).is_none());
    }
}
