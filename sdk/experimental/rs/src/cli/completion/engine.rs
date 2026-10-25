//! The hidden `__complete` entry point. Ports cobra's `getCompletions`
//! and the request command's output over a built clap tree: find the
//! command, decide between flag-value, flag-name and argument
//! completion, then print one completion per line and `:<directive>`.

use std::fs::OpenOptions;
use std::io::{self, Write};

use clap::{Arg, ArgAction, Command};

use super::args::{final_cmd, find, is_flag_arg, lookup_long, lookup_short, parse_flags};
use super::scripts::config_env_var;
use super::{Context, Directive};
use crate::cli::root::{flag_name, Root};

/// The words after `__complete`, answered on `out` and `err`.
pub(crate) fn complete_request(
    root: &Root,
    args: &[String],
    no_desc_cmd: bool,
    out: &mut dyn Write,
    err: &mut dyn Write,
) -> io::Result<()> {
    let mut built = root.command();
    built.build();
    let (completions, directive, error) = get_completions(root, &built, args);
    if let Some(e) = error {
        comp_error(err, &e)?;
    }
    let mut no_desc = no_desc_cmd;
    if !no_desc {
        if let Some(b) =
            env_config(built.get_name(), "COMPLETION_DESCRIPTIONS").and_then(parse_bool)
        {
            no_desc = !b;
        }
    }
    for comp in completions {
        let mut c = comp.as_str();
        if no_desc {
            c = c.split('\t').next().unwrap_or_default();
        }
        // First line only: a description with a line break would read
        // as further completions.
        c = c.split('\n').next().unwrap_or_default();
        writeln!(out, "{}", c.trim())?;
    }
    writeln!(out, ":{}", directive.bits())?;
    writeln!(err, "Completion ended with directive: {directive}")
}

type Outcome = (Vec<String>, Directive, Option<String>);

fn get_completions(root: &Root, built: &Command, args: &[String]) -> Outcome {
    let (to_complete, trimmed) = match args.split_last() {
        Some((last, rest)) => (last.clone(), rest),
        None => (String::new(), &[][..]),
    };
    let (chain, words, final_args) = find(built, trimmed);
    let cmd = *chain.last().expect("chain holds the root");
    let path = words.join(" ");

    let fc = check_flag_completion(&chain, final_args, to_complete);
    let (flag, final_args, to_complete) = (fc.flag, fc.args, fc.to_complete);

    // A `--` already on the line turns later dash words into arguments.
    let flag_completion = !final_args.iter().any(|a| a == "--");
    let parsed = match parse_flags(&chain, &final_args) {
        Ok(p) => p,
        Err(e) => {
            return (
                Vec::new(),
                Directive::DEFAULT,
                Some(format!(
                    "Error while parsing flags from args [{}]: {e}",
                    final_args.join(" ")
                )),
            )
        }
    };
    if let Some(e) = fc.error {
        if flag_completion {
            return (Vec::new(), Directive::DEFAULT, Some(e));
        }
    }
    if parsed.seen.iter().any(|a| {
        matches!(
            a.get_action(),
            ArgAction::Help | ArgAction::HelpShort | ArgAction::HelpLong | ArgAction::Version
        )
    }) {
        return (Vec::new(), Directive::NO_FILE_COMP, None);
    }
    let final_args = parsed.positionals;
    let ctx = Context::new(path, final_args.clone());

    let mut completions = Vec::new();
    let mut directive;
    if flag.is_none()
        && to_complete.starts_with('-')
        && !to_complete.contains('=')
        && flag_completion
    {
        completions = required_flags(&chain, &parsed.seen, &to_complete);
        if completions.is_empty() {
            for a in ordered_flags(&chain) {
                let multi = matches!(a.get_action(), ArgAction::Append | ArgAction::Count);
                if !is_seen(&parsed.seen, a) || multi {
                    completions.extend(flag_name_completions(&chain, a, &to_complete));
                }
            }
        }
        directive = Directive::NO_FILE_COMP;
        if completions.len() == 1 && completions[0].ends_with('=') {
            directive = Directive::NO_SPACE;
        }
        return (completions, directive, None);
    }

    directive = Directive::DEFAULT;
    if flag.is_none() {
        let local_given = parsed
            .seen
            .iter()
            .any(|a| !a.is_global_set() && !is_inherited(&chain, a));
        if final_args.is_empty() && !local_given {
            let mut subs: Vec<&Command> =
                cmd.get_subcommands().filter(|s| !s.is_hide_set()).collect();
            subs.sort_by(|a, b| a.get_name().cmp(b.get_name()));
            for s in subs {
                if s.get_name().starts_with(to_complete.as_str()) {
                    let about = s
                        .get_about()
                        .map(|a| a.ansi().to_string())
                        .unwrap_or_default();
                    completions.push(format!("{}\t{about}", s.get_name()));
                }
                directive = Directive::NO_FILE_COMP;
            }
        }
        completions.extend(required_flags(&chain, &parsed.seen, &to_complete));

        // clap's possible values on the next positional play the part
        // of cobra's ValidArgs: they win over a bound completer.
        if let Some(pos) = cmd.get_positionals().nth(final_args.len()) {
            let values: Vec<String> = pos
                .get_possible_values()
                .iter()
                .filter(|p| !p.is_hide_set())
                .map(|p| p.get_name().to_string())
                .collect();
            if !values.is_empty() {
                completions.extend(
                    values
                        .into_iter()
                        .filter(|v| v.starts_with(to_complete.as_str())),
                );
                return (completions, Directive::NO_FILE_COMP, None);
            }
        }
    }

    let result = match flag {
        Some(a) if flag_completion => root.run_flag_completion(&words, a, &ctx, &to_complete),
        _ => root.args_completion(&ctx.command_path, &final_args, &to_complete),
    };
    if let Some((comps, d)) = result {
        completions.extend(comps);
        directive = d;
    }
    (completions, directive, None)
}

