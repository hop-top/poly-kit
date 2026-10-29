//! Root aliases. Ports Go `console/cli/alias.go`: validation with Go's
//! error texts, a clap alias for a direct child and Go's flagless shim
//! for a deeper command; [`Root::alias_with`] registers a proxy shaped by
//! [`AliasOptions`] instead. Ports Go `console/alias` name validation and
//! cycle detection as free functions.

use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::fmt;

use clap::{Arg, Command};

use super::root::{resolve_path, Root};

/// The positional arguments a deep-alias shim accepts, unparsed.
pub const SHIM_ARGS: &str = "args";

/// An alias that cannot be registered. The texts are Go's.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AliasError(String);

impl fmt::Display for AliasError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for AliasError {}

/// What `<alias> --help` shows on a proxy.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum AliasHelp {
    /// The proxy's own help: its description. The default.
    #[default]
    Own,
    /// No help flag on the proxy: `--help` lands in [`SHIM_ARGS`], so the
    /// caller's dispatch hands it to the target.
    Passthrough,
}

type Describe = Box<dyn Fn(&str, &str) -> String>;

/// How [`Root::alias_with`] shapes an alias proxy. Rust has no dispatch
/// hook: every proxy is register-only and the caller dispatches it
/// through [`Root::alias_target`].
pub struct AliasOptions {
    hidden: bool,
    describe: Option<Describe>,
    help: AliasHelp,
}

impl Default for AliasOptions {
    fn default() -> Self {
        AliasOptions {
            hidden: true,
            describe: None,
            help: AliasHelp::Own,
        }
    }
}

impl fmt::Debug for AliasOptions {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("AliasOptions")
            .field("hidden", &self.hidden)
            .field("describe", &self.describe.is_some())
            .field("help", &self.help)
            .finish()
    }
}

impl AliasOptions {
    /// The defaults: hidden, described `Alias for <target>`, own help.
    pub fn new() -> Self {
        Self::default()
    }

    /// Whether the proxy is hidden from help.
    pub fn hidden(mut self, hidden: bool) -> Self {
        self.hidden = hidden;
        self
    }

    /// The proxy's description from its name and target path, in place
    /// of `Alias for <target>`.
    pub fn describe(mut self, f: impl Fn(&str, &str) -> String + 'static) -> Self {
        self.describe = Some(Box::new(f));
        self
    }

    /// What `<alias> --help` shows.
    pub fn help(mut self, help: AliasHelp) -> Self {
        self.help = help;
        self
    }
}

impl Root {
    /// Registers `name` as a root-level alias for the command at `target`
    /// (a space-separated path). A direct child gains a clap alias. A
    /// deeper command gets Go's shim: a hidden root subcommand named
    /// `name` with none of the target's flags, taking any positional
    /// arguments, which the caller dispatches through
    /// [`Root::alias_target`].
    pub fn alias(&mut self, name: &str, target: &str) -> Result<(), AliasError> {
        let words = self.check_alias(name, target)?;
        let path = words.join(" ");
        let cmd = std::mem::take(&mut self.cmd);
        self.cmd = if words.len() == 1 {
            cmd.mut_subcommand(&words[0], |c| c.alias(name.to_string()))
        } else {
            let shim = Command::new(name.to_string())
                .about(format!("Alias for {path}"))
                .hide(true)
                .arg(Arg::new(SHIM_ARGS).num_args(0..).hide(true));
            cmd.subcommand(shim)
        };
        self.aliases.insert(name.to_string(), path);
        Ok(())
    }

