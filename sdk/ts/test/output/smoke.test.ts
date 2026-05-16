/**
 * Smoke tests mirroring T-1011's manual checklist. Each test exercises a
 * dispatch path end-to-end against an in-memory writer or temp file so the
 * suite is hermetic but covers the same surface a real `cli list` invocation
 * would.
 */
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
    .name('cli')
    .exitOverride()
    .configureOutput({ writeOut: () => {}, writeErr: () => {} });
  registerOutputFlags(program);
  program.action(() => {});
  // Smoke harness: invokes the root command directly. The "list" subcommand
  // in the spec checklist would be a real command in adopter CLIs; here we
  // exercise the same flag surface against the root program.
  program.parse(['node', 'cli', ...argv], { from: 'node' });
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

const tmp = mkdtempSync(join(tmpdir(), 'sdk-ts-smoke-'));
const rows = [
  { id: '1', name: 'Alice', status: 'active' },
  { id: '2', name: 'Bob', status: 'idle' },
];

describe('smoke — T-1011 checklist', () => {
  it('cli list (default table)', async () => {
    const program = makeProgram([]);
    const cap = captureStdout();
    try {
      await dispatch(program, rows);
    } finally {
      cap.restore();
    }
    const out = cap.out();
    expect(out).toContain('id');
    expect(out).toContain('name');
    expect(out).toContain('status');
    expect(out).toContain('Alice');
  });

  it('cli list --format json', async () => {
    const program = makeProgram(['--format', 'json']);
    const cap = captureStdout();
    try {
      await dispatch(program, rows);
    } finally {
      cap.restore();
    }
    expect(JSON.parse(cap.out())).toEqual(rows);
  });

  it("cli list --format csv --format-opt delimiter=';'", async () => {
    const program = makeProgram(['--format', 'csv', '--format-opt', 'delimiter=;']);
    const cap = captureStdout();
    try {
      await dispatch(program, rows);
    } finally {
      cap.restore();
    }
    const lines = cap.out().split('\n');
    expect(lines[0]).toBe('id;name;status');
    expect(lines[1]).toBe('1;Alice;active');
  });

  it('cli list --cols name,status -o /tmp/x.csv', async () => {
    const path = join(tmp, 'x.csv');
    const program = makeProgram(['--cols', 'name,status', '-o', path]);
    await dispatch(program, rows);
    expect(readFileSync(path, 'utf8')).toBe('name,status\nAlice,active\nBob,idle\n');
    rmSync(path);
  });

  it('cli list -o /tmp/x.json (ext infers json)', async () => {
    const path = join(tmp, 'x.json');
    const program = makeProgram(['-o', path]);
    await dispatch(program, rows);
    expect(JSON.parse(readFileSync(path, 'utf8'))).toEqual(rows);
    rmSync(path);
  });

  it('cli list -o /tmp/x.csv --format json (mismatch error)', async () => {
    const path = join(tmp, 'x.csv');
    const program = makeProgram(['-o', path, '--format', 'json']);
    await expect(dispatch(program, rows)).rejects.toThrow(
      /format "json" does not match output extension "\.csv"/,
    );
  });

  it('cli list --format-help', async () => {
    const program = makeProgram(['--format-help']);
    const cap = captureStdout();
    try {
      await dispatch(program, []);
    } finally {
      cap.restore();
    }
    const out = cap.out();
    expect(out).toContain('format');
    expect(out).toContain('csv');
    expect(out).toContain('json');
    expect(out).toContain('table');
    expect(out).toContain('text');
    expect(out).toContain('yaml');
  });

  it('cli list --format-help csv', async () => {
    const program = makeProgram(['--format-help', 'csv']);
    const cap = captureStdout();
    try {
      await dispatch(program, []);
    } finally {
      cap.restore();
    }
    const out = cap.out();
    expect(out).toContain('delimiter');
    expect(out).toContain('no-header');
    expect(out).toContain('quote-all');
    expect(out).toContain('crlf');
  });
});
