//! The `completion <shell>` scripts.
//!
//! The templates are cobra v1.10.2's bash (V2), zsh, fish and
//! powershell scripts with the program-dependent parts replaced by
//! `@@…@@` tokens, so a kit-rs program writes byte for byte what a Go
//! kit program of the same name writes (pinned by
//! `contracts/completion-v1/scripts`). cobra is Apache-2.0; its licence
//! ships next to the templates as `LICENSE-cobra.txt`.

use std::fmt;
use std::io::{self, Write};
use std::str::FromStr;

use super::{NO_DESC_REQUEST_CMD, REQUEST_CMD};

const BASH: &str = include_str!("scripts/bash.tmpl");
const ZSH: &str = include_str!("scripts/zsh.tmpl");
const FISH: &str = include_str!("scripts/fish.tmpl");
const POWERSHELL: &str = include_str!("scripts/powershell.tmpl");

/// A shell `completion` can write a script for.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Shell {
    /// bash (cobra's V2 script; needs the bash-completion package).
    Bash,
    /// zsh.
    Zsh,
    /// fish.
    Fish,
    /// PowerShell.
    PowerShell,
}

impl Shell {
    /// Every shell, in the order the `completion` command lists them.
    pub const ALL: [Shell; 4] = [Shell::Bash, Shell::Zsh, Shell::Fish, Shell::PowerShell];

    /// The subcommand name: `bash`, `zsh`, `fish` or `powershell`.
    pub fn name(self) -> &'static str {
        match self {
            Shell::Bash => "bash",
            Shell::Zsh => "zsh",
            Shell::Fish => "fish",
            Shell::PowerShell => "powershell",
        }
    }

    fn template(self) -> &'static str {
        match self {
            Shell::Bash => BASH,
            Shell::Zsh => ZSH,
            Shell::Fish => FISH,
            Shell::PowerShell => POWERSHELL,
        }
    }
}

impl fmt::Display for Shell {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.name())
    }
}

impl FromStr for Shell {
    type Err = String;
    fn from_str(s: &str) -> Result<Shell, String> {
        Shell::ALL
            .into_iter()
            .find(|sh| sh.name() == s)
            .ok_or_else(|| format!("unsupported shell {s:?}"))
    }
}

/// Writes the completion script for program `name`.
pub(crate) fn write_script(
    shell: Shell,
    name: &str,
    include_desc: bool,
    out: &mut dyn Write,
) -> io::Result<()> {
    out.write_all(render(shell, name, include_desc).as_bytes())
}

fn render(shell: Shell, name: &str, include_desc: bool) -> String {
    let comp_cmd = if include_desc {
        REQUEST_CMD
    } else {
        NO_DESC_REQUEST_CMD
    };
    // Shell identifiers cannot hold '-' or ':'.
    let name_var = name.replace(['-', ':'], "_");
    shell
        .template()
        .replace("@@NAME_PAD36@@", &format!("{name:<36}"))
        .replace("@@NAME_VAR@@", &name_var)
        .replace("@@ACTIVE_HELP_VAR@@", &config_env_var(name, "ACTIVE_HELP"))
        .replace("@@COMP_CMD@@", comp_cmd)
        .replace("@@NAME@@", name)
}

/// cobra's `<PROGRAM>_<SUFFIX>` variable name: upper-cased, every
/// character outside `[A-Z0-9_]` replaced by `_`.
pub(crate) fn config_env_var(name: &str, suffix: &str) -> String {
    format!("{name}_{suffix}")
        .to_uppercase()
        .chars()
        .map(|c| {
            if c.is_ascii_uppercase() || c.is_ascii_digit() || c == '_' {
                c
            } else {
                '_'
            }
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn templates_keep_no_unknown_tokens() {
        for sh in Shell::ALL {
            let out = render(sh, "kit-app", true);
            assert!(!out.contains("@@"), "{sh}: token left in the script");
        }
    }

    #[test]
    fn env_var_names() {
        assert_eq!(
            config_env_var("kit-app.v2", "ACTIVE_HELP"),
            "KIT_APP_V2_ACTIVE_HELP"
        );
        assert_eq!(
            config_env_var("tool", "COMPLETION_DESCRIPTIONS"),
            "TOOL_COMPLETION_DESCRIPTIONS"
        );
    }

    #[test]
    fn shell_names_round_trip() {
        for sh in Shell::ALL {
            assert_eq!(sh.name().parse::<Shell>().unwrap(), sh);
        }
        assert!("tcsh".parse::<Shell>().is_err());
    }
}