    /// Registers `name` as a proxy for the command at `target`, whatever
    /// its depth: a root subcommand named `name`, shaped by `opts`, that
    /// takes every argument unparsed (flags included) in [`SHIM_ARGS`].
    /// The caller dispatches it through [`Root::alias_target`]. Same
    /// validation and errors as [`Root::alias`].
    pub fn alias_with(
        &mut self,
        name: &str,
        target: &str,
        opts: &AliasOptions,
    ) -> Result<(), AliasError> {
        let words = self.check_alias(name, target)?;
        let path = words.join(" ");
        let about = match &opts.describe {
            Some(f) => f(name, &path),
            None => format!("Alias for {path}"),
        };
        let mut proxy = Command::new(name.to_string())
            .about(about)
            .hide(opts.hidden)
            .arg(
                Arg::new(SHIM_ARGS)
                    .num_args(0..)
                    .trailing_var_arg(true)
                    .allow_hyphen_values(true)
                    .hide(true),
            );
        if opts.help == AliasHelp::Passthrough {
            proxy = proxy.disable_help_flag(true);
        }
        let cmd = std::mem::take(&mut self.cmd);
        self.cmd = cmd.subcommand(proxy);
        self.aliases.insert(name.to_string(), path);
        Ok(())
    }

    /// Go's registration checks; the target's path words.
    fn check_alias(&self, name: &str, target: &str) -> Result<Vec<String>, AliasError> {
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
        resolve_path(&self.cmd, target).map_err(AliasError)
    }

    /// Every registered alias with its target path.
    pub fn aliases(&self) -> &BTreeMap<String, String> {
        &self.aliases
    }

    /// The target path of alias `name`.
    pub fn alias_target(&self, name: &str) -> Option<&str> {
        self.aliases.get(name).map(String::as_str)
    }
}

/// The rule an alias name failed; see [`validate_alias_name`].
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum AliasNameErrorKind {
    /// The name is the empty string.
    Empty,
    /// The name holds a Unicode White_Space character.
    Whitespace,
    /// The name is one of [`AliasNameRules::builtins`].
    Builtin,
    /// The name is a key of [`AliasNameRules::existing`].
    Duplicate,
}

impl AliasNameErrorKind {
    /// The kind's contract name: `empty`, `whitespace`, `builtin`,
    /// `duplicate`.
    pub fn as_str(self) -> &'static str {
        match self {
            AliasNameErrorKind::Empty => "empty",
            AliasNameErrorKind::Whitespace => "whitespace",
            AliasNameErrorKind::Builtin => "builtin",
            AliasNameErrorKind::Duplicate => "duplicate",
        }
    }
}

/// An alias name [`validate_alias_name`] refused. Displays Go's texts.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AliasNameError {
    /// The rule that failed.
    pub kind: AliasNameErrorKind,
    /// The refused name.
    pub name: String,
    /// The existing alias's target, for [`AliasNameErrorKind::Duplicate`].
    pub target: Option<String>,
}

impl fmt::Display for AliasNameError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let name = &self.name;
        match self.kind {
            AliasNameErrorKind::Builtin => {
                write!(f, "alias {name:?} collides with command {name:?}")
            }
            AliasNameErrorKind::Duplicate => write!(
                f,
                "alias {name:?} already registered for {:?}",
                self.target.as_deref().unwrap_or_default()
            ),
            _ => write!(
                f,
                "alias name {name:?} must be non-empty without whitespace"
            ),
        }
    }
}

impl std::error::Error for AliasNameError {}

/// What [`validate_alias_name`] checks a name against.
#[derive(Clone, Copy, Debug, Default)]
pub struct AliasNameRules<'a> {
    /// Names an alias must not take: the tool's commands and reserved
    /// words. Compared exactly.
    pub builtins: &'a [&'a str],
    /// The alias map the name would join.
    pub existing: Option<&'a BTreeMap<String, String>>,
}

/// Checks an alias name without a store, config file or command tree.
/// Rules run in order and the first failure is returned: empty,
/// whitespace (any Unicode White_Space character), builtin, duplicate.
pub fn validate_alias_name(name: &str, rules: &AliasNameRules<'_>) -> Result<(), AliasNameError> {
    let fail = |kind, target: Option<&String>| {
        Err(AliasNameError {
            kind,
            name: name.to_string(),
            target: target.cloned(),
        })
    };
    if name.is_empty() {
        return fail(AliasNameErrorKind::Empty, None);
    }
    if name.chars().any(char::is_whitespace) {
        return fail(AliasNameErrorKind::Whitespace, None);
    }
    if rules.builtins.contains(&name) {
        return fail(AliasNameErrorKind::Builtin, None);
    }
    if let Some(t) = rules.existing.and_then(|m| m.get(name)) {
        return fail(AliasNameErrorKind::Duplicate, Some(t));
    }
    Ok(())
}

