import { describe, it, expect, vi } from 'vitest';
import { Command } from 'commander';
import { dispatch } from '../../src/output/dispatch';
import { registerOutputFlags } from '../../src/output/flags';
import {
  listFormats,
  formatOptions,
  renderFormatHelp,
} from '../../src/output/format_help';
import { defaultRegistry } from '../../src/output/registry';
import '../../src/output/builtins';

function makeStream() {
  const chunks: string[] = [];
  const stream = {
    write: vi.fn((c: string) => {
      chunks.push(c);
      return true;
    }),
  } as unknown as NodeJS.WritableStream;
  return { stream, captured: () => chunks.join('') };
}

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

describe('listFormats', () => {
  it('returns one row per registered formatter sorted by key', () => {
    const rows = listFormats(defaultRegistry);
    const keys = rows.map(r => r.format);
    expect(keys).toEqual(['csv', 'json', 'table', 'text', 'yaml']);
  });

  it('csv row exposes options sorted alphabetically', () => {
    const rows = listFormats(defaultRegistry);
    const csv = rows.find(r => r.format === 'csv')!;
    expect(csv.options).toBe('crlf, delimiter, no-header, quote-all');
  });
});

describe('formatOptions', () => {
  it('returns one row per OptionSpec for csv', () => {
    const rows = formatOptions(defaultRegistry, 'csv');
    expect(rows.map(r => r.name)).toEqual(['delimiter', 'no-header', 'quote-all', 'crlf']);
  });

  it('throws for unknown key with valid list', () => {
    expect(() => formatOptions(defaultRegistry, 'bogus')).toThrow(
      /unknown format "bogus"/,
    );
  });
});

describe('renderFormatHelp', () => {
  it('without format → renders catalog table', () => {
    const s = makeStream();
    renderFormatHelp(s.stream, defaultRegistry, '');
    const out = s.captured();
    expect(out).toContain('format');
    expect(out).toContain('csv');
    expect(out).toContain('json');
  });

  it('with format=csv → renders csv options table', () => {
    const s = makeStream();
    renderFormatHelp(s.stream, defaultRegistry, 'csv');
    const out = s.captured();
    expect(out).toContain('delimiter');
    expect(out).toContain('no-header');
  });

  it('with format=table (no options) → prints sentinel line', () => {
    const s = makeStream();
    renderFormatHelp(s.stream, defaultRegistry, 'table');
    expect(s.captured()).toContain('"table" has no options');
  });

  it('throws for unknown format', () => {
    const s = makeStream();
    expect(() => renderFormatHelp(s.stream, defaultRegistry, 'bogus')).toThrow(
      /unknown format "bogus"/,
    );
  });
});

describe('dispatch — --format-help short-circuit', () => {
  it('--format-help (bare) lists registry', async () => {
    const program = makeProgram(['--format-help']);
    const cap = captureStdout();
    try {
      await dispatch(program, []);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toContain('format');
    expect(cap.out()).toContain('csv');
  });

  it('--format-help csv renders csv options', async () => {
    const program = makeProgram(['--format-help', 'csv']);
    const cap = captureStdout();
    try {
      await dispatch(program, []);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toContain('delimiter');
  });
});
