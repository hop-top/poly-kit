import { describe, it, expect, vi } from 'vitest';
import { Command } from 'commander';
import { dispatch } from '../../src/output/dispatch';
import { registerOutputFlags, resolveCols } from '../../src/output/flags';
import '../../src/output/builtins';

function makeProgram(argv: readonly string[]) {
  const program = new Command()
    .name('test')
    .exitOverride()
    .configureOutput({ writeOut: () => {}, writeErr: () => {} });
  registerOutputFlags(program);
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
  return {
    restore: () => spy.mockRestore(),
    out: () => Buffer.concat(chunks).toString(),
  };
}

const rows = [
  { id: '1', name: 'Alice', notes: 'a' },
  { id: '2', name: 'Bob', notes: 'b' },
];

describe('resolveCols', () => {
  it('splits comma-separated values', () => {
    expect(resolveCols({ cols: ['a,b', 'c'] })).toEqual(['a', 'b', 'c']);
  });

  it('dedupes preserving first-seen order', () => {
    expect(resolveCols({ cols: ['a,b', 'a,c'] })).toEqual(['a', 'b', 'c']);
  });

  it('merges --cols + --columns', () => {
    expect(resolveCols({ cols: ['a'], columns: ['b'] })).toEqual(['a', 'b']);
  });

  it('trims whitespace', () => {
    expect(resolveCols({ cols: [' a , b '] })).toEqual(['a', 'b']);
  });
});

describe('dispatch — --cols on table', () => {
  it('limits columns to subset', async () => {
    const program = makeProgram(['--cols', 'id,name']);
    const cap = captureStdout();
    try {
      await dispatch(program, rows);
    } finally {
      cap.restore();
    }
    const out = cap.out();
    expect(out).toContain('id');
    expect(out).toContain('name');
    expect(out).not.toContain('notes');
  });
});

describe('dispatch — --cols on json', () => {
  it('projects rows to subset keys', async () => {
    const program = makeProgram(['--format', 'json', '--cols', 'id']);
    const cap = captureStdout();
    try {
      await dispatch(program, rows);
    } finally {
      cap.restore();
    }
    const parsed = JSON.parse(cap.out()) as Array<Record<string, unknown>>;
    expect(parsed).toEqual([{ id: '1' }, { id: '2' }]);
  });
});

describe('dispatch — --cols on yaml', () => {
  it('projects rows to subset keys', async () => {
    const program = makeProgram(['--format', 'yaml', '--cols', 'id']);
    const cap = captureStdout();
    try {
      await dispatch(program, rows);
    } finally {
      cap.restore();
    }
    const out = cap.out();
    expect(out).toContain('id:');
    expect(out).not.toContain('name:');
  });
});

describe('dispatch — --cols on csv', () => {
  it('limits columns', async () => {
    const program = makeProgram(['--format', 'csv', '--cols', 'name']);
    const cap = captureStdout();
    try {
      await dispatch(program, rows);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toBe('name\nAlice\nBob\n');
  });
});

describe('dispatch — --cols unknown column', () => {
  it('throws with valid list', async () => {
    const program = makeProgram(['--cols', 'bogus']);
    await expect(dispatch(program, rows)).rejects.toThrow(/unknown column "bogus"/);
  });
});

describe('dispatch — --columns alias', () => {
  it('--columns has same effect as --cols', async () => {
    const program = makeProgram(['--format', 'csv', '--columns', 'id']);
    const cap = captureStdout();
    try {
      await dispatch(program, rows);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toBe('id\n1\n2\n');
  });
});

describe('dispatch — --cols dedupe', () => {
  it('dedupes repeated columns', async () => {
    const program = makeProgram([
      '--format', 'csv',
      '--cols', 'id,id',
      '--cols', 'id',
    ]);
    const cap = captureStdout();
    try {
      await dispatch(program, rows);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toBe('id\n1\n2\n');
  });
});