/// Reports the alias cycles in `aliases` (name -> target). An alias
/// points at another when the first word of its target is a key of the
/// map. Each cycle is a path that starts at its smallest name, follows
/// the aliases and ends with that name again (`[a, b, a]`); cycles are
/// sorted by first name. An alias that only leads into a cycle is not
/// part of one.
pub fn detect_cycles(aliases: &BTreeMap<String, String>) -> Vec<Vec<String>> {
    let next = |name: &str| -> Option<&str> {
        let first = aliases.get(name)?.split_whitespace().next()?;
        aliases.contains_key(first).then_some(first)
    };
    let mut done: BTreeSet<&str> = BTreeSet::new();
    let mut cycles: Vec<Vec<String>> = Vec::new();
    for start in aliases.keys() {
        if done.contains(start.as_str()) {
            continue;
        }
        let mut pos: HashMap<&str, usize> = HashMap::new();
        let mut walk: Vec<&str> = Vec::new();
        let mut cur = Some(start.as_str());
        while let Some(c) = cur.filter(|c| !done.contains(c)) {
            if let Some(&at) = pos.get(c) {
                let members = &walk[at..];
                let low = (0..members.len()).min_by_key(|&i| members[i]).unwrap_or(0);
                let mut path: Vec<String> = members[low..]
                    .iter()
                    .chain(&members[..low])
                    .map(|s| s.to_string())
                    .collect();
                path.push(path[0].clone());
                cycles.push(path);
                break;
            }
            pos.insert(c, walk.len());
            walk.push(c);
            cur = next(c);
        }
        done.extend(walk);
    }
    cycles.sort_by(|a, b| a[0].cmp(&b[0]));
    cycles
}

#[cfg(test)]
mod tests {
    use super::*;

    fn text(name: &str, rules: &AliasNameRules<'_>) -> String {
        validate_alias_name(name, rules).unwrap_err().to_string()
    }

    #[test]
    fn name_error_texts_are_gos() {
        let none = AliasNameRules::default();
        assert_eq!(
            text("", &none),
            r#"alias name "" must be non-empty without whitespace"#
        );
        assert_eq!(
            text("a b", &none),
            r#"alias name "a b" must be non-empty without whitespace"#
        );
        let builtins = AliasNameRules {
            builtins: &["task"],
            existing: None,
        };
        assert_eq!(
            text("task", &builtins),
            r#"alias "task" collides with command "task""#
        );
        let existing = BTreeMap::from([("d".to_string(), "deploy".to_string())]);
        let dup = AliasNameRules {
            builtins: &[],
            existing: Some(&existing),
        };
        assert_eq!(
            text("d", &dup),
            r#"alias "d" already registered for "deploy""#
        );
    }

    #[test]
    fn alias_with_proxies_a_direct_child() {
        let mut root = Root::new(Command::new("app").subcommand(Command::new("deploy")));
        root.alias_with("d", "deploy", &AliasOptions::new().hidden(false))
            .unwrap();
        let cmd = root.command();
        let proxy = cmd.find_subcommand("d").unwrap();
        assert_eq!(proxy.get_name(), "d");
        assert!(!proxy.is_hide_set());
        assert_eq!(root.alias_target("d"), Some("deploy"));
        let m = cmd
            .try_get_matches_from(["app", "d", "--env", "x"])
            .unwrap();
        let (_, sm) = m.subcommand().unwrap();
        let args: Vec<&String> = sm.get_many(SHIM_ARGS).unwrap().collect();
        assert_eq!(args, ["--env", "x"]);
    }
}
