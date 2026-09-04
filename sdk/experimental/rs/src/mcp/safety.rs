//! Surface-exposure policy gate.
//!
//! Ports Go's `cmdsurface/safety.go`: [`SafetyClass`] (the bridge's read
//! of a leaf's safety annotations), [`Surface`] (the transport a call
//! arrives on), and [`Policy`] (which surface may invoke which class).
//!
//! This is **not** the Factor 10 delegation guard (`--force` / TTY
//! checks). That mechanism answers "may this CLI invocation proceed
//! unattended"; this one answers "may this *transport* reach this leaf
//! at all". ADR 0043 §5 keeps them separate deliberately.
//!
//! The default is deny-by-default for remote destruction:
//! [`Policy::default_policy`] leaves `allow_destructive_on` empty, so no
//! remote surface may invoke a destructive leaf.

use std::collections::BTreeSet;

/// A transport a command can be invoked through.
///
/// `Cli` and `Lib` are the local-runtime surfaces and are always
/// permitted; every other variant is remote and subject to the
/// destructive ceiling.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
#[non_exhaustive]
pub enum Surface {
    /// Local interactive CLI.
    Cli,
    /// In-process library call.
    Lib,
    /// MCP surface. One value for **both** spec revisions: enablement,
    /// policy, and sink filters treat 2024-11-05 and 2026-07-28 as a
    /// single transport (ADR 0042, "One surface, not two").
    Mcp,
    /// JSON-RPC surface.
    Rpc,
    /// REST surface.
    Rest,
    /// Webhook surface.
    Webhook,
    /// Event-bus surface.
    Bus,
    /// Cron/scheduled surface.
    Cron,
}

impl Surface {
    /// The wire name, matching Go's `Surface` string values. Used in
    /// the destructive-block message, which is fixture-pinned.
    #[must_use]
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Cli => "cli",
            Self::Lib => "lib",
            Self::Mcp => "mcp",
            Self::Rpc => "rpc",
            Self::Rest => "rest",
            Self::Webhook => "webhook",
            Self::Bus => "bus",
            Self::Cron => "cron",
        }
    }
}

impl std::fmt::Display for Surface {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

/// The bridge's read of one leaf's safety annotations.
///
/// A default value describes a read-only, no-auth command — the same
/// zero-value semantics Go's `Classify` gives a command with no
/// annotations.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct SafetyClass {
    /// Set when `kit/side-effect` names a destructive tier
    /// (`destructive`, `destructive-local`, `destructive-shared`).
    pub destructive: bool,
    /// Set when `kit/auth-required` is `"true"`.
    pub auth_required: bool,
    /// Set when `kit/requires-confirmation` is `"true"`.
    pub requires_confirmation: bool,
    /// Parsed `kit/permissions` (comma-separated). Empty when unset.
    pub permissions: Vec<String>,
    /// Parsed `kit/exit-codes` (comma-separated). Empty when unset.
    pub exit_codes: Vec<String>,
}

impl SafetyClass {
    /// Reads a `kit/*` annotation map into a class, mirroring Go's
    /// `Classify`. Unknown keys are ignored; absent keys leave their
    /// field at the zero value.
    pub fn from_annotations<'a, I>(annotations: I) -> Self
    where
        I: IntoIterator<Item = (&'a str, &'a str)>,
    {
        let mut cls = Self::default();
        for (key, value) in annotations {
            match key {
                "kit/side-effect" => {
                    cls.destructive = matches!(
                        value,
                        "destructive" | "destructive-local" | "destructive-shared"
                    );
                }
                "kit/auth-required" => cls.auth_required = value == "true",
                "kit/requires-confirmation" => cls.requires_confirmation = value == "true",
                "kit/permissions" => cls.permissions = split_csv(value),
                "kit/exit-codes" => cls.exit_codes = split_csv(value),
                _ => {}
            }
        }
        cls
    }
}

/// Parses a comma-separated annotation value, trimming whitespace and
/// dropping empty entries — Go's `splitCSV`.
fn split_csv(s: &str) -> Vec<String> {
    if s.is_empty() {
        return Vec::new();
    }
    s.split(',')
        .map(str::trim)
        .filter(|p| !p.is_empty())
        .map(ToOwned::to_owned)
        .collect()
}

/// Gates which [`Surface`] may invoke a leaf of a given [`SafetyClass`].
///
/// [`Policy::default`] is the zero value: permissive only on the
/// local-runtime surfaces, because `allow_destructive_on` is empty.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Policy {
    /// Surfaces on which destructive leaves may be invoked. `Cli` and
    /// `Lib` are allowed regardless of this set's contents. **Empty
    /// means block every remote destructive invocation** — that is the
    /// default, not an unset sentinel.
    pub allow_destructive_on: BTreeSet<Surface>,
    /// Surfaces a leaf is exposed on when its per-command config omits
    /// the enabled field. Empty falls back to `[Cli, Lib, Mcp]`.
    pub default_enabled: BTreeSet<Surface>,
}

