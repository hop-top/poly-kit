//! Kit-owned command description: the builder an adopter defines a CLI
//! with. [`CommandSpec`], [`FlagSpec`] and [`ArgSpec`] carry the
//! vocabulary of Go's cobra tree as kit types (name, about, flags with
//! kinds, positional arguments, groups, annotations, side-effect tier,
//! handler); the parsing provider is chosen behind [`Root`] and never
//! appears in the contract.
//!
//! ```
//! use hop_top_kit::cli::{CommandSpec, FlagSpec, Root};
//!
//! let mut root = Root::new(CommandSpec::new("mytool").about("My tool"));
//! root.add_command(
//!     CommandSpec::new("deploy")
//!         .about("Deploy a target")
//!         .side_effect("write")
//!         .flag(FlagSpec::string("env").short('e').help("Target env"))
//!         .handler(|inv, out, _err| {
//!             writeln!(out, "deploying {:?}", inv.flag("env")).ok();
//!             Ok(())
//!         }),
//! );
//! let mut out = Vec::new();
//! let mut err = Vec::new();
//! assert_eq!(root.run(["mytool", "deploy", "-e", "prod"], &mut out, &mut err), 0);
//! ```

use std::collections::BTreeMap;
use std::io::Write;
use std::sync::Arc;

use crate::output::CliError;

use super::model::{FlagKind, SIDE_EFFECT};
use super::run::{HandlerFn, Invocation};

/// A legal value of a flag or argument.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ValueSpec {
    /// The value.
    pub value: String,
    /// Whether help and completion leave it out while still accepting
    /// it.
    pub hidden: bool,
}

impl ValueSpec {
    /// A visible value.
    pub fn new(value: impl Into<String>) -> ValueSpec {
        ValueSpec {
            value: value.into(),
            hidden: false,
        }
    }

    /// An accepted value that help and completion do not list.
    pub fn hidden(value: impl Into<String>) -> ValueSpec {
        ValueSpec {
            value: value.into(),
            hidden: true,
        }
    }
}

/// A flag of a command, as the author declares it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct FlagSpec {
    pub(crate) name: String,
    pub(crate) short: Option<char>,
    pub(crate) usage: String,
    pub(crate) kind: FlagKind,
    pub(crate) value_name: Option<String>,
    pub(crate) default: Option<String>,
    pub(crate) required: bool,
    pub(crate) hidden: bool,
    pub(crate) persistent: bool,
    pub(crate) no_opt_default: Option<String>,
    pub(crate) allowed: Vec<ValueSpec>,
    pub(crate) help_flag: bool,
}

impl FlagSpec {
    fn of(name: impl Into<String>, kind: FlagKind) -> FlagSpec {
        FlagSpec {
            name: name.into(),
            short: None,
            usage: String::new(),
            kind,
            value_name: None,
            default: None,
            required: false,
            hidden: false,
            persistent: false,
            no_opt_default: None,
            allowed: Vec::new(),
            help_flag: false,
        }
    }

    /// A switch.
    pub fn bool(name: impl Into<String>) -> FlagSpec {
        FlagSpec::of(name, FlagKind::Bool)
    }

    /// A repeatable counter (`-VV`).
    pub fn count(name: impl Into<String>) -> FlagSpec {
        FlagSpec::of(name, FlagKind::Count)
    }

    /// A string-valued flag.
    pub fn string(name: impl Into<String>) -> FlagSpec {
        FlagSpec::of(name, FlagKind::String)
    }

    /// An integer-valued flag.
    pub fn int(name: impl Into<String>) -> FlagSpec {
        FlagSpec::of(name, FlagKind::Int)
    }

    /// An unsigned-integer-valued flag.
    pub fn uint(name: impl Into<String>) -> FlagSpec {
        FlagSpec::of(name, FlagKind::Uint)
    }

    /// A floating-point-valued flag.
    pub fn float(name: impl Into<String>) -> FlagSpec {
        FlagSpec::of(name, FlagKind::Float)
    }

    /// A repeatable string-valued flag.
    pub fn strings(name: impl Into<String>) -> FlagSpec {
        FlagSpec::of(name, FlagKind::Strings)
    }

    /// The command's own help flag, in place of the default one: a flag
    /// of the given name whose use prints help.
    pub fn help_flag(name: impl Into<String>) -> FlagSpec {
        let mut f = FlagSpec::of(name, FlagKind::Bool);
        f.help_flag = true;
        f
    }

    /// The one-character short name.
    pub fn short(mut self, c: char) -> FlagSpec {
        self.short = Some(c);
        self
    }

    /// The description shown in help.
    pub fn help(mut self, usage: impl Into<String>) -> FlagSpec {
        self.usage = usage.into();
        self
    }

    /// The value name help prints; absent, the kind's type name.
    pub fn value_name(mut self, name: impl Into<String>) -> FlagSpec {
        self.value_name = Some(name.into());
        self
    }

    /// The value the flag has when not given.
    pub fn default_value(mut self, v: impl Into<String>) -> FlagSpec {
        self.default = Some(v.into());
        self
    }

    /// Makes the flag mandatory.
    pub fn required(mut self) -> FlagSpec {
        self.required = true;
        self
    }

    /// Keeps the flag out of default help.
    pub fn hidden(mut self) -> FlagSpec {
        self.hidden = true;
        self
    }

    /// Declares the flag once for the command and everything below it.
    pub fn persistent(mut self) -> FlagSpec {
        self.persistent = true;
        self
    }

