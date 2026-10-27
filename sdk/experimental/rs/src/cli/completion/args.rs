//! Command-line scanning for the `__complete` engine: cobra's `Find`
//! (which command the words name) and pflag's parse of the final
//! command's flags, with their error texts.

use clap::{Arg, Command};

/// cobra `Find`: descend through subcommand names (and aliases) among
/// the non-flag words, dropping each from the arguments.
pub(super) fn find<'a>(
    root: &'a Command,
    args: &[String],
) -> (Vec<&'a Command>, Vec<String>, Vec<String>) {
    let mut chain = vec![root];
    let mut words = Vec::new();
    let mut rest = args.to_vec();
    loop {
        let cur = *chain.last().expect("non-empty");
        let Some(next) = strip_flags(&rest, &chain).into_iter().next() else {
            break;
        };
        let Some(sub) = cur
            .get_subcommands()
            .find(|s| s.get_name() == next || s.get_all_aliases().any(|a| a == next))
        else {
            break;
        };
        rest = args_minus_first(&rest, &next, &chain);
        words.push(sub.get_name().to_string());
        chain.push(sub);
    }
    (chain, words, rest)
}

fn takes_value_long(chain: &[&Command], name: &str) -> bool {
    lookup_long(chain, name).is_none_or(|a| a.get_action().takes_values())
}

fn takes_value_short(chain: &[&Command], s: &str) -> bool {
    s.chars()
        .next()
        .and_then(|c| lookup_short(chain, c))
        .is_none_or(|a| a.get_action().takes_values())
}

/// cobra `stripFlags`: the non-flag words, skipping the value of a
/// value-taking (or unknown) flag.
fn strip_flags(args: &[String], chain: &[&Command]) -> Vec<String> {
    let mut commands = Vec::new();
    let mut i = 0;
    while i < args.len() {
        let s = &args[i];
        i += 1;
        if s == "--" {
            break;
        }
        let long_val = s.starts_with("--") && !s.contains('=') && takes_value_long(chain, &s[2..]);
        let short_val = !long_val
            && s.starts_with('-')
            && !s.contains('=')
            && s.len() == 2
            && takes_value_short(chain, &s[1..]);
        if long_val || short_val {
            if args.len() - i <= 1 {
                break;
            }
            i += 1;
            continue;
        }
        if !s.is_empty() && !s.starts_with('-') {
            commands.push(s.clone());
        }
    }
    commands
}

/// cobra `argsMinusFirstX`: `args` without the first non-flag `x`.
fn args_minus_first(args: &[String], x: &str, chain: &[&Command]) -> Vec<String> {
    let mut pos = 0;
    while pos < args.len() {
        let s = &args[pos];
        if s == "--" {
            break;
        }
        let long_val = s.starts_with("--") && !s.contains('=') && takes_value_long(chain, &s[2..]);
        let short_val = s.starts_with('-')
            && !s.contains('=')
            && s.len() == 2
            && takes_value_short(chain, &s[1..]);
        if long_val || short_val {
            pos += 2;
            continue;
        }
        if !s.starts_with('-') && s == x {
            let mut out = args.to_vec();
            out.remove(pos);
            return out;
        }
        pos += 1;
    }
    args.to_vec()
}

/// pflag `isFlagArg`.
pub(super) fn is_flag_arg(a: &str) -> bool {
    let b = a.as_bytes();
    (b.len() >= 3 && &b[..2] == b"--") || (b.len() >= 2 && b[0] == b'-' && b[1] != b'-')
}

pub(super) struct Parsed<'a> {
    pub(super) seen: Vec<&'a Arg>,
    pub(super) positionals: Vec<String>,
}