struct FlagCheck<'a> {
    flag: Option<&'a Arg>,
    args: Vec<String>,
    to_complete: String,
    error: Option<String>,
}

/// cobra `checkIfFlagCompletion`: is the word being completed the value
/// of a flag (`--flag <TAB>`, `--flag=<TAB>`, `-f <TAB>`)?
fn check_flag_completion<'a>(
    chain: &[&'a Command],
    args: Vec<String>,
    last: String,
) -> FlagCheck<'a> {
    let none = |args, to_complete| FlagCheck {
        flag: None,
        args,
        to_complete,
        error: None,
    };
    let mut name = String::new();
    let mut trimmed = args.clone();
    let mut with_equal = false;
    let mut value = last.clone();
    if last.starts_with('-') {
        let Some(idx) = last.find('=') else {
            return none(args, last);
        };
        name = if last.starts_with("--") {
            last[2..idx].to_string()
        } else {
            // The last shorthand of a cluster: `-asd=` completes `d`.
            last[..idx]
                .chars()
                .last()
                .map(String::from)
                .unwrap_or_default()
        };
        value = last[idx + 1..].to_string();
        with_equal = true;
    }
    if name.is_empty() {
        if let Some(prev) = args.last() {
            if is_flag_arg(prev) && !prev.contains('=') {
                name = match prev.strip_prefix("--") {
                    Some(long) => long.to_string(),
                    None => prev.chars().last().map(String::from).unwrap_or_default(),
                };
                trimmed.pop();
            }
        }
    }
    if name.is_empty() {
        return none(trimmed, value);
    }
    // cobra `findFlag`: a one-letter name is a shorthand first.
    let short = match name.chars().count() {
        1 => name.chars().next().and_then(|c| lookup_short(chain, c)),
        _ => None,
    };
    let found = short.or_else(|| lookup_long(chain, &name));
    let Some(flag) = found else {
        let cmd = chain.last().expect("non-empty").get_name();
        return FlagCheck {
            flag: None,
            args,
            to_complete: last,
            error: Some(format!("Subcommand '{cmd}' does not support flag '{name}'")),
        };
    };
    if !with_equal && !flag.get_action().takes_values() {
        // A boolean flag takes no value: complete the word as an argument.
        return none(args, value);
    }
    FlagCheck {
        flag: Some(flag),
        args: trimmed,
        to_complete: value,
        error: None,
    }
}

