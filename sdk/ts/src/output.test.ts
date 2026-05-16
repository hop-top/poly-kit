import { describe, it, expect, vi } from 'vitest';
import { render, JSON_FORMAT, YAML_FORMAT, TABLE_FORMAT } from './output';

function makeStream(): { write: ReturnType<typeof vi.fn>; captured: () => string } {
  const chunks: string[] = [];
  const write = vi.fn((chunk: string) => { chunks.push(chunk); return true; });
  return { write, captured: () => chunks.join('') };
}

describe('render — JSON', () => {
  it('writes 2-space indented JSON + trailing newline', () => {
    const s = makeStream();
    render({ write: s.write } as unknown as NodeJS.WritableStream, JSON_FORMAT, { a: 1, b: 'x' });
    expect(s.captured()).toBe(JSON.stringify({ a: 1, b: 'x' }, null, 2) + '\n');
  });

  it('works with arrays', () => {
    const s = makeStream();
    render({ write: s.write } as unknown as NodeJS.WritableStream, JSON_FORMAT, [1, 2, 3]);
    expect(s.captured()).toBe(JSON.stringify([1, 2, 3], null, 2) + '\n');
  });
});

describe('render — YAML', () => {
  it('writes valid YAML', () => {
    const s = makeStream();
    render({ write: s.write } as unknown as NodeJS.WritableStream, YAML_FORMAT, { name: 'hop', version: 1 });
    const out = s.captured();
    expect(out).toContain('name: hop');
    expect(out).toContain('version: 1');
  });

  it('works with arrays', () => {
    const s = makeStream();
    render({ write: s.write } as unknown as NodeJS.WritableStream, YAML_FORMAT, ['a', 'b']);
    const out = s.captured();
    expect(out).toContain('- a');
    expect(out).toContain('- b');
  });
});

describe('render — table (array of objects)', () => {
  const rows = [
    { id: '1', name: 'Alice', age: '30' },
    { id: '2', name: 'Bob',   age: '25' },
  ];

  it('includes headers from object keys', () => {
    const s = makeStream();
    render({ write: s.write } as unknown as NodeJS.WritableStream, TABLE_FORMAT, rows);
    const out = s.captured();
    expect(out).toContain('id');
    expect(out).toContain('name');
    expect(out).toContain('age');
  });

  it('includes all row values', () => {
    const s = makeStream();
    render({ write: s.write } as unknown as NodeJS.WritableStream, TABLE_FORMAT, rows);
    const out = s.captured();
    expect(out).toContain('Alice');
    expect(out).toContain('Bob');
    expect(out).toContain('30');
    expect(out).toContain('25');
  });

  it('aligns columns (each column at least as wide as header)', () => {
    const s = makeStream();
    render({ write: s.write } as unknown as NodeJS.WritableStream, TABLE_FORMAT, rows);
    const lines = s.captured().split('\n').filter(Boolean);
    // header and two data rows
    expect(lines.length).toBe(3);
    // all lines same length (padded)
    const lengths = lines.map(l => l.length);
    expect(new Set(lengths).size).toBe(1);
  });
});

describe('render — table (single object)', () => {
  it('renders single object as 1-row table with headers', () => {
    const s = makeStream();
    render({ write: s.write } as unknown as NodeJS.WritableStream, TABLE_FORMAT, { id: '42', status: 'ok' });
    const out = s.captured();
    expect(out).toContain('id');
    expect(out).toContain('status');
    expect(out).toContain('42');
    expect(out).toContain('ok');
    const lines = out.split('\n').filter(Boolean);
    expect(lines.length).toBe(2);
  });
});

describe('render — table (empty array)', () => {
  it('produces no output for empty array', () => {
    const s = makeStream();
    render({ write: s.write } as unknown as NodeJS.WritableStream, TABLE_FORMAT, []);
    expect(s.captured()).toBe('');
    expect(s.write).not.toHaveBeenCalled();
  });
});

describe('render — unknown format', () => {
  it('throws for unrecognised format', () => {
    const s = makeStream();
    // 'bogus' replaces the previous 'csv' fixture: csv is now a registered
    // built-in, so the original would no longer be "unknown".
    expect(() =>
      render({ write: s.write } as unknown as NodeJS.WritableStream, 'bogus' as never, {}),
    ).toThrow(/unknown.*format/i);
  });
});

describe('exported constants', () => {
  it('JSON_FORMAT === "json"', () => expect(JSON_FORMAT).toBe('json'));
  it('YAML_FORMAT === "yaml"', () => expect(YAML_FORMAT).toBe('yaml'));
  it('TABLE_FORMAT === "table"', () => expect(TABLE_FORMAT).toBe('table'));
});
