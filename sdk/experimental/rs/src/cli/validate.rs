//! `Root::validate` and the signature validators. Ports the shipped
//! side-effect arm and the Layer-A checks of Go `Root.Validate`
//! (Short/Long, noun-verb shape, the configurable gates) and
//! `Root.ValidateSignature` (local-globals, reserved-name,
//! depth-hierarchical, passthrough).
//!
//! Not ported, because the surfaces they police are not in kit-rs: the
//! `kit/idempotent` and `kit/cache-ttl` arms, the `kit/output-schema`
//! parse and the reserved `status` subcommand (toolspec and idempotency
//! layers), and the `spec coverage` hint on a side-effect failure.

use std::fmt;

use crate::output::CliError;

use super::model::CommandInfo;
use super::policy::{is_destructive_like, is_valid_tier, is_write_like};
use super::root::Root;

const TOP_LEVEL_VERB: &str = "kit/top-level-verb";
const HIERARCHICAL: &str = "kit/hierarchical";
const PASSTHROUGH: &str = "kit/passthrough";
const RESERVED: &str = "kit/reserved";
const RESERVES_CHILDREN: &str = "kit/reserves-children";
const EXEMPT: &str = "kit/exempt-validation";
const EXAMPLES: &str = "kit/examples";
const NEXT_STEPS: &str = "kit/next-steps";
const DEFAULT_MAX_TOP_LEVEL_VERBS: usize = 10;
const DEFAULT_MAX_HIERARCHY_DEPTH: usize = 3;
const HARD_MAX_HIERARCHY_DEPTH: usize = 5;

/// How `execute` reports a validation failure.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum ValidationFailureMode {
    /// Renders the failure to stderr and returns it as a USAGE error
    /// (exit 2); Go's `Exit` mode ends the process, which a library
    /// leaves to `main`.
    #[default]
    Exit,
    /// Returns the failure without rendering it.
    Error,
    /// Warns on stderr and carries on; discouraged outside recovery
    /// tooling.
    Silent,
}

/// What the signature validators do with a violation.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum SignatureStrictness {
    /// Checks are not acted on.
    #[default]
    Silent,
    /// Violations are warned on stderr.
    Warn,
    /// Any violation fails like a validation failure.
    Reject,
}

/// What `kit/passthrough` annotations do under validation.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum PassthroughStrictness {
    /// Accepted (the default).
    #[default]
    Warn,
    /// Refused as an error.
    Reject,
    /// Accepted silently.
    Silent,
}

/// The leaves that fail [`Root::validate`], by rule. Paths are full
/// command paths (`tool a b`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ValidationError {
    /// Runnable leaves lacking `kit/side-effect`.
    pub missing: Vec<String>,
    /// Leaves whose `kit/side-effect` names no tier, as `path="value"`.
    pub invalid: Vec<String>,
    /// Runnable and group nodes without an about text.
    pub missing_short: Vec<String>,
    /// Runnable nodes without a long description.
    pub missing_long: Vec<String>,
    /// Opted-out write/destructive leaves without a rationale.
    pub missing_dry_run_rationale: Vec<String>,
    /// Destructive leaves without `kit/destructive-token: required`.
    pub missing_destructive_token: Vec<String>,
    /// Runnable leaves without `kit/examples`.
    pub missing_examples: Vec<String>,
    /// Non-read leaves without `kit/next-steps`.
    pub missing_next_steps: Vec<String>,
    /// Depth-1 leaves without `kit/top-level-verb`.
    pub unannotated_top_level_leaf: Vec<String>,
    /// The count and cap when depth-1 leaves exceed the maximum.
    pub too_many_top_level_verbs: Vec<String>,
    /// Depth >= 3 paths missing a `kit/hierarchical` chain.
    pub unannotated_depth_exceedance: Vec<String>,
    /// Paths deeper than the maximum hierarchy depth.
    pub hierarchy_depth_exceeded: Vec<String>,
    /// `kit/passthrough` leaves under `reject` strictness.
    pub passthrough_rejected: Vec<String>,
}