fn is_seen(seen: &[&Arg], a: &Arg) -> bool {
    seen.iter().any(|s| s.get_id() == a.get_id())
}

/// A global flag the final command got from an ancestor.
fn is_inherited(chain: &[&Command], a: &Arg) -> bool {
    a.is_global_set()
        && chain.len() > 1
        && chain[chain.len() - 2]
            .get_arguments()
            .any(|p| p.get_id() == a.get_id() && p.is_global_set())
}

/// The final command's flags as pflag visits them: inherited first,
/// then its own, each sorted by name.
fn ordered_flags<'a>(chain: &[&'a Command]) -> Vec<&'a Arg> {
    let (mut inherited, mut own): (Vec<&Arg>, Vec<&Arg>) = final_cmd(chain)
        .get_arguments()
        .filter(|a| !a.is_positional())
        .partition(|a| is_inherited(chain, a));
    inherited.sort_by_key(|a| flag_name(a));
    own.sort_by_key(|a| flag_name(a));
    inherited.into_iter().chain(own).collect()
}

fn required_flags(chain: &[&Command], seen: &[&Arg], to_complete: &str) -> Vec<String> {
    ordered_flags(chain)
        .into_iter()
        .filter(|a| a.is_required_set() && !is_seen(seen, a))
        .flat_map(|a| flag_name_completions(chain, a, to_complete))
        .collect()
}

/// `--long<TAB>usage` and `-s<TAB>usage` when they match; nothing for a
/// hidden flag.
fn flag_name_completions(chain: &[&Command], a: &Arg, to_complete: &str) -> Vec<String> {
    if a.is_hide_set() {
        return Vec::new();
    }
    let usage = match a.get_action() {
        ArgAction::Help | ArgAction::HelpShort | ArgAction::HelpLong => {
            format!("help for {}", final_cmd(chain).get_name())
        }
        ArgAction::Version => format!("version for {}", chain[0].get_name()),
        _ => a
            .get_help()
            .map(|h| h.ansi().to_string())
            .unwrap_or_default(),
    };
    let mut out = Vec::new();
    if let Some(l) = a.get_long() {
        let name = format!("--{l}");
        if name.starts_with(to_complete) {
            out.push(format!("{name}\t{usage}"));
        }
    }
    if let Some(s) = a.get_short() {
        let name = format!("-{s}");
        if name.starts_with(to_complete) {
            out.push(format!("{name}\t{usage}"));
        }
    }
    out
}

/// cobra `getEnvConfig`: `<PROGRAM>_<SUFFIX>`, else `COBRA_<SUFFIX>`.
fn env_config(prog: &str, suffix: &str) -> Option<String> {
    [
        config_env_var(prog, suffix),
        config_env_var("COBRA", suffix),
    ]
    .into_iter()
    .filter_map(|k| std::env::var(k).ok())
    .find(|v| !v.is_empty())
}

/// Go `strconv.ParseBool`.
fn parse_bool(v: String) -> Option<bool> {
    match v.as_str() {
        "1" | "t" | "T" | "TRUE" | "true" | "True" => Some(true),
        "0" | "f" | "F" | "FALSE" | "false" | "False" => Some(false),
        _ => None,
    }
}

/// cobra `CompErrorln`: stderr, plus `$BASH_COMP_DEBUG_FILE` when set.
fn comp_error(err: &mut dyn Write, msg: &str) -> io::Result<()> {
    let line = format!("[Debug] [Error] {msg}\n");
    if let Some(path) = std::env::var_os("BASH_COMP_DEBUG_FILE").filter(|p| !p.is_empty()) {
        if let Ok(mut f) = OpenOptions::new().append(true).create(true).open(path) {
            let _ = f.write_all(line.as_bytes());
        }
    }
    err.write_all(line.as_bytes())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn go_parse_bool() {
        assert_eq!(parse_bool("T".into()), Some(true));
        assert_eq!(parse_bool("False".into()), Some(false));
        assert_eq!(parse_bool("yes".into()), None);
    }
}