/// pflag's parse of the final command's arguments, with its error texts.
pub(super) fn parse_flags<'a>(
    chain: &[&'a Command],
    args: &[String],
) -> Result<Parsed<'a>, String> {
    let mut p = Parsed {
        seen: Vec::new(),
        positionals: Vec::new(),
    };
    let mut i = 0;
    let mut dashdash = false;
    while i < args.len() {
        let s = &args[i];
        i += 1;
        if dashdash || s.len() < 2 || !s.starts_with('-') {
            p.positionals.push(s.clone());
            continue;
        }
        if s == "--" {
            dashdash = true;
            continue;
        }
        if let Some(body) = s.strip_prefix("--") {
            if body.starts_with(['-', '=']) {
                return Err(format!("bad flag syntax: {s}"));
            }
            let (name, has_value) = match body.split_once('=') {
                Some((n, _)) => (n, true),
                None => (body, false),
            };
            let arg = lookup_long(chain, name).ok_or_else(|| format!("unknown flag: --{name}"))?;
            if !has_value && arg.get_action().takes_values() {
                if i >= args.len() {
                    return Err(format!("flag needs an argument: {s}"));
                }
                i += 1;
            }
            p.seen.push(arg);
            continue;
        }
        let shorthands = &s[1..];
        let mut rest = shorthands;
        while let Some(c) = rest.chars().next() {
            rest = &rest[c.len_utf8()..];
            let arg = lookup_short(chain, c)
                .ok_or_else(|| format!("unknown shorthand flag: '{c}' in -{shorthands}"))?;
            p.seen.push(arg);
            if rest.starts_with('=') || !arg.get_action().takes_values() {
                if rest.starts_with('=') {
                    break;
                }
                continue;
            }
            if rest.is_empty() {
                if i >= args.len() {
                    return Err(format!("flag needs an argument: '{c}' in -{shorthands}"));
                }
                i += 1;
            }
            break;
        }
    }
    Ok(p)
}

pub(super) fn final_cmd<'a>(chain: &[&'a Command]) -> &'a Command {
    chain.last().expect("non-empty")
}

pub(super) fn lookup_long<'a>(chain: &[&'a Command], name: &str) -> Option<&'a Arg> {
    final_cmd(chain).get_arguments().find(|a| {
        !a.is_positional()
            && (a.get_long() == Some(name)
                || a.get_all_aliases().is_some_and(|al| al.contains(&name)))
    })
}

pub(super) fn lookup_short<'a>(chain: &[&'a Command], c: char) -> Option<&'a Arg> {
    final_cmd(chain).get_arguments().find(|a| {
        !a.is_positional()
            && (a.get_short() == Some(c)
                || a.get_all_short_aliases().is_some_and(|al| al.contains(&c)))
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use clap::ArgAction;

    fn s(v: &[&str]) -> Vec<String> {
        v.iter().map(|x| x.to_string()).collect()
    }

    fn tree() -> Command {
        let mut c = Command::new("p")
            .arg(
                Arg::new("v")
                    .long("v")
                    .short('v')
                    .global(true)
                    .action(ArgAction::SetTrue),
            )
            .arg(Arg::new("out").long("out").short('o'))
            .subcommand(Command::new("a").subcommand(Command::new("b")));
        c.build();
        c
    }

    #[test]
    fn strip_flags_skips_flag_values() {
        let c = tree();
        let chain = [&c];
        assert_eq!(strip_flags(&s(&["--out", "x", "a"]), &chain), s(&["a"]));
        assert_eq!(strip_flags(&s(&["-v", "a"]), &chain), s(&["a"]));
        assert_eq!(strip_flags(&s(&["--", "a"]), &chain), s(&[]));
        // Unknown flags are taken to have a value.
        assert_eq!(strip_flags(&s(&["--zz", "a", "b"]), &chain), s(&["b"]));
    }

    #[test]
    fn find_descends_through_flags() {
        let c = tree();
        let (chain, words, rest) = find(&c, &s(&["-v", "a", "b", "x"]));
        assert_eq!(chain.len(), 3);
        assert_eq!(words, s(&["a", "b"]));
        assert_eq!(rest, s(&["-v", "x"]));
        // As in cobra, a flag the subcommand does not know is taken to
        // have a value, so the next word is not a subcommand.
        let (chain, _, _) = find(&c, &s(&["--out", "o", "a", "--zz", "b"]));
        assert_eq!(chain.len(), 2);
    }

    #[test]
    fn parse_flags_error_texts() {
        let c = tree();
        let chain = [&c];
        let e = |a: &[&str]| parse_flags(&chain, &s(a)).err().unwrap();
        assert_eq!(e(&["--nope"]), "unknown flag: --nope");
        assert_eq!(e(&["-vx"]), "unknown shorthand flag: 'x' in -vx");
        assert_eq!(e(&["--out"]), "flag needs an argument: --out");
        assert_eq!(e(&["-o"]), "flag needs an argument: 'o' in -o");
        assert_eq!(e(&["---x"]), "bad flag syntax: ---x");
        let p = parse_flags(&chain, &s(&["-vo", "x", "y", "--", "-z"])).unwrap();
        assert_eq!(p.seen.len(), 2);
        assert_eq!(p.positionals, s(&["y", "-z"]));
    }
}
