import { describe, it, expect, vi, afterEach } from 'vitest';
import { Command } from 'commander';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { dispatch } from '../../src/output/dispatch';
import { registerOutputFlags } from '../../src/output/flags';
import '../../src/output/builtins';

function makeProgram(argv: readonly string[], opts?: Parameters<typeof registerOutputFlags>[1]) {
  const program = new Command()
    .name('test')
    .exitOverride()
    .configureOutput({ writeOut: () => {}, writeErr: () => {} });
  registerOutputFlags(program, opts);
  program.action(() => {});
  program.parse(['node', 'test', ...argv], { from: 'node' });
  return program;
}

function captureStdout(): { restore: () => void; out: () => string } {
  const chunks: Buffer[] = [];
  const spy = vi.spyOn(process.stdout, 'write').mockImplementation(
    ((c: string | Buffer) => {
      chunks.push(Buffer.isBuffer(c) ? c : Buffer.from(c));
      return true;
    }) as typeof process.stdout.write,
  );
  return { restore: () => spy.mockRestore(), out: () => Buffer.concat(chunks).toString() };
}

const dir = mkdtempSync(join(tmpdir(), 'sdk-ts-out-'));
afterEach(() => {});

describe('dispatch — --output stdout default', () => {
  it('default writes to process.stdout', async () => {
    const program = makeProgram([]);
    const cap = captureStdout();
    try {
      await dispatch(program, [{ id: '1' }]);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toContain('id');
    expect(cap.out()).toContain('1');
  });

  it('-o - writes to stdout (sentinel)', async () => {
    const program = makeProgram(['-o', '-']);
    const cap = captureStdout();
    try {
      await dispatch(program, [{ id: '1' }]);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toContain('id');
  });
});

describe('dispatch — --output file', () => {
  it('-o file writes + readback', async () => {
    const path = join(dir, 'out1.json');
    const program = makeProgram(['--format', 'json', '-o', path]);
    await dispatch(program, [{ id: '1' }]);
    expect(JSON.parse(readFileSync(path, 'utf8'))).toEqual([{ id: '1' }]);
    rmSync(path);
  });

  it('overwrites existing file', async () => {
    const path = join(dir, 'out2.json');
    let program = makeProgram(['--format', 'json', '-o', path]);
    await dispatch(program, [{ a: 1 }]);
    program = makeProgram(['--format', 'json', '-o', path]);
    await dispatch(program, [{ b: 2 }]);
    expect(JSON.parse(readFileSync(path, 'utf8'))).toEqual([{ b: 2 }]);
    rmSync(path);
  });
});

describe('dispatch — extension inference', () => {
  it('-o file.json with default --format infers json', async () => {
    const path = join(dir, 'out.json');
    const program = makeProgram(['-o', path]);
    await dispatch(program, [{ id: '1' }]);
    expect(JSON.parse(readFileSync(path, 'utf8'))).toEqual([{ id: '1' }]);
    rmSync(path);
  });

  it('-o file.csv with default --format infers csv', async () => {
    const path = join(dir, 'out.csv');
    const program = makeProgram(['-o', path]);
    await dispatch(program, [{ id: '1' }]);
    expect(readFileSync(path, 'utf8')).toBe('id\n1\n');
    rmSync(path);
  });

  it('explicit --format wins when matching extension', async () => {
    const path = join(dir, 'out.json');
    const program = makeProgram(['--format', 'json', '-o', path]);
    await dispatch(program, [{ id: '1' }]);
    expect(JSON.parse(readFileSync(path, 'utf8'))).toEqual([{ id: '1' }]);
    rmSync(path);
  });

  it('mismatch error: explicit --format json + .csv extension', async () => {
    const path = join(dir, 'out.csv');
    const program = makeProgram(['--format', 'json', '-o', path]);
    await expect(dispatch(program, [{ id: '1' }])).rejects.toThrow(
      /format "json" does not match output extension "\.csv"/,
    );
  });
});

describe('registerOutputFlags — disable', () => {
  it('disable.output suppresses -o', () => {
    const program = makeProgram([], { disable: { output: true } });
    expect(program.options.find(o => o.long === '--output')).toBeUndefined();
  });

  it('disable.format suppresses --format', () => {
    const program = makeProgram([], { disable: { format: true } });
    expect(program.options.find(o => o.long === '--format')).toBeUndefined();
  });
});
