import { describe, it, expect, vi, afterEach } from 'vitest';
import { Command } from 'commander';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { buildSync } from 'esbuild';

import {
  bindArgs, bindFlag, completeRequest, completionCommand,
  generateBashCompletion, generateFishCompletion,
  generatePowerShellCompletion, generateZshCompletion,
  installCompletionRequest, renderCompletionScript,
} from './completion';
import { funcCompleter, staticValues } from './completion-values';
import { createCLI, setCommandGroup } from './cli';

/** Strip ANSI SGR sequences. */
function stripAnsi(s: string): string {
  return s.replace(/\x1b\[[^m]*m/g, '');
}

/** Runs a request against `program`, returning stdout and stderr. */
async function request(program: Command, args: string[], noDesc = false) {
  let out = '';
  let err = '';
  await completeRequest(program, args, noDesc, {
    out: s => { out += s; },
    err: s => { err += s; },
  });
  return { out, err };
}

/** Run a shell in a throwaway HOME; null when the shell is absent. */
function runShell(shell: string, args: (dir: string) => string[],
                  files: Record<string, string>, path = '') {
  const which = spawnSync('/bin/sh', ['-c', `command -v ${shell}`]);
  if (which.status !== 0) return null;
  const home = mkdtempSync(join(tmpdir(), 'kit-compl-'));
  try {
    for (const [n, body] of Object.entries(files)) {
      writeFileSync(join(home, n), body);
      chmodSync(join(home, n), 0o755);
    }
    return spawnSync(shell, args(home), {
      env: { PATH: `${path ? path.replace('$HOME', home) + ':' : ''}${process.env.PATH ?? ''}`,
             HOME: home, XDG_CONFIG_HOME: join(home, '.config'),
             XDG_DATA_HOME: join(home, '.local', 'share') },
      encoding: 'utf8',
    });
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
}

describe('completion scripts', () => {
  it('render cobra\'s scripts for the program name, every token replaced', () => {
    const p = new Command('my-tool');
    for (const s of [generateBashCompletion(p), generateZshCompletion(p),
                     generateFishCompletion(p), generatePowerShellCompletion(p)]) {
      expect(s).not.toContain('@@');
      expect(s).toContain(' __complete ');
    }
    expect(generateBashCompletion(p)).toContain('__start_my-tool');
    expect(generateZshCompletion(p)).toContain('compdef _my-tool my-tool');
  });

  it('call __completeNoDesc without descriptions', () => {
    const s = renderCompletionScript('zsh', 'tool', false);
    expect(s).toContain('__completeNoDesc');
    expect(s).not.toContain(' __complete ');
    expect(generateFishCompletion(new Command('tool'), false)).toContain('__completeNoDesc');
  });

  it.each(['bash', 'zsh', 'fish'])('%s parses the scripts', shell => {
    for (const name of ['tool', 'kit-app', 'a:b-c']) {
      for (const desc of [true, false]) {
        const res = runShell(shell, d => ['-n', join(d, 'script')],
          { script: renderCompletionScript(shell as 'bash', name, desc) });
        if (res) expect(res.status, `${shell} -n ${name}: ${res.stderr}`).toBe(0);
      }
    }
  });
});

describe('completionCommand', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('returns a Command named completion', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
    });
    const cmd = completionCommand(program);
    expect(cmd).toBeInstanceOf(Command);
    expect(cmd.name()).toBe('completion');
  });

  it('has a leaf per shell with --no-descriptions', () => {
    const cmd = completionCommand(new Command('mytool'));
    expect(cmd.commands.map(c => c.name())).toEqual(['bash', 'zsh', 'fish', 'powershell']);
    for (const c of cmd.commands) {
      expect(c.options.map(o => o.long)).toEqual(['--no-descriptions']);
    }
  });

  it('writes the script for the shell', async () => {
    const program = new Command('mytool');
    program.addCommand(completionCommand(program));
    let got = '';
    vi.spyOn(process.stdout, 'write').mockImplementation((s: string | Uint8Array) => {
      got += String(s);
      return true;
    });
    await program.parseAsync(['completion', 'fish', '--no-descriptions'], { from: 'user' });
    vi.restoreAllMocks();
    expect(got).toBe(renderCompletionScript('fish', 'mytool', false));
  });

  it('hidden when placed in a hidden management group', () => {
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

  it('is not offered as a subcommand', async () => {
    const program = new Command('mytool');
    program.command('deploy').description('Deploy');
    program.addCommand(completionCommand(program));
    expect((await request(program, [''])).out).toBe('deploy\tDeploy\n:4\n');
  });
});

describe('bindFlag / bindArgs', () => {
  it('first flag binding wins; dashes optional; unknown flags are silent', async () => {
    const program = new Command('t');
    const deploy = program.command('deploy').option('--env <e>', 'Env');
    bindFlag(deploy, '--env', staticValues('prod'));
    bindFlag(deploy, 'env', staticValues('other'));
    bindFlag(deploy, 'nosuch', staticValues('x'));
    expect((await request(program, ['deploy', '--env', ''])).out).toBe('prod\n:4\n');
  });

  it('args binding is replaced and sees the context', async () => {
    const program = new Command('t');
    const deploy = program.command('deploy');
    let seen;
    bindArgs(deploy, staticValues('old'));
    bindArgs(deploy, funcCompleter(async (_p, ctx) => {
      seen = ctx;
      return [{ value: 'new' }];
    }));
    expect((await request(program, ['deploy', 'a', ''])).out).toBe('new\n:4\n');
    expect(seen).toEqual({ commandPath: 'deploy', args: ['a'] });
  });

  it('binds a root flag that subcommands inherit', async () => {
    const { program } = createCLI({ name: 't', version: '1', description: 'T' });
    program.command('deploy').description('Deploy');
    bindFlag(program, 'format', staticValues('json', 'yaml'));
    expect((await request(program, ['deploy', '--format', 'j'])).out).toBe('json\n:4\n');
    const names = (await request(program, ['deploy', '--for'])).out;
    expect(names).toContain('--format\t');
  });
});

