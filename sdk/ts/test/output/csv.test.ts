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

// --- CR/LF preservation -------------------------------------------------

// The adversarial row that pins csv quoting rules byte-for-byte. Every field
// is a separate hazard: the delimiter, an internal quote, an embedded LF, a
// leading space, a trailing space, empty, a tab, and a LONE CR.
const adversarialRow = {
  a: 'plain',
  b: 'with,comma',
  c: 'with"quote',
  d: 'with\nnewline',
  e: ' leading space',
  f: 'trailing ',
  g: '',
  h: 'with\ttab',
  i: 'with\rcr',
};

const adversarialValues = [
  'plain',
  'with,comma',
  'with"quote',
  'with\nnewline',
  ' leading space',
  'trailing ',
  '',
  'with\ttab',
  'with\rcr',
];

/**
 * A minimal RFC 4180 reader. The package ships no csv parser, so round-trip
 * is proved against a decoder written to the grammar directly:
 * `escaped = DQUOTE *(TEXTDATA / COMMA / CR / LF / 2DQUOTE) DQUOTE`.
 * Records split only on an UNQUOTED line ending.
 */
function decodeCsv(input: string, delim = ','): string[][] {
  const records: string[][] = [];
  let record: string[] = [];
  let field = '';
  let inQuotes = false;
  let pending = false;

  for (let i = 0; i < input.length; i++) {
    const c = input[i];
    pending = true;
    if (inQuotes) {
      if (c === '"') {
        if (input[i + 1] === '"') {
          field += '"';
          i++;
        } else {
          inQuotes = false;
        }
      } else {
        field += c;
      }
      continue;
    }
    if (c === '"' && field === '') {
      inQuotes = true;
    } else if (c === delim) {
      record.push(field);
      field = '';
    } else if (c === '\r' || c === '\n') {
      if (c === '\r' && input[i + 1] === '\n') i++;
      record.push(field);
      field = '';
      records.push(record);
      record = [];
      pending = false;
    } else {
      field += c;
    }
  }
  if (pending || field !== '') {
    record.push(field);
    records.push(record);
  }
  return records;
}

describe('csv — CR/LF preservation', () => {
  const modes: [string, boolean, boolean][] = [
    ['lf', false, false],
    ['crlf', true, false],
    ['lf/quote-all', false, true],
    ['crlf/quote-all', true, true],
  ];

  // The load-bearing case: in CRLF mode csv-stringify left the embedded LF
  // UNQUOTED, so one 9-field record decoded as two records. That is not a
  // parity question — it is structurally invalid CSV.
  it.each(modes)('quotes and preserves CR/LF verbatim (%s)', (_label, crlf, quoteAll) => {
    const s = makeStream();
    csvFormatter.render(
      s.stream,
      [adversarialRow],
      { delimiter: ',', 'no-header': true, 'quote-all': quoteAll, crlf },
      [],
    );
    const out = s.captured();
    expect(out).toContain('"with\rcr"');
    expect(out).toContain('"with\nnewline"');
    expect(out).toContain('" leading space"');
    expect(out.endsWith(crlf ? '\r\n' : '\n')).toBe(true);
  });

  // Round-trip is the acceptance criterion, not byte-equality: byte-equality
  // alone would be satisfied by every runtime agreeing on lossy output.
  it.each(modes)('round-trips the adversarial row (%s)', (_label, crlf, quoteAll) => {
    const s = makeStream();
    csvFormatter.render(
      s.stream,
      [adversarialRow],
      { delimiter: ',', 'no-header': true, 'quote-all': quoteAll, crlf },
      [],
    );
    const recs = decodeCsv(s.captured());
    expect(recs).toHaveLength(1);
    expect(recs[0]).toEqual(adversarialValues);
  });

  it('changes only the record terminator in crlf mode', () => {
    const s = makeStream();
    csvFormatter.render(
      s.stream,
      [{ v: 'a\nb' }],
      { delimiter: ',', 'no-header': true, 'quote-all': false, crlf: true },
      [],
    );
    expect(s.captured()).toBe('"a\nb"\r\n');
  });

  it.each([
    ['tab', '\tlead', '"\tlead"\n'],
    ['space', ' lead', '" lead"\n'],
    ['nbsp', '\u00a0lead', '"\u00a0lead"\n'],
    ['vtab', '\vlead', '"\vlead"\n'],
    ['trailing space stays bare', 'trail ', 'trail \n'],
    ['plain stays bare', 'plain', 'plain\n'],
  ])('quotes any leading unicode space (%s)', (_label, input, want) => {
    const s = makeStream();
    csvFormatter.render(
      s.stream,
      [{ v: input }],
      { delimiter: ',', 'no-header': true, 'quote-all': false, crlf: false },
      [],
    );
    expect(s.captured()).toBe(want);
  });

  // `\.` alone on a line terminates a PostgreSQL COPY stream.
  it('quotes the postgres COPY sentinel', () => {
    const s = makeStream();
    csvFormatter.render(
      s.stream,
      [{ v: '\\.' }],
      { delimiter: ',', 'no-header': true, 'quote-all': false, crlf: false },
      [],
    );
    expect(s.captured()).toBe('"\\."\n');
  });
});