impl ValidationError {
    /// Whether any rule failed.
    pub fn has_issues(&self) -> bool {
        self.buckets().iter().any(|(_, v)| !v.is_empty())
    }

    fn buckets(&self) -> [(&'static str, &Vec<String>); 13] {
        [
            ("missing kit/side-effect annotation", &self.missing),
            ("with invalid kit/side-effect", &self.invalid),
            ("missing Short", &self.missing_short),
            ("missing Long", &self.missing_long),
            (
                "missing kit/dry-run-rationale",
                &self.missing_dry_run_rationale,
            ),
            (
                "missing kit/destructive-token",
                &self.missing_destructive_token,
            ),
            ("missing kit/examples", &self.missing_examples),
            ("missing kit/next-steps", &self.missing_next_steps),
            (
                "depth-1 leaf missing kit/top-level-verb",
                &self.unannotated_top_level_leaf,
            ),
            ("exceeding MaxTopLevelVerbs", &self.too_many_top_level_verbs),
            (
                "at depth>=3 missing kit/hierarchical chain",
                &self.unannotated_depth_exceedance,
            ),
            (
                "exceeding MaxHierarchyDepth",
                &self.hierarchy_depth_exceeded,
            ),
            (
                "annotated kit/passthrough under reject strictness",
                &self.passthrough_rejected,
            ),
        ]
    }

    /// The failure as a USAGE envelope (exit 2).
    pub fn to_cli_error(&self) -> CliError {
        CliError::usage(self.to_string())
    }
}

impl fmt::Display for ValidationError {
    /// Go's wording: `cli validation failed: N leaf command(s) <rule>:
    /// paths; ...`, rules in a fixed order.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("cli validation failed")?;
        let mut prior = false;
        for (label, items) in self.buckets() {
            if items.is_empty() {
                continue;
            }
            f.write_str(if prior { ";" } else { ":" })?;
            write!(
                f,
                " {} leaf command(s) {label}: {}",
                items.len(),
                items.join(", ")
            )?;
            prior = true;
        }
        Ok(())
    }
}

impl std::error::Error for ValidationError {}

/// One failure of a signature check.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SignatureViolation {
    /// The offending command's full path.
    pub path: String,
    /// `local-globals`, `reserved-name`, `depth-hierarchical` or
    /// `passthrough`.
    pub check: &'static str,
    /// What is wrong.
    pub detail: String,
    /// `error` (would trip reject mode) or `warning`.
    pub severity: &'static str,
}

/// The violations of a [`Root::validate_signature`] walk.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SignatureReport {
    /// In walk order.
    pub violations: Vec<SignatureViolation>,
}

impl SignatureReport {
    /// Whether any check failed.
    pub fn has_violations(&self) -> bool {
        !self.violations.is_empty()
    }

    /// One line per violation, or `no signature violations`.
    pub fn render_text(&self) -> String {
        if self.violations.is_empty() {
            return "no signature violations\n".into();
        }
        self.violations
            .iter()
            .map(|v| format!("{} [{}/{}] {}\n", v.path, v.check, v.severity, v.detail))
            .collect()
    }

    /// The report as one JSON object, `{"violations":[...]}`.
    pub fn render_json(&self) -> String {
        let items: Vec<_> = self
            .violations
            .iter()
            .map(|v| {
                serde_json::json!({
                    "path": v.path, "check": v.check, "detail": v.detail, "severity": v.severity,
                })
            })
            .collect();
        format!("{}\n", serde_json::json!({ "violations": items }))
    }

    /// The one-line summary reject mode fails with.
    pub fn summary(&self) -> String {
        if self.violations.is_empty() {
            return "signature validation failed".into();
        }
        let parts: Vec<String> = self
            .violations
            .iter()
            .map(|v| format!("{} [{}] {}", v.path, v.check, v.detail))
            .collect();
        format!("signature validation failed: {}", parts.join("; "))
    }
}

fn truthy(c: &CommandInfo, key: &str) -> bool {
    matches!(c.annotations.get(key).map(String::as_str), Some("true"))
}

