/**
 * @module completion
 * @package @hop-top/kit
 *
 * Shell completion script generators for Commander-based CLIs.
 * Extracts subcommand names and flag names from the program tree
 * and generates bash/zsh/fish completion scripts.
 */

import { Command } from 'commander';

/** Collect all visible subcommand names + aliases (non-hidden, excluding help). */
function collectCommands(cmd: Command): string[] {
  return cmd.commands
    .filter(c => !(c as any)._hidden && c.name() !== 'help')
    .flatMap(c => [c.name(), ...c.aliases()]);
}

/** Collect all option long-names (e.g. --format, --quiet). */
function collectFlags(cmd: Command): string[] {
  return cmd.options.map(o => o.long).filter(Boolean) as string[];
}

/** Generate a bash completion script for the given program. */
export function generateBashCompletion(program: Command): string {
  const name = program.name();
  const cmds = collectCommands(program);
  const flags = collectFlags(program);
  const words = [...cmds, ...flags].join(' ');

  return `# bash completion for ${name}
# eval "$(${name} completion bash)"

_${name}_completions() {
  local cur="\${COMP_WORDS[COMP_CWORD]}"
  local commands="${words}"

  COMPREPLY=( $(compgen -W "\${commands}" -- "\${cur}") )
}

complete -F _${name}_completions ${name}
`;
}

/** Generate a zsh completion script for the given program. */
export function generateZshCompletion(program: Command): string {
  const name = program.name();
  const cmds = collectCommands(program);
  const flags = collectFlags(program);

  const cmdEntries = cmds
    .map(c => {
      const sub = program.commands.find(
        s => s.name() === c || s.aliases().includes(c),
      );
      const desc = sub?.description() ?? '';
      return `    '${c}:${desc}'`;
    })
    .join('\n');

  const flagEntries = flags
    .map(f => `    '${f}[${f}]'`)
    .join('\n');

  return `#compdef ${name}
# eval "$(${name} completion zsh)"

_${name}() {
  local -a commands flags

  commands=(
${cmdEntries}
  )

  flags=(
${flagEntries}
  )

  _arguments -s \\
    '1:command:->cmds' \\
    '*::flags:->flags'

  case "$state" in
    cmds)
      _describe -t commands 'commands' commands
      ;;
    flags)
      _describe -t flags 'flags' flags
      ;;
  esac
}

compdef _${name} ${name}
`;
}

/** Generate a fish completion script for the given program. */
export function generateFishCompletion(program: Command): string {
  const name = program.name();
  const cmds = collectCommands(program);
  const flags = collectFlags(program);

  const lines = [
    `# fish completion for ${name}`,
    `# ${name} completion fish | source`,
    '',
  ];

  for (const c of cmds) {
    const sub = program.commands.find(
      s => s.name() === c || s.aliases().includes(c),
    );
    const desc = sub?.description() ?? '';
    lines.push(
      `complete -c ${name} -n '__fish_use_subcommand' ` +
      `-a '${c}' -d '${desc}'`
    );
  }

  for (const f of flags) {
    const long = f.replace(/^--/, '');
    lines.push(
      `complete -c ${name} -l '${long}'`
    );
  }

  lines.push('');
  return lines.join('\n');
}

/**
 * Create a hidden `completion` command with bash/zsh/fish subcommands.
 * Each prints the completion script to stdout.
 */
export function completionCommand(program: Command): Command {
  const cmd = new Command('completion')
    .description('Generate shell completion scripts');

  cmd.command('bash')
    .description('Generate bash completion script')
    .action(() => {
      process.stdout.write(generateBashCompletion(program));
    });

  cmd.command('zsh')
    .description('Generate zsh completion script')
    .action(() => {
      process.stdout.write(generateZshCompletion(program));
    });

  cmd.command('fish')
    .description('Generate fish completion script')
    .action(() => {
      process.stdout.write(generateFishCompletion(program));
    });

  return cmd;
}
