import { describe, it, expect } from 'vitest';
import { generateBashCompletion, generateZshCompletion,
         generateFishCompletion, completionCommand } from './completion';
import { createCLI, setCommandGroup } from './cli';
import { Command } from 'commander';

/** Strip ANSI SGR sequences. */
function stripAnsi(s: string): string {
  return s.replace(/\x1b\[[^m]*m/g, '');
}

describe('generateBashCompletion', () => {
  it('contains program name in function name', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    program.command('deploy').description('Deploy');
    const script = generateBashCompletion(program);
    expect(script).toContain('_mytool_completions');
  });

  it('includes subcommand names', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    program.command('deploy').description('Deploy');
    program.command('build').description('Build');
    const script = generateBashCompletion(program);
    expect(script).toContain('deploy');
    expect(script).toContain('build');
  });

  it('includes flag names', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    const script = generateBashCompletion(program);
    expect(script).toContain('--format');
    expect(script).toContain('--quiet');
    expect(script).toContain('--no-color');
  });

  it('includes alias names', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    const deploy = program.command('deploy').description('Deploy');
    deploy.alias('d');
    const script = generateBashCompletion(program);
    expect(script).toContain('deploy');
    expect(script).toContain(' d ');
  });

  it('registers complete -F', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    const script = generateBashCompletion(program);
    expect(script).toContain('complete -F _mytool_completions mytool');
  });
});

describe('generateZshCompletion', () => {
  it('contains compdef for program name', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    program.command('deploy').description('Deploy');
    const script = generateZshCompletion(program);
    expect(script).toContain('compdef _mytool mytool');
  });

  it('includes subcommand names', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    program.command('deploy').description('Deploy');
    const script = generateZshCompletion(program);
    expect(script).toContain('deploy');
  });

  it('includes alias names with description', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    const deploy = program.command('deploy').description('Deploy');
    deploy.alias('d');
    const script = generateZshCompletion(program);
    expect(script).toContain("'d:Deploy'");
  });

  it('includes flag names', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    const script = generateZshCompletion(program);
    expect(script).toContain('--format');
    expect(script).toContain('--quiet');
  });
});

describe('generateFishCompletion', () => {
  it('uses program name in complete commands', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    program.command('deploy').description('Deploy');
    const script = generateFishCompletion(program);
    expect(script).toContain('complete -c mytool');
  });

  it('includes subcommand completions', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    program.command('deploy').description('Deploy');
    const script = generateFishCompletion(program);
    expect(script).toContain('deploy');
  });

  it('includes alias names', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    const deploy = program.command('deploy').description('Deploy');
    deploy.alias('d');
    const script = generateFishCompletion(program);
    expect(script).toContain("'d'");
    expect(script).toContain("'Deploy'");
  });

  it('includes flag completions with long names', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    const script = generateFishCompletion(program);
    // fish uses -l 'name' (no -- prefix)
    expect(script).toContain("-l 'format'");
    expect(script).toContain("-l 'quiet'");
  });
});

describe('completionCommand', () => {
  it('returns a Command named completion', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    const cmd = completionCommand(program);
    expect(cmd).toBeInstanceOf(Command);
    expect(cmd.name()).toBe('completion');
  });

  it('hidden when placed in a hidden management group', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      groups: [{ id: 'management', title: 'MANAGEMENT', hidden: true }],
    });
    program.command('run').description('Run');
    const cmd = completionCommand(program);
    setCommandGroup(cmd, 'management');
    program.addCommand(cmd);

    const help = stripAnsi(program.helpInformation());
    expect(help).not.toContain('completion');
  });

  it('has bash, zsh, fish subcommands', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    const cmd = completionCommand(program);
    const subNames = cmd.commands.map(c => c.name());
    expect(subNames).toContain('bash');
    expect(subNames).toContain('zsh');
    expect(subNames).toContain('fish');
  });

  it('hidden from default --help', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      groups: [{ id: 'management', title: 'MANAGEMENT', hidden: true }],
    });
    program.command('run').description('Run something');
    const cmd = completionCommand(program);
    setCommandGroup(cmd, 'management');
    program.addCommand(cmd);

    const help = stripAnsi(program.helpInformation());
    expect(help).not.toContain('completion');
  });

  it('visible in --help-all', () => {
    const origArgv = process.argv;
    try {
      process.argv = ['node', 'mytool', '--help-all'];
      const { program } = createCLI({
        name: 'mytool', version: '1.0.0', description: 'A tool',
        groups: [{ id: 'management', title: 'MANAGEMENT', hidden: true }],
      });
      program.command('run').description('Run something');
      const cmd = completionCommand(program);
      setCommandGroup(cmd, 'management');
      program.addCommand(cmd);

      const help = stripAnsi(program.helpInformation());
      expect(help).toContain('MANAGEMENT:');
      expect(help).toContain('completion');
    } finally {
      process.argv = origArgv;
    }
  });
});
