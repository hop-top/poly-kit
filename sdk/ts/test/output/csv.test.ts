import { describe, it, expect, vi } from 'vitest';
import { csvFormatter } from '../../src/output/formatters/csv';
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

const rows = [
  { id: '1', name: 'Alice', notes: 'a, b' },
  { id: '2', name: 'Bob', notes: '"quoted"' },
];

describe('csv — defaults', () => {
  it('emits header + rows with comma delimiter and CRLF default off', () => {
    const s = makeStream();
    csvFormatter.render(
      s.stream,
      rows,
      { delimiter: ',', 'no-header': false, 'quote-all': false, crlf: false },
      [],
    );
    const out = s.captured();
    expect(out.split('\n')[0]).toBe('id,name,notes');
    expect(out).toContain('1,Alice,"a, b"');
    expect(out).toContain('2,Bob,"""quoted"""');
    expect(out).not.toContain('\r\n');
  });
});

describe('csv — delimiter override', () => {
  it('uses semicolon when set', () => {
    const s = makeStream();
    csvFormatter.render(
      s.stream,
      rows,
      { delimiter: ';', 'no-header': false, 'quote-all': false, crlf: false },
      [],
    );
    expect(s.captured().split('\n')[0]).toBe('id;name;notes');
  });

  it('throws on multi-char delimiter', () => {
    const s = makeStream();
    expect(() =>
      csvFormatter.render(
        s.stream,
        rows,
        { delimiter: '||', 'no-header': false, 'quote-all': false, crlf: false },
        [],
      ),
    ).toThrow(/exactly one character/);
  });
});

describe('csv — no-header', () => {
  it('omits header row', () => {
    const s = makeStream();
    csvFormatter.render(
      s.stream,
      rows,
      { delimiter: ',', 'no-header': true, 'quote-all': false, crlf: false },
      [],
    );
    expect(s.captured().split('\n')[0]).toBe('1,Alice,"a, b"');
  });
});

describe('csv — quote-all', () => {
  it('quotes every field', () => {
    const s = makeStream();
    csvFormatter.render(
      s.stream,
      [{ a: '1', b: 'x' }],
      { delimiter: ',', 'no-header': false, 'quote-all': true, crlf: false },
      [],
    );
    const out = s.captured();
    expect(out).toContain('"a","b"');
    expect(out).toContain('"1","x"');
  });
});

describe('csv — crlf', () => {
  it('uses CRLF line endings', () => {
    const s = makeStream();
    csvFormatter.render(
      s.stream,
      [{ a: '1' }],
      { delimiter: ',', 'no-header': false, 'quote-all': false, crlf: true },
      [],
    );
    expect(s.captured()).toContain('\r\n');
  });
});

describe('csv — empty input', () => {
  it('produces no output', () => {
    const s = makeStream();
    csvFormatter.render(s.stream, [], {}, []);
    expect(s.captured()).toBe('');
  });
});

describe('csv — cols subset', () => {
  it('filters to requested columns', () => {
    const s = makeStream();
    csvFormatter.render(s.stream, rows, {}, ['id', 'name']);
    const out = s.captured();
    expect(out.split('\n')[0]).toBe('id,name');
    expect(out).not.toContain('notes');
  });
});

describe('csv — registry', () => {
  it('registered as "csv" with .csv extension on defaultRegistry', () => {
    const f = defaultRegistry.lookup('csv');
    expect(f).toBe(csvFormatter);
    expect(defaultRegistry.extensionMap().get('.csv')).toBe('csv');
  });
});