describe('__complete requests', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    delete process.env['APOS_TOOL_COMPLETION_DESCRIPTIONS'];
    process.exitCode = undefined;
  });

  function tool() {
    const program = new Command('apos-tool');
    const hook = vi.fn();
    const action = vi.fn();
    program.hook('preAction', hook);
    program.command('deploy').description("Don't deploy: it's \"live\"").action(action);
    installCompletionRequest(program);
    return { program, hook, action };
  }

  function captureStd() {
    const io = { out: '', err: '' };
    vi.spyOn(process.stdout, 'write').mockImplementation((s: string | Uint8Array) => {
      io.out += String(s);
      return true;
    });
    vi.spyOn(process.stderr, 'write').mockImplementation((s: string | Uint8Array) => {
      io.err += String(s);
      return true;
    });
    return io;
  }

  it('parseAsync answers before commander runs hooks or actions', async () => {
    const { program, hook, action } = tool();
    const io = captureStd();
    await program.parseAsync(['node', 'apos-tool', '__complete', 'de']);
    expect(io.out).toBe("deploy\tDon't deploy: it's \"live\"\n:4\n");
    expect(io.err).toContain('Completion ended with directive: ShellCompDirectiveNoFileComp');
    expect(hook).not.toHaveBeenCalled();
    expect(action).not.toHaveBeenCalled();
  });

  it('parse answers too, without descriptions for __completeNoDesc', async () => {
    const { program, action } = tool();
    const io = captureStd();
    program.parse(['__completeNoDesc', 'de'], { from: 'user' });
    await vi.waitFor(() => expect(io.out).toBe('deploy\n:4\n'));
    expect(action).not.toHaveBeenCalled();
  });

  it('<PROGRAM>_COMPLETION_DESCRIPTIONS=false drops descriptions', async () => {
    const { program } = tool();
    process.env['APOS_TOOL_COMPLETION_DESCRIPTIONS'] = 'false';
    expect((await request(program, ['de'])).out).toBe('deploy\n:4\n');
  });

  it('a request without words fails', async () => {
    const { program } = tool();
    const io = captureStd();
    await program.parseAsync(['__complete'], { from: 'user' });
    expect(io.err).toContain('requires at least 1 arg(s), only received 0');
    expect(process.exitCode).toBe(1);
  });

  it('other argv reach commander', async () => {
    const { program, action } = tool();
    await program.parseAsync(['deploy'], { from: 'user' });
    expect(action).toHaveBeenCalled();
  });

  it('createCLI programs answer requests', async () => {
    const { program } = createCLI({ name: 't', version: '1', description: 'T' });
    program.command('deploy').description('Deploy');
    const io = captureStd();
    await program.parseAsync(['__complete', 'dep'], { from: 'user' });
    expect(io.out).toBe('deploy\tDeploy\n:4\n');
  });

  it('flag values and positional choices', async () => {
    const program = new Command('t');
    const run = program.command('run').option('--mode <m>', 'Mode');
    run.options[0]!.choices(['fast', 'slow']);
    run.argument('<target>').registeredArguments[0]!.choices(['web', 'api']);
    expect((await request(program, ['run', '--m'])).out)
      .toBe('--mode\tMode (one of: fast, slow)\n:4\n');
    expect((await request(program, ['run', '--mode', 'F'])).out).toBe('fast\n:4\n');
    expect((await request(program, ['run', 'w'])).out).toBe('web\n:4\n');
  });
});

describe('real shells', () => {
  // A program on PATH answering __complete through the bundled sources.
  function bundle(dir: string): string {
    const entry = join(dir, 'entry.ts');
    writeFileSync(entry, `
      import { Command } from 'commander';
      import { bindFlag, completionCommand, installCompletionRequest } from ${JSON.stringify(join(__dirname, 'completion'))};
      import { staticValues } from ${JSON.stringify(join(__dirname, 'completion-values'))};
      const program = new Command('kittool');
      const grp = program.command('group').description('Group ops');
      const sub = grp.command('sub').description("Don't panic: it's fine").option('--env <e>', 'Env');
      bindFlag(sub, 'env', staticValues('prod', 'preview'));
      program.addCommand(completionCommand(program));
      installCompletionRequest(program);
      program.parse();
    `);
    const out = join(dir, 'kittool.cjs');
    buildSync({ entryPoints: [entry], bundle: true, platform: 'node', outfile: out,
                nodePaths: [join(__dirname, '..', 'node_modules')], logLevel: 'silent' });
    return `#!/bin/sh\nexec ${JSON.stringify(process.execPath)} ${JSON.stringify(out)} "$@"\n`;
  }

  it('fish completes nested commands, flag values and quoted descriptions', () => {
    const dir = mkdtempSync(join(tmpdir(), 'kit-compl-build-'));
    try {
      const launcher = bundle(dir);
      const script = renderCompletionScript('fish', 'kittool', true);
      const res = runShell('fish', d => ['--no-config', '-c', [
        `source ${join(d, 'kittool.fish')}`,
        `complete -C 'kittool group '`,
        `complete -C 'kittool group sub --env pr'`,
      ].join('; ')], { 'kittool': launcher, 'kittool.fish': script }, '$HOME');
      if (!res) return;
      expect(res.stderr).toBe('');
      // fish sorts candidates.
      expect(res.stdout).toBe("sub\tDon't panic: it's fine\npreview\nprod\n");
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