/// Commands kit ships (the built-in `completion` and its leaves) or the
/// author exempted.
fn is_builtin(root: &Root, c: &CommandInfo) -> bool {
    c.path.first().is_some_and(|top| root.is_builtin_top(top))
        || c.annotations.get(EXEMPT).map(String::as_str) == Some("true")
}

fn passthrough_leaf(c: &CommandInfo) -> bool {
    c.commands.is_empty() && c.invocable && c.args.iter().any(|a| a.variadic && !a.required)
}

impl Root {
    fn full_path(&self, c: &CommandInfo) -> String {
        let name = self.cmd.get_name();
        if c.path.is_empty() {
            name.to_string()
        } else {
            format!("{name} {}", c.path.join(" "))
        }
    }

    fn is_reserved(&self, model: &CommandInfo, top: &str) -> bool {
        self.is_builtin_top(top) || model.find(top).is_some_and(|c| truthy(c, RESERVED))
    }

    /// Walks the tree and refuses what Go's `Root.Validate` refuses:
    /// runnable leaves without a valid `kit/side-effect` and, unless
    /// the [`Config`](super::Config) disabled it, the Layer-A rules
    /// (about/long texts, noun-verb shape, the `enforce_*` gates).
    /// Built-ins and `kit/exempt-validation` leaves are skipped.
    pub fn validate(&self) -> Result<(), ValidationError> {
        let model = self.model();
        let cfg = self.config.as_ref();
        let mut ve = ValidationError::default();
        // Shipped arm: every runnable leaf declares a valid tier.
        model.walk(&mut |c| {
            if c.path.is_empty() || !c.commands.is_empty() || !c.invocable || is_builtin(self, c) {
                return;
            }
            match c.side_effect() {
                None | Some("") => ve.missing.push(self.full_path(c)),
                Some(s) if !is_valid_tier(s) => {
                    ve.invalid.push(format!("{}={s:?}", self.full_path(c)))
                }
                _ => {}
            }
        });
        // Layer A.
        model.walk(&mut |c| {
            if c.path.is_empty() || is_builtin(self, c) {
                return;
            }
            let group = !c.commands.is_empty() && !c.invocable;
            if !c.invocable && !group {
                return;
            }
            if c.short.is_empty() {
                ve.missing_short.push(self.full_path(c));
            }
            if c.invocable && c.long.is_empty() {
                ve.missing_long.push(self.full_path(c));
            }
        });
        self.check_shape(&model, &mut ve);
        self.check_gates(&model, &mut ve);
        let _ = cfg;
        if ve.has_issues() {
            Err(ve)
        } else {
            Ok(())
        }
    }

    fn check_shape(&self, model: &CommandInfo, ve: &mut ValidationError) {
        let cfg = self.config.as_ref();
        let max_top = cfg
            .map(|c| c.max_top_level_verbs)
            .filter(|n| *n > 0)
            .unwrap_or(DEFAULT_MAX_TOP_LEVEL_VERBS);
        let max_depth = cfg
            .map(|c| c.max_hierarchy_depth)
            .filter(|n| *n > 0)
            .unwrap_or(DEFAULT_MAX_HIERARCHY_DEPTH)
            .min(HARD_MAX_HIERARCHY_DEPTH);
        let reject_passthrough =
            cfg.is_some_and(|c| c.passthrough_strictness == PassthroughStrictness::Reject);
        let mut top_level = 0usize;
        model.walk(&mut |c| {
            let depth = c.path.len();
            if depth == 0 || is_builtin(self, c) {
                return;
            }
            if depth > max_depth {
                ve.hierarchy_depth_exceeded
                    .push(format!("{} (depth={depth}>{max_depth})", self.full_path(c)));
            }
            if reject_passthrough && truthy(c, PASSTHROUGH) {
                ve.passthrough_rejected.push(self.full_path(c));
            }
            if !c.invocable {
                if depth >= 2 && !self.is_reserved(model, &c.path[0]) && !truthy(c, HIERARCHICAL) {
                    ve.unannotated_depth_exceedance.push(self.full_path(c));
                }
                return;
            }
            match depth {
                1 => {
                    if self.is_reserved(model, &c.name) {
                        return;
                    }
                    if !truthy(c, TOP_LEVEL_VERB) {
                        ve.unannotated_top_level_leaf.push(self.full_path(c));
                    }
                    top_level += 1;
                }
                2 => {}
                _ => {
                    if !self.depth_three_ancestors_ok(model, c) {
                        ve.unannotated_depth_exceedance.push(self.full_path(c));
                    }
                }
            }
        });
        if top_level > max_top {
            ve.too_many_top_level_verbs.push(format!(
                "{}: {top_level} top-level verbs > MaxTopLevelVerbs={max_top}",
                self.cmd.get_name()
            ));
        }
    }

