//! The command model kit projects: a kit-owned, clap-free description of
//! the tree a CLI author defines. The api-server projection, OpenAPI
//! emission and any other surface read this, never clap types; clap is
//! the parsing provider behind [`Root`] and stays adapter-internal.

use std::any::TypeId;
use std::collections::BTreeMap;

use clap::{Arg, ArgAction, Command};

use super::root::Root;

/// The `kit/side-effect` annotation key.
pub const SIDE_EFFECT: &str = "kit/side-effect";

/// What a flag carries.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum FlagKind {
    /// A switch.
    Bool,
    /// A repeatable counter (`-VV`).
    Count,
    /// A string value.
    String,
    /// An integer value.
    Int,
    /// An unsigned integer value.
    Uint,
    /// A floating-point value.
    Float,
    /// A repeatable string value.
    Strings,
}

impl FlagKind {
    /// pflag's type name for the kind, the value-name fallback of help,
    /// completion specs and the missing-value fix.
    pub fn type_name(self) -> &'static str {
        match self {
            FlagKind::Bool => "bool",
            FlagKind::Count => "count",
            FlagKind::String => "string",
            FlagKind::Int => "int",
            FlagKind::Uint => "uint",
            FlagKind::Float => "float",
            FlagKind::Strings => "stringArray",
        }
    }

    /// Whether the flag is given a value.
    pub fn takes_value(self) -> bool {
        !matches!(self, FlagKind::Bool | FlagKind::Count)
    }
}

/// A flag of a command.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct FlagInfo {
    /// The long name.
    pub name: String,
    /// The short name.
    pub short: Option<char>,
    /// The description.
    pub usage: String,
    /// What the flag carries.
    pub kind: FlagKind,
    /// The value name shown in help: an explicit one, else the type name.
    pub value_name: String,
    /// The default value, when there is one.
    pub default: Option<String>,
    /// Whether the flag must be given.
    pub required: bool,
    /// Whether help hides it.
    pub hidden: bool,
    /// Whether it is declared once for the whole tree.
    pub persistent: bool,
    /// The closed set of legal values, when declared.
    pub enum_values: Vec<String>,
}

/// A positional argument of a command.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ArgInfo {
    /// The argument name.
    pub name: String,
    /// The description.
    pub usage: String,
    /// Whether it must be given.
    pub required: bool,
    /// Whether it takes several values.
    pub variadic: bool,
}

/// One command of the tree.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CommandInfo {
    /// The command's name.
    pub name: String,
    /// The path below the root, words in order; empty for the root.
    pub path: Vec<String>,
    /// The one-line description.
    pub short: String,
    /// The long description.
    pub long: String,
    /// Other names the command answers to.
    pub aliases: Vec<String>,
    /// Whether help hides it.
    pub hidden: bool,
    /// The help group id; `None` for the default group.
    pub group: Option<String>,
    /// Whether the command runs (has a handler), as opposed to only
    /// grouping subcommands.
    pub invocable: bool,
    /// The `kit/*` annotations.
    pub annotations: BTreeMap<String, String>,
    /// The flags declared on this command (the persistent ones on the
    /// root).
    pub flags: Vec<FlagInfo>,
    /// The positional arguments.
    pub args: Vec<ArgInfo>,
    /// The subcommands.
    pub commands: Vec<CommandInfo>,
}

impl CommandInfo {
    /// The path as one space-separated string.
    pub fn path_string(&self) -> String {
        self.path.join(" ")
    }

    /// The `kit/side-effect` annotation.
    pub fn side_effect(&self) -> Option<&str> {
        self.annotations.get(SIDE_EFFECT).map(String::as_str)
    }

    /// The descendant at `path` (space-separated, relative to this
    /// command); the empty path is this command.
    pub fn find(&self, path: &str) -> Option<&CommandInfo> {
        path.split_whitespace()
            .try_fold(self, |cur, w| cur.commands.iter().find(|c| c.name == w))
    }

    /// The flag named `name` declared on this command.
    pub fn flag(&self, name: &str) -> Option<&FlagInfo> {
        self.flags.iter().find(|f| f.name == name)
    }

    /// Calls `f` on this command and every descendant, parents first.
    pub fn walk<'a>(&'a self, f: &mut dyn FnMut(&'a CommandInfo)) {
        f(self);
        for c in &self.commands {
            c.walk(f);
        }
    }
}

