//! cobra's default `completion` command: `completion bash|zsh|fish|powershell`
//! with `--no-descriptions`, help texts included, plus kit's
//! `completion fig|usage` spec exports.

use clap::{Arg, ArgAction, Command};

use super::Shell;

/// Name of the completion command.
pub(crate) const COMPLETION: &str = "completion";
/// The flag that drops descriptions from the generated script.
pub(crate) const NO_DESC_FLAG: &str = "no-descriptions";

/// cobra's default `completion` command, texts included, with the
/// spec exports.
pub(crate) fn completion_command(prog: &str) -> Command {
    let mut cmd = Command::new(COMPLETION)
        .about("Generate the autocompletion script for the specified shell")
        .long_about(format!(
            "Generate the autocompletion script for {prog} for the specified shell.\n\
             See each sub-command's help for details on how to use the generated script.\n"
        ))
        .hide(true);
    for sh in Shell::ALL {
        cmd = cmd.subcommand(
            Command::new(sh.name())
                .about(format!("Generate the autocompletion script for {sh}"))
                .long_about(shell_long_help(sh, prog))
                .arg(
                    Arg::new(NO_DESC_FLAG)
                        .long(NO_DESC_FLAG)
                        .action(ArgAction::SetTrue)
                        .help("disable completion descriptions"),
                ),
        );
    }
    cmd.subcommands(crate::cli::completion_spec::export_commands())
}

fn shell_long_help(sh: Shell, p: &str) -> String {
    match sh {
        Shell::Bash => format!(
            "Generate the autocompletion script for the bash shell.\n\n\
             This script depends on the 'bash-completion' package.\n\
             If it is not installed already, you can install it via your OS's package manager.\n\n\
             To load completions in your current shell session:\n\n\
             \tsource <({p} completion bash)\n\n\
             To load completions for every new session, execute once:\n\n\
             #### Linux:\n\n\
             \t{p} completion bash > /etc/bash_completion.d/{p}\n\n\
             #### macOS:\n\n\
             \t{p} completion bash > $(brew --prefix)/etc/bash_completion.d/{p}\n\n\
             You will need to start a new shell for this setup to take effect.\n"
        ),
        Shell::Zsh => format!(
            "Generate the autocompletion script for the zsh shell.\n\n\
             If shell completion is not already enabled in your environment you will need\n\
             to enable it.  You can execute the following once:\n\n\
             \techo \"autoload -U compinit; compinit\" >> ~/.zshrc\n\n\
             To load completions in your current shell session:\n\n\
             \tsource <({p} completion zsh)\n\n\
             To load completions for every new session, execute once:\n\n\
             #### Linux:\n\n\
             \t{p} completion zsh > \"${{fpath[1]}}/_{p}\"\n\n\
             #### macOS:\n\n\
             \t{p} completion zsh > $(brew --prefix)/share/zsh/site-functions/_{p}\n\n\
             You will need to start a new shell for this setup to take effect.\n"
        ),
        Shell::Fish => format!(
            "Generate the autocompletion script for the fish shell.\n\n\
             To load completions in your current shell session:\n\n\
             \t{p} completion fish | source\n\n\
             To load completions for every new session, execute once:\n\n\
             \t{p} completion fish > ~/.config/fish/completions/{p}.fish\n\n\
             You will need to start a new shell for this setup to take effect.\n"
        ),
        Shell::PowerShell => format!(
            "Generate the autocompletion script for powershell.\n\n\
             To load completions in your current shell session:\n\n\
             \t{p} completion powershell | Out-String | Invoke-Expression\n\n\
             To load completions for every new session, add the output of the above command\n\
             to your powershell profile.\n"
        ),
    }
}