    /// A leaf at depth >= 3 passes when its depth-1 ancestor is
    /// reserved or every intermediate ancestor is `kit/hierarchical`.
    fn depth_three_ancestors_ok(&self, model: &CommandInfo, leaf: &CommandInfo) -> bool {
        if self.is_reserved(model, &leaf.path[0]) {
            return true;
        }
        (1..leaf.path.len()).all(|n| {
            model
                .find(&leaf.path[..n].join(" "))
                .is_some_and(|a| truthy(a, HIERARCHICAL))
        })
    }

    fn check_gates(&self, model: &CommandInfo, ve: &mut ValidationError) {
        let Some(cfg) = self.config.as_ref() else {
            return;
        };
        if !cfg.enforce_dry_run_rationale && !cfg.enforce_destructive_token && !cfg.enforce_guidance
        {
            return;
        }
        model.walk(&mut |c| {
            if c.path.is_empty() || !c.commands.is_empty() || !c.invocable || is_builtin(self, c) {
                return;
            }
            let se = c.side_effect().unwrap_or("");
            let mutating = is_write_like(se) || is_destructive_like(se);
            if cfg.enforce_dry_run_rationale
                && c.annotations.get(super::gate::DRY_RUN).map(String::as_str) == Some("opted-out")
                && mutating
                && c.annotations
                    .get(super::gate::DRY_RUN_RATIONALE)
                    .is_none_or(|r| r.is_empty())
            {
                ve.missing_dry_run_rationale.push(self.full_path(c));
            }
            if cfg.enforce_destructive_token
                && is_destructive_like(se)
                && !matches!(
                    c.annotations
                        .get(super::gate::DESTRUCTIVE_TOKEN)
                        .map(String::as_str),
                    Some("required" | "true")
                )
            {
                ve.missing_destructive_token.push(self.full_path(c));
            }
            if cfg.enforce_guidance {
                if !c.annotations.contains_key(EXAMPLES) {
                    ve.missing_examples.push(self.full_path(c));
                }
                if se != "read" && !c.annotations.contains_key(NEXT_STEPS) {
                    ve.missing_next_steps.push(self.full_path(c));
                }
            }
        });
    }

