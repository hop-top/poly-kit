//! The clap adapter: turns kit's [`CommandSpec`] into the clap tree the
//! parsing provider runs on. Nothing outside `cli` sees clap types;
//! swapping the provider means replacing this file and the parse seams
//! in `run.rs` and `usage.rs`.

use clap::builder::{PossibleValue, PossibleValuesParser};
use clap::{Arg, ArgAction, Command};

use super::model::FlagKind;
use super::spec::{ArgSpec, CommandSpec, FlagSpec, ValueSpec};

/// The clap command for `spec` and its subtree.
pub(super) fn to_clap(spec: &CommandSpec) -> Command {
    let mut cmd = Command::new(spec.name.clone());
    if !spec.about.is_empty() {
        cmd = cmd.about(spec.about.clone());
    }
    if !spec.long_about.is_empty() {
        cmd = cmd.long_about(spec.long_about.clone());
    }
    for a in &spec.aliases {
        cmd = cmd.alias(a.clone());
    }
    if spec.hidden {
        cmd = cmd.hide(true);
    }
    for a in &spec.args {
        cmd = cmd.arg(arg(a));
    }
    for f in &spec.flags {
        if f.help_flag {
            cmd = cmd.disable_help_flag(true);
        }
        cmd = cmd.arg(flag(f));
    }
    if let Some((v, short)) = &spec.version {
        let mut a = Arg::new("version")
            .long("version")
            .action(ArgAction::Version);
        if let Some(s) = short {
            a = a.short(*s);
        }
        cmd = cmd.version(v.clone()).disable_version_flag(true).arg(a);
    }
    for s in &spec.subcommands {
        cmd = cmd.subcommand(to_clap(s));
    }
    cmd
}

fn flag(f: &FlagSpec) -> Arg {
    let mut a = Arg::new(f.name.clone()).long(f.name.clone());
    if !f.usage.is_empty() {
        a = a.help(f.usage.clone());
    }
    if let Some(s) = f.short {
        a = a.short(s);
    }
    a = match f.kind {
        _ if f.help_flag => a.action(ArgAction::Help),
        FlagKind::Bool => a.action(ArgAction::SetTrue),
        FlagKind::Count => a.action(ArgAction::Count),
        FlagKind::String => a.action(ArgAction::Set),
        FlagKind::Int => a.value_parser(clap::value_parser!(i64)),
        FlagKind::Uint => a.value_parser(clap::value_parser!(u64)),
        FlagKind::Float => a.value_parser(clap::value_parser!(f64)),
        FlagKind::Strings => a.action(ArgAction::Append),
    };
    if let Some(n) = &f.value_name {
        a = a.value_name(n.clone());
    }
    if let Some(d) = &f.default {
        a = a.default_value(d.clone());
    }
    if f.required {
        a = a.required(true);
    }
    if f.hidden {
        a = a.hide(true);
    }
    if f.persistent {
        a = a.global(true);
    }
    if let Some(v) = &f.no_opt_default {
        a = a
            .num_args(0..=1)
            .require_equals(true)
            .default_missing_value(v.clone());
    }
    if !f.allowed.is_empty() {
        a = a.value_parser(PossibleValuesParser::new(possible(&f.allowed)));
    }
    a
}

fn arg(s: &ArgSpec) -> Arg {
    let mut a = Arg::new(s.name.clone());
    if !s.usage.is_empty() {
        a = a.help(s.usage.clone());
    }
    if s.required {
        a = a.required(true);
    }
    if s.variadic {
        a = a.num_args(1..);
    }
    if !s.allowed.is_empty() {
        a = a.value_parser(PossibleValuesParser::new(possible(&s.allowed)));
    }
    a
}

fn possible(vals: &[ValueSpec]) -> Vec<PossibleValue> {
    vals.iter()
        .map(|v| PossibleValue::new(v.value.clone()).hide(v.hidden))
        .collect()
}