    /// Lets the flag be given without a value, as `--name`; the value
    /// is then `v` (pflag's `NoOptDefVal`). A value is given as
    /// `--name=value` only.
    pub fn no_opt_default(mut self, v: impl Into<String>) -> FlagSpec {
        self.no_opt_default = Some(v.into());
        self
    }

    /// Restricts the flag to a closed set of values; any other value
    /// is a parse failure.
    pub fn allowed_values(mut self, values: impl IntoIterator<Item = ValueSpec>) -> FlagSpec {
        self.allowed = values.into_iter().collect();
        self
    }

    /// The long name.
    pub fn name(&self) -> &str {
        &self.name
    }
}

/// A positional argument of a command.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ArgSpec {
    pub(crate) name: String,
    pub(crate) usage: String,
    pub(crate) required: bool,
    pub(crate) variadic: bool,
    pub(crate) allowed: Vec<ValueSpec>,
}

impl ArgSpec {
    /// An optional single-valued argument.
    pub fn new(name: impl Into<String>) -> ArgSpec {
        ArgSpec {
            name: name.into(),
            usage: String::new(),
            required: false,
            variadic: false,
            allowed: Vec::new(),
        }
    }

    /// The description shown in help.
    pub fn help(mut self, usage: impl Into<String>) -> ArgSpec {
        self.usage = usage.into();
        self
    }

    /// Makes the argument mandatory.
    pub fn required(mut self) -> ArgSpec {
        self.required = true;
        self
    }

    /// Lets the argument take several values.
    pub fn variadic(mut self) -> ArgSpec {
        self.variadic = true;
        self
    }

    /// Restricts the argument to a closed set of values.
    pub fn allowed_values(mut self, values: impl IntoIterator<Item = ValueSpec>) -> ArgSpec {
        self.allowed = values.into_iter().collect();
        self
    }
}

/// A command and its subtree, as the author declares it.
#[derive(Clone)]
pub struct CommandSpec {
    pub(crate) name: String,
    pub(crate) about: String,
    pub(crate) long_about: String,
    pub(crate) aliases: Vec<String>,
    pub(crate) hidden: bool,
    pub(crate) group: Option<String>,
    pub(crate) flags: Vec<FlagSpec>,
    pub(crate) args: Vec<ArgSpec>,
    pub(crate) subcommands: Vec<CommandSpec>,
    pub(crate) annotations: BTreeMap<String, String>,
    pub(crate) handler: Option<Arc<HandlerFn>>,
    pub(crate) version: Option<(String, Option<char>)>,
}

impl std::fmt::Debug for CommandSpec {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("CommandSpec")
            .field("name", &self.name)
            .field("subcommands", &self.subcommands)
            .field("handler", &self.handler.is_some())
            .finish_non_exhaustive()
    }
}

impl CommandSpec {
    /// A command named `name`.
    pub fn new(name: impl Into<String>) -> CommandSpec {
        CommandSpec {
            name: name.into(),
            about: String::new(),
            long_about: String::new(),
            aliases: Vec::new(),
            hidden: false,
            group: None,
            flags: Vec::new(),
            args: Vec::new(),
            subcommands: Vec::new(),
            annotations: BTreeMap::new(),
            handler: None,
            version: None,
        }
    }

    /// The one-line description.
    pub fn about(mut self, s: impl Into<String>) -> CommandSpec {
        self.about = s.into();
        self
    }

    /// The long description.
    pub fn long_about(mut self, s: impl Into<String>) -> CommandSpec {
        self.long_about = s.into();
        self
    }

    /// Another name the command answers to.
    pub fn alias(mut self, name: impl Into<String>) -> CommandSpec {
        self.aliases.push(name.into());
        self
    }

    /// Keeps the command out of default help.
    pub fn hidden(mut self) -> CommandSpec {
        self.hidden = true;
        self
    }

    /// Puts the command in help group `id` (root subcommands only).
    pub fn group(mut self, id: impl Into<String>) -> CommandSpec {
        self.group = Some(id.into());
        self
    }

    /// Declares a flag.
    pub fn flag(mut self, f: FlagSpec) -> CommandSpec {
        self.flags.push(f);
        self
    }

    /// Declares a positional argument.
    pub fn arg(mut self, a: ArgSpec) -> CommandSpec {
        self.args.push(a);
        self
    }

    /// Adds a subcommand.
    pub fn subcommand(mut self, c: CommandSpec) -> CommandSpec {
        self.subcommands.push(c);
        self
    }

    /// Sets a `kit/*` annotation.
    pub fn annotate(mut self, key: impl Into<String>, value: impl Into<String>) -> CommandSpec {
        self.annotations.insert(key.into(), value.into());
        self
    }

    /// Declares the `kit/side-effect` tier (`read`, `write`,
    /// `destructive`, ...).
    pub fn side_effect(self, tier: &str) -> CommandSpec {
        self.annotate(SIDE_EFFECT, tier)
    }

    /// The code that runs the command: a command with a handler is
    /// invocable, one without only groups its subcommands.
    pub fn handler<F>(mut self, f: F) -> CommandSpec
    where
        F: Fn(&Invocation, &mut dyn Write, &mut dyn Write) -> Result<(), CliError>
            + Send
            + Sync
            + 'static,
    {
        self.handler = Some(Arc::new(f));
        self
    }

    /// The version `--version` prints on this command (the root, in
    /// practice), with an optional short name for the flag.
    pub fn version(mut self, v: impl Into<String>, short: Option<char>) -> CommandSpec {
        self.version = Some((v.into(), short));
        self
    }

    /// The command's name.
    pub fn name(&self) -> &str {
        &self.name
    }
}
