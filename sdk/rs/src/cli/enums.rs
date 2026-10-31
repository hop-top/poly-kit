//! The flag value-enum registry. Ports the completion and help consumers
//! of Go `console/cli/flag_enum.go`.

use std::collections::HashMap;

use clap::{Arg, Command};

use super::completion::Directive;
use super::root::{flag_name, normalize, Root};

impl Root {
    /// Declares the closed value set of every flag named `name` in the
    /// tree: completion offers the values and help gains
    /// `(one of: …)`. Last registration wins; no values clears it.
    pub fn with_flag_enum<S: Into<String>>(
        &mut self,
        name: &str,
        values: impl IntoIterator<Item = S>,
    ) -> &mut Root {
        self.set_enum(String::new(), name, values)
    }

    /// [`Root::with_flag_enum`] for one command's flag; wins over a
    /// tree-wide declaration of the same name.
    pub fn with_command_flag_enum<S: Into<String>>(
        &mut self,
        path: &str,
        name: &str,
        values: impl IntoIterator<Item = S>,
    ) -> &mut Root {
        self.set_enum(normalize(path), name, values)
    }

    fn set_enum<S: Into<String>>(
        &mut self,
        path: String,
        name: &str,
        values: impl IntoIterator<Item = S>,
    ) -> &mut Root {
        if name.is_empty() {
            return self;
        }
        let values: Vec<String> = values.into_iter().map(Into::into).collect();
        let key = (path, name.to_string());
        if values.is_empty() {
            self.enums.remove(&key);
        } else {
            self.enums.insert(key, values);
        }
        self
    }

    /// The tree-wide value set declared for `name`.
    pub fn flag_enum(&self, name: &str) -> Option<&[String]> {
        self.enums
            .get(&(String::new(), name.to_string()))
            .map(Vec::as_slice)
    }

    /// A flag's enum values filtered by `to_complete`: a command-scoped
    /// declaration, else a tree-wide one, else clap's possible values.
    pub(crate) fn enum_completion(
        &self,
        words: &[String],
        arg: &Arg,
        to_complete: &str,
    ) -> Option<(Vec<String>, Directive)> {
        let name = flag_name(arg);
        let values = self
            .enums
            .get(&(words.join(" "), name.clone()))
            .or_else(|| self.enums.get(&(String::new(), name)))
            .cloned()
            .unwrap_or_else(|| {
                arg.get_possible_values()
                    .iter()
                    .filter(|p| !p.is_hide_set())
                    .map(|p| p.get_name().to_string())
                    .collect()
            });
        if values.is_empty() {
            return None;
        }
        let lower = to_complete.to_lowercase();
        let out = values
            .into_iter()
            .filter(|v| v.to_lowercase().starts_with(&lower))
            .collect();
        Some((out, Directive::NO_FILE_COMP))
    }
}

/// Appends `(one of: …)` to the help of every flag with declared enum
/// values, from the unsuffixed help each time so re-declaring replaces.
pub(super) fn apply_enum_help(
    mut cmd: Command,
    path: &str,
    enums: &HashMap<(String, String), Vec<String>>,
) -> Command {
    if enums.is_empty() {
        return cmd;
    }
    let edits: Vec<(String, String)> = cmd
        .get_arguments()
        .filter(|a| !a.is_positional())
        .filter_map(|a| {
            let name = flag_name(a);
            let values = enums
                .get(&(path.to_string(), name.clone()))
                .or_else(|| enums.get(&(String::new(), name)))?;
            let suffix = format!("(one of: {})", values.join(", "));
            let help = match a.get_help().map(|h| h.ansi().to_string()) {
                Some(h) if !h.is_empty() => format!("{h} {suffix}"),
                _ => suffix,
            };
            Some((a.get_id().as_str().to_string(), help))
        })
        .collect();
    for (id, help) in edits {
        cmd = cmd.mut_arg(id, |a| a.help(help));
    }
    let subs: Vec<String> = cmd
        .get_subcommands()
        .map(|c| c.get_name().to_string())
        .collect();
    for s in subs {
        let p = if path.is_empty() {
            s.clone()
        } else {
            format!("{path} {s}")
        };
        cmd = cmd.mut_subcommand(&s, |c| apply_enum_help(c, &p, enums));
    }
    cmd
}