impl Root {
    /// The command tree as kit's model: every command with its flags,
    /// arguments, annotations and group, in definition order. The root
    /// carries the persistent flags.
    pub fn model(&self) -> CommandInfo {
        let mut built = self.command();
        built.build();
        self.describe(&built, &[], true)
    }

    fn describe(&self, c: &Command, path: &[String], root: bool) -> CommandInfo {
        let path_str = path.join(" ");
        let mut flags = Vec::new();
        let mut args = Vec::new();
        for a in c.get_arguments() {
            if a.is_positional() {
                args.push(ArgInfo {
                    name: a.get_id().to_string(),
                    usage: a.get_help().map(|h| h.to_string()).unwrap_or_default(),
                    required: a.is_required_set(),
                    variadic: a.get_num_args().is_some_and(|n| n.max_values() > 1),
                });
            } else if root || !a.is_global_set() {
                flags.push(self.describe_flag(path, a));
            }
        }
        // `--version` and the help flags are clap/kit plumbing, not
        // adopter flags; the model keeps `help` but not the version flag.
        let commands = c
            .get_subcommands()
            .filter(|s| s.get_name() != "help")
            .map(|s| {
                let mut p = path.to_vec();
                p.push(s.get_name().to_string());
                self.describe(s, &p, false)
            })
            .collect();
        CommandInfo {
            name: c.get_name().to_string(),
            path: path.to_vec(),
            short: c.get_about().map(|s| s.to_string()).unwrap_or_default(),
            long: c
                .get_long_about()
                .map(|s| s.to_string())
                .unwrap_or_default(),
            aliases: c.get_all_aliases().map(str::to_string).collect(),
            hidden: c.is_hide_set(),
            group: self.group_of(path),
            invocable: self.is_invocable(&path_str, c),
            annotations: self.annotations_of(&path_str),
            flags,
            args,
            commands,
        }
    }

    fn describe_flag(&self, path: &[String], a: &Arg) -> FlagInfo {
        let name = super::root::flag_name(a);
        let kind = flag_kind(a);
        let enum_values = self
            .enums
            .get(&(path.join(" "), name.clone()))
            .or_else(|| self.enums.get(&(String::new(), name.clone())))
            .cloned()
            .unwrap_or_default();
        FlagInfo {
            short: a.get_short(),
            usage: a.get_help().map(|h| h.to_string()).unwrap_or_default(),
            kind,
            value_name: value_name_of(a, kind),
            default: a
                .get_default_values()
                .first()
                .map(|v| v.to_string_lossy().into_owned()),
            required: a.is_required_set(),
            hidden: a.is_hide_set(),
            persistent: a.is_global_set(),
            enum_values,
            name,
        }
    }

    fn is_invocable(&self, path: &str, _c: &Command) -> bool {
        self.handlers.contains_key(path)
    }
}

/// The kind of value a flag carries, from its clap action and value
/// parser.
pub(crate) fn flag_kind(a: &Arg) -> FlagKind {
    match a.get_action() {
        ArgAction::SetTrue | ArgAction::SetFalse => FlagKind::Bool,
        ArgAction::Count => FlagKind::Count,
        ArgAction::Append => FlagKind::Strings,
        _ => {
            let t = a.get_value_parser().type_id();
            let is = |id: TypeId| t == id;
            if is(TypeId::of::<i8>())
                || is(TypeId::of::<i16>())
                || is(TypeId::of::<i32>())
                || is(TypeId::of::<i64>())
                || is(TypeId::of::<isize>())
            {
                FlagKind::Int
            } else if is(TypeId::of::<u8>())
                || is(TypeId::of::<u16>())
                || is(TypeId::of::<u32>())
                || is(TypeId::of::<u64>())
                || is(TypeId::of::<usize>())
            {
                FlagKind::Uint
            } else if is(TypeId::of::<f32>()) || is(TypeId::of::<f64>()) {
                FlagKind::Float
            } else if is(TypeId::of::<bool>())
                && a.get_num_args().is_some_and(|n| n.max_values() == 0)
            {
                FlagKind::Bool
            } else {
                FlagKind::String
            }
        }
    }
}

/// An explicit value name, else pflag's type name: what Go prints for
/// a flag that names no value.
pub(crate) fn value_name_of(a: &Arg, kind: FlagKind) -> String {
    match a.get_value_names().and_then(|n| n.first()) {
        Some(n) if !n.is_empty() => n.to_string(),
        _ => kind.type_name().to_string(),
    }
}
