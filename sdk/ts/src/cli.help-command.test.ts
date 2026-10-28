import { describe, it, expect } from 'vitest';
import { CommanderError } from 'commander';
import { createCLI, setCommandGroup } from './cli';

// "help <operand...>" resolution, matching the Go root's order:
// "all", then a fully-resolved command path, then a group ID, else a
// usage error (exit 2). A command wins over a group of the same name.

const ANSI = /\x1b\[[0-9;]*m/g;

interface Run { out: string; err: string; code: number }

function makeCLI() {
  const { program } = createCLI({
    name: 'prog', version: '1.0.0', description: 'root desc',
    groups: [
      { id: 'management', title: 'MANAGEMENT', hidden: true },
      { id: 'extras', title: 'EXTRAS' },
    ],
  });
  let out = '';
  let err = '';
  program.exitOverride();
  program.configureOutput({
    writeOut: (s: string) => { out += s; },
    writeErr: (s: string) => { err += s; },
  });
  program.command('map').alias('mp').description('Map things')
    .option('--deep', 'Map recursively');
  const remote = program.command('remote').description('Manage remotes');
  remote.command('add').description('Add a remote')
    .option('--fetch', 'Fetch after adding');
  setCommandGroup(program.command('config').description('Manage config'), 'management');
  // Command sharing its name with a group ID.
  program.command('extras').description('Extras command');
  return {
    program,
    run(args: string[]): Run {
      let code = -1;
      // Group help is signalled through process.argv; keep it local.
      const argv = process.argv;
      process.argv = [...argv];
      try {
        program.parse(args, { from: 'user' });
        code = 0;
      } catch (e) {
        if (!(e instanceof CommanderError)) throw e;
        code = e.exitCode;
      } finally {
        process.argv = argv;
      }
      return { out: out.replace(ANSI, ''), err: err.replace(ANSI, ''), code };
    },
  };
}

describe('help <command> subcommand', () => {
  it('help <command> prints that command\'s help, not root help', () => {
    const r = makeCLI().run(['help', 'map']);
    expect(r.code).toBe(0);
    expect(r.out).toContain('Usage: prog map');
    expect(r.out).toContain('--deep');
    expect(r.out).not.toContain('COMMANDS:');
  });

  it('help <alias> resolves to the aliased command', () => {
    const r = makeCLI().run(['help', 'mp']);
    expect(r.code).toBe(0);
    expect(r.out).toContain('Usage: prog map');
  });

  it('help <a> <b> prints nested command help', () => {
    const r = makeCLI().run(['help', 'remote', 'add']);
    expect(r.code).toBe(0);
    expect(r.out).toContain('Usage: prog remote add');
    expect(r.out).toContain('--fetch');
  });

  it('help <group> still prints group help', () => {
    const r = makeCLI().run(['help', 'management']);
    expect(r.code).toBe(0);
    expect(r.out).toContain('MANAGEMENT:');
    expect(r.out).toContain('config');
  });

  it('command wins over a group of the same name', () => {
    const r = makeCLI().run(['help', 'extras']);
    expect(r.code).toBe(0);
    expect(r.out).toContain('Usage: prog extras');
    expect(r.out).not.toContain('EXTRAS:');
  });

  it('unknown topic is a usage error (exit 2)', () => {
    const r = makeCLI().run(['help', 'nosuch']);
    expect(r.code).toBe(2);
    expect(r.err).toContain('unknown help topic \'nosuch\'');
    expect(r.out).not.toContain('Usage: prog');
  });

  it('partial path is a usage error, not parent help', () => {
    const r = makeCLI().run(['help', 'remote', 'nosuch']);
    expect(r.code).toBe(2);
    expect(r.err).toContain('unknown help topic \'remote nosuch\'');
  });
});