impl Policy {
    /// The conservative default: no remote surface may invoke a
    /// destructive command; default enablement is CLI + Lib + MCP.
    ///
    /// Mirrors Go's `DefaultPolicy()` exactly, including the
    /// empty-`allow_destructive_on` "block all" semantics.
    #[must_use]
    pub fn default_policy() -> Self {
        Self {
            allow_destructive_on: BTreeSet::new(),
            default_enabled: [Surface::Cli, Surface::Lib, Surface::Mcp]
                .into_iter()
                .collect(),
        }
    }

    /// Reports whether `class` may be invoked via `surface`.
    ///
    /// 1. `Cli` and `Lib` are always allowed (local runtime).
    /// 2. Non-destructive commands are allowed on every other surface.
    /// 3. Destructive commands are allowed only when `surface` is in
    ///    `allow_destructive_on`.
    ///
    /// Per-leaf enablement is a separate gate; this enforces only the
    /// destructive ceiling.
    #[must_use]
    pub fn allowed(&self, class: &SafetyClass, surface: Surface) -> bool {
        if matches!(surface, Surface::Cli | Surface::Lib) {
            return true;
        }
        if !class.destructive {
            return true;
        }
        self.allow_destructive_on.contains(&surface)
    }

    /// `default_enabled`, or the package-wide fallback when unset.
    #[must_use]
    pub fn resolved_defaults(&self) -> BTreeSet<Surface> {
        if self.default_enabled.is_empty() {
            return [Surface::Cli, Surface::Lib, Surface::Mcp]
                .into_iter()
                .collect();
        }
        self.default_enabled.clone()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn local_surfaces_always_allowed_even_when_destructive() {
        let policy = Policy::default_policy();
        let destructive = SafetyClass {
            destructive: true,
            ..SafetyClass::default()
        };
        assert!(policy.allowed(&destructive, Surface::Cli));
        assert!(policy.allowed(&destructive, Surface::Lib));
    }

    #[test]
    fn default_policy_blocks_destructive_on_every_remote_surface() {
        let policy = Policy::default_policy();
        let destructive = SafetyClass {
            destructive: true,
            ..SafetyClass::default()
        };
        for surface in [
            Surface::Mcp,
            Surface::Rpc,
            Surface::Rest,
            Surface::Webhook,
            Surface::Bus,
            Surface::Cron,
        ] {
            assert!(
                !policy.allowed(&destructive, surface),
                "{surface} must be blocked by the default policy"
            );
        }
    }

    #[test]
    fn non_destructive_allowed_on_remote_surfaces() {
        let policy = Policy::default_policy();
        let read_only = SafetyClass::default();
        assert!(policy.allowed(&read_only, Surface::Mcp));
    }

    #[test]
    fn destructive_allowed_only_on_listed_surface() {
        let policy = Policy {
            allow_destructive_on: [Surface::Mcp].into_iter().collect(),
            ..Policy::default_policy()
        };
        let destructive = SafetyClass {
            destructive: true,
            ..SafetyClass::default()
        };
        assert!(policy.allowed(&destructive, Surface::Mcp));
        assert!(!policy.allowed(&destructive, Surface::Rest));
    }

    #[test]
    fn classify_reads_destructive_tiers() {
        for tier in ["destructive", "destructive-local", "destructive-shared"] {
            let cls = SafetyClass::from_annotations([("kit/side-effect", tier)]);
            assert!(cls.destructive, "{tier} must classify as destructive");
        }
        for tier in ["read", "write", ""] {
            let cls = SafetyClass::from_annotations([("kit/side-effect", tier)]);
            assert!(
                !cls.destructive,
                "{tier:?} must not classify as destructive"
            );
        }
    }

    #[test]
    fn classify_reads_auth_and_confirmation() {
        let cls = SafetyClass::from_annotations([
            ("kit/auth-required", "true"),
            ("kit/requires-confirmation", "true"),
        ]);
        assert!(cls.auth_required);
        assert!(cls.requires_confirmation);

        // Only the exact string "true" enables the flags.
        let cls = SafetyClass::from_annotations([("kit/auth-required", "yes")]);
        assert!(!cls.auth_required);
    }

    #[test]
    fn split_csv_trims_and_drops_empties() {
        let cls = SafetyClass::from_annotations([("kit/permissions", " a , ,b ")]);
        assert_eq!(cls.permissions, vec!["a".to_string(), "b".to_string()]);
        let cls = SafetyClass::from_annotations([("kit/permissions", "")]);
        assert!(cls.permissions.is_empty());
    }

    #[test]
    fn resolved_defaults_falls_back_when_unset() {
        let policy = Policy::default();
        assert_eq!(
            policy.resolved_defaults(),
            [Surface::Cli, Surface::Lib, Surface::Mcp]
                .into_iter()
                .collect()
        );
    }
}
