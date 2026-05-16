import { describe, it, expect, vi } from 'vitest';
import { Command } from 'commander';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { dispatch } from '../../src/output/dispatch';
import { registerOutputFlags } from '../../src/output/flags';
import '../../src/output/builtins';

function makeProgram(argv: readonly string[]) {
  const program = new Command()
    .name('test')
    .exitOverride()
    .configureOutput({
      writeOut: () => {},
      writeErr: () => {},
    });
  registerOutputFlags(program);
  program.action(() => {});
  program.parse(['node', 'test', ...argv], { from: 'node' });
  return program;
}

function captureStdout(): { restore: () => void; out: () => string } {
  const chunks: Buffer[] = [];
  const original = process.stdout.write.bind(process.stdout);
  const spy = vi.spyOn(process.stdout, 'write').mockImplementation(
    ((c: string | Buffer) => {
      chunks.push(Buffer.isBuffer(c) ? c : Buffer.from(c));
      return true;
    }) as typeof process.stdout.write,
  );
  return {
    restore: () => spy.mockRestore() && original.toString,
    out: () => Buffer.concat(chunks).toString(),
  };
}

describe('dispatch — --format-opt parsing', () => {
  it('passes parsed options to formatter via key=value', async () => {
    const program = makeProgram([
      '--format', 'csv',
      '--format-opt', 'delimiter=;',
      '--format-opt', 'no-header=true',
    ]);
    const cap = captureStdout();
    try {
      await dispatch(program, [{ a: '1', b: 'x' }]);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toBe('1;x\n');
  });

  it('throws on unknown option key with valid list', async () => {
    const program = makeProgram([
      '--format', 'csv',
      '--format-opt', 'bogus=x',
    ]);
    await expect(dispatch(program, [{ a: '1' }])).rejects.toThrow(
      /unknown option "bogus" \(valid: delimiter, no-header, quote-all, crlf\)/,
    );
  });

  it('throws on type error', async () => {
    const program = makeProgram([
      '--format', 'json',
      '--format-opt', 'indent=abc',
    ]);
    await expect(dispatch(program, { a: 1 })).rejects.toThrow(/not an int/);
  });

  it('key-only form for bool spec sets true', async () => {
    const program = makeProgram([
      '--format', 'csv',
      '--format-opt', 'no-header',
    ]);
    const cap = captureStdout();
    try {
      await dispatch(program, [{ a: '1' }]);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toBe('1\n');
  });

  it('repeated --format-opt accumulates', async () => {
    const program = makeProgram([
      '--format', 'csv',
      '--format-opt', 'delimiter=|',
      '--format-opt', 'no-header',
    ]);
    const cap = captureStdout();
    try {
      await dispatch(program, [{ a: '1', b: 'x' }]);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toBe('1|x\n');
  });

  it('throws on out-of-enum value', async () => {
    const program = makeProgram([
      '--format', 'text',
      '--format-opt', 'style=tree',
    ]);
    await expect(dispatch(program, [{ a: '1' }])).rejects.toThrow(/not in \{kv, lines, paragraph\}/);
  });
});

describe('dispatch — default render', () => {
  it('renders table by default', async () => {
    const program = makeProgram([]);
    const cap = captureStdout();
    try {
      await dispatch(program, [{ id: '1', name: 'Alice' }]);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toContain('id');
    expect(cap.out()).toContain('Alice');
  });

  it('honors --format json', async () => {
    const program = makeProgram(['--format', 'json']);
    const cap = captureStdout();
    try {
      await dispatch(program, [{ id: '1' }]);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toContain('"id"');
  });
});

describe('dispatch — -o / --output to file', () => {
  it('writes output to the given path with -o', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'dispatch-o-'));
    const out = join(dir, 'report.json');
    try {
      const program = makeProgram(['--format', 'json', '-o', out]);
      await dispatch(program, [{ id: '1', name: 'Alice' }]);
      const body = readFileSync(out, 'utf8');
      expect(body).toContain('"id"');
      expect(body).toContain('Alice');
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it('writes output to the given path with --output', async () => {
    const dir = mkdtempSync(join(tmpdir(), 'dispatch-o-'));
    const out = join(dir, 'report.json');
    try {
      const program = makeProgram(['--format', 'json', '--output', out]);
      await dispatch(program, [{ id: '2' }]);
      const body = readFileSync(out, 'utf8');
      expect(body).toContain('"id"');
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it('subcommand reads -o from parent globals (optsWithGlobals)', async () => {
    // Mirrors the parity test setup: -o is registered on the root program,
    // a subcommand is invoked with `sub -o <file>`, and dispatch is called
    // from the subcommand context. The flag must be visible via
    // optsWithGlobals so subcommand handlers can locate the output path.
    const dir = mkdtempSync(join(tmpdir(), 'dispatch-sub-'));
    const out = join(dir, 'sub-report.json');
    try {
      let received: Command | null = null;
      const program = new Command()
        .name('test')
        .exitOverride()
        .configureOutput({ writeOut: () => {}, writeErr: () => {} });
      registerOutputFlags(program);
      const sub = program.command('do').action(function (this: Command) {
        received = this;
      });
      // Force `sub` to be referenced for linters; commander stores by name.
      void sub;
      program.parse(['node', 'test', 'do', '-o', out, '--format', 'json'],
        { from: 'node' });
      expect(received).not.toBeNull();
      await dispatch(received!, [{ k: 'v' }]);
      const body = readFileSync(out, 'utf8');
      expect(body).toContain('"k"');
      expect(body).toContain('"v"');
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