    /// Runs the four signature checks. The walk runs whatever the
    /// configured strictness; the strictness decides how `execute`
    /// reacts.
    pub fn validate_signature(&self) -> SignatureReport {
        let model = self.model();
        let mut report = SignatureReport::default();
        let globals: std::collections::BTreeSet<&str> = model
            .flags
            .iter()
            .filter(|f| f.persistent)
            .map(|f| f.name.as_str())
            .collect();
        // local-globals: a runnable leaf declaring a flag named like a
        // root persistent flag shadows it.
        model.walk(&mut |c| {
            if c.path.is_empty() || is_builtin(self, c) || !c.commands.is_empty() || !c.invocable {
                return;
            }
            let mut shadowed: Vec<&str> = c
                .flags
                .iter()
                .filter(|f| !f.persistent && globals.contains(f.name.as_str()))
                .map(|f| f.name.as_str())
                .collect();
            if shadowed.is_empty() {
                return;
            }
            shadowed.sort_unstable();
            report.violations.push(SignatureViolation {
                path: self.full_path(c),
                check: "local-globals",
                detail: format!("leaf redefines global flag(s): {}", shadowed.join(", ")),
                severity: "error",
            });
        });
        // reserved-name.
        model.walk(&mut |c| {
            if is_builtin(self, c) {
                return;
            }
            let Some(raw) = c.annotations.get(RESERVES_CHILDREN) else {
                return;
            };
            let reserved: Vec<&str> = raw
                .split(',')
                .map(str::trim)
                .filter(|s| !s.is_empty())
                .collect();
            for child in &c.commands {
                if !is_builtin(self, child) && reserved.contains(&child.name.as_str()) {
                    report.violations.push(SignatureViolation {
                        path: self.full_path(child),
                        check: "reserved-name",
                        detail: format!(
                            "child name {:?} is reserved by parent {:?}",
                            child.name,
                            self.full_path(c)
                        ),
                        severity: "error",
                    });
                }
            }
        });
        // depth-hierarchical: stricter than the Layer-A rule; reserved
        // ancestors get no pass.
        model.walk(&mut |c| {
            let depth = c.path.len();
            if depth < 3 || is_builtin(self, c) || !c.commands.is_empty() || !c.invocable {
                return;
            }
            let missing: Vec<String> = (1..depth)
                .filter_map(|n| {
                    let a = model.find(&c.path[..n].join(" "))?;
                    (!truthy(a, HIERARCHICAL)).then(|| self.full_path(a))
                })
                .collect();
            if missing.is_empty() {
                return;
            }
            report.violations.push(SignatureViolation {
                path: self.full_path(c),
                check: "depth-hierarchical",
                detail: format!(
                    "depth {depth} leaf requires kit/hierarchical on intermediate(s): {}",
                    missing.join(", ")
                ),
                severity: "error",
            });
        });
        // passthrough: an open positional list without the annotation.
        model.walk(&mut |c| {
            if c.path.is_empty() || is_builtin(self, c) {
                return;
            }
            if passthrough_leaf(c) && !truthy(c, PASSTHROUGH) {
                report.violations.push(SignatureViolation {
                    path: self.full_path(c),
                    check: "passthrough",
                    detail: "leaf accepts arbitrary arguments without kit/passthrough annotation"
                        .into(),
                    severity: "warning",
                });
            }
        });
        report
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::cli::{ArgSpec, CommandSpec, FlagSpec};

    fn leaf(name: &str) -> CommandSpec {
        CommandSpec::new(name)
            .about(name)
            .long_about("long")
            .side_effect("read")
            .annotate(TOP_LEVEL_VERB, "true")
            .handler(|_, _, _| Ok(()))
    }

    #[test]
    fn a_clean_tree_validates() {
        let mut root = Root::new(CommandSpec::new("t"));
        root.add_command(leaf("ls"));
        assert_eq!(root.validate(), Ok(()));
    }

    #[test]
    fn error_text_is_gos() {
        let mut root = Root::new(CommandSpec::new("t"));
        root.add_command(
            CommandSpec::new("rm")
                .about("rm")
                .annotate(TOP_LEVEL_VERB, "true")
                .handler(|_, _, _| Ok(())),
        );
        root.add_command(
            CommandSpec::new("bad")
                .about("bad")
                .long_about("l")
                .side_effect("nope")
                .annotate(TOP_LEVEL_VERB, "true")
                .handler(|_, _, _| Ok(())),
        );
        let e = root.validate().unwrap_err();
        assert_eq!(
            e.to_string(),
            "cli validation failed: 1 leaf command(s) missing kit/side-effect annotation: t rm; \
             1 leaf command(s) with invalid kit/side-effect: t bad=\"nope\"; \
             1 leaf command(s) missing Long: t rm"
        );
        assert_eq!(e.to_cli_error().exit_code, 2);
    }

    #[test]
    fn non_runnable_groups_and_builtins_are_exempt() {
        let mut root = Root::new(CommandSpec::new("t"));
        root.add_command(
            CommandSpec::new("g")
                .about("group")
                .annotate(HIERARCHICAL, "true")
                .subcommand(leaf("x").annotate(TOP_LEVEL_VERB, "false")),
        );
        // The completion command is built in and never listed.
        assert_eq!(root.validate(), Ok(()));
    }

    #[test]
    fn shape_rules() {
        let mut root = Root::new(CommandSpec::new("t"));
        root.add_command(
            CommandSpec::new("plain")
                .about("p")
                .long_about("l")
                .side_effect("read")
                .handler(|_, _, _| Ok(())),
        );
        let deep = CommandSpec::new("a").about("a").subcommand(
            CommandSpec::new("b").about("b").subcommand(
                CommandSpec::new("c")
                    .about("c")
                    .long_about("l")
                    .side_effect("read")
                    .handler(|_, _, _| Ok(())),
            ),
        );
        root.add_command(deep);
        let e = root.validate().unwrap_err();
        assert_eq!(e.unannotated_top_level_leaf, ["t plain"]);
        assert_eq!(e.unannotated_depth_exceedance, ["t a b", "t a b c"]);
    }

    #[test]
    fn top_level_cap() {
        let mut cfg = crate::cli::Config::new("t", "1", "t");
        cfg.max_top_level_verbs = 1;
        let mut root = Root::with_config(cfg);
        root.add_command(leaf("a"));
        root.add_command(leaf("b"));
        let e = root.validate().unwrap_err();
        assert_eq!(
            e.too_many_top_level_verbs,
            ["t: 2 top-level verbs > MaxTopLevelVerbs=1"]
        );
    }

    #[test]
    fn configurable_gates() {
        let mut cfg = crate::cli::Config::new("t", "1", "t");
        cfg.enforce_dry_run_rationale = true;
        cfg.enforce_destructive_token = true;
        cfg.enforce_guidance = true;
        let mut root = Root::with_config(cfg);
        root.add_command(
            leaf("rm")
                .side_effect("destructive")
                .annotate("kit/dry-run", "opted-out"),
        );
        let e = root.validate().unwrap_err();
        assert_eq!(e.missing_dry_run_rationale, ["t rm"]);
        assert_eq!(e.missing_destructive_token, ["t rm"]);
        assert_eq!(e.missing_examples, ["t rm"]);
        assert_eq!(e.missing_next_steps, ["t rm"]);
    }

    #[test]
    fn signature_checks() {
        let mut cfg = crate::cli::Config::new("t", "1", "t");
        cfg.signature_strictness = SignatureStrictness::Reject;
        let mut root = Root::with_config(cfg);
        root.add_command(leaf("shadow").flag(FlagSpec::bool("quiet")));
        root.add_command(
            CommandSpec::new("hold")
                .about("h")
                .annotate(RESERVES_CHILDREN, "taken, other")
                .subcommand(leaf("taken")),
        );
        root.add_command(leaf("open").arg(ArgSpec::new("rest").variadic()));
        let r = root.validate_signature();
        let got: Vec<(&str, &str)> = r
            .violations
            .iter()
            .map(|v| (v.path.as_str(), v.check))
            .collect();
        assert_eq!(
            got,
            [
                ("t shadow", "local-globals"),
                ("t hold taken", "reserved-name"),
                ("t open", "passthrough"),
            ]
        );
        assert_eq!(r.violations[2].severity, "warning");
        assert!(r
            .render_text()
            .contains("t shadow [local-globals/error] leaf redefines global flag(s): quiet"));
        assert!(r.render_json().starts_with("{\"violations\":["));
        assert!(r
            .summary()
            .starts_with("signature validation failed: t shadow [local-globals]"));
        assert_eq!(
            SignatureReport::default().render_text(),
            "no signature violations\n"
        );
    }

    #[test]
    fn signature_depth_wants_the_annotation_even_under_reserved() {
        let mut root = Root::new(CommandSpec::new("t"));
        root.add_command(
            CommandSpec::new("a")
                .about("a")
                .annotate(RESERVED, "true")
                .subcommand(
                    CommandSpec::new("b")
                        .about("b")
                        .annotate(HIERARCHICAL, "true")
                        .subcommand(leaf("c")),
                ),
        );
        let r = root.validate_signature();
        assert_eq!(r.violations.len(), 1);
        assert_eq!(r.violations[0].check, "depth-hierarchical");
        assert!(r.violations[0].detail.ends_with("t a"));
        // The Layer-A rule passes: the depth-1 ancestor is reserved.
        assert!(root.validate().is_ok());
    }
}
