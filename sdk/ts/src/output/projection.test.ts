/**
 * Ordering + header/key contract tests for the projection helpers and, more
 * importantly, for the dispatch path that actually reaches them.
 *
 * Rules pinned here:
 *   1. ColumnSpec list drives default column order + headers; payload key
 *      order is the fallback only when no ColumnSpec is supplied.
 *   2. --cols reorders as well as selects — user order wins.
 *   3. header == key universally.
 *   4. Zero rows emits nothing, decided by ROW count not header count.
 *   5. priority is accepted, stored, ignored.
 */

import { describe, it, expect, vi } from 'vitest';
import { Command } from 'commander';
import { dispatch } from './dispatch';
import { registerOutputFlags } from './flags';
import { deriveHeaders, filterColumns, projectRows } from './projection';
import { columnName, type ColumnSpec } from './formatter';
import './builtins';

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

async function run(
  argv: readonly string[],
  data: unknown,
  columns?: readonly ColumnSpec[],
): Promise<string> {
  const program = makeProgram(argv);
  const cap = captureStdout();
  try {
    await dispatch(program, data, columns ? { columns } : {});
  } finally {
    cap.restore();
  }
  return cap.out();
}

/**
 * Rows whose own key order (notes, name, id) deliberately disagrees with the
 * ColumnSpec order (id, name, notes) so the two rules are distinguishable.
 */
const rows = [
  { notes: 'a', name: 'Alice', id: '1' },
  { notes: 'b', name: 'Bob', id: '2' },
];

const cols: readonly ColumnSpec[] = [
  { header: 'id', key: 'id', priority: 9 },
  { header: 'name', key: 'name', priority: 8 },
  { header: 'notes', key: 'notes', priority: 2 },
];

// ---------------------------------------------------------------------------
// Rule 1 — ColumnSpec list drives default order, through dispatch.
// ---------------------------------------------------------------------------

describe('rule 1 — ColumnSpec drives default column order', () => {
  it('table uses ColumnSpec order, not payload key order', async () => {
    const out = await run([], rows, cols);
    expect(out.split('\n')[0]).toBe('id  name   notes');
  });

  it('csv uses ColumnSpec order, not payload key order', async () => {
    const out = await run(['--format', 'csv'], rows, cols);
    expect(out).toBe('id,name,notes\n1,Alice,a\n2,Bob,b\n');
  });

  it('json emits keys in ColumnSpec order', async () => {
    const out = await run(['--format', 'json'], rows, cols);
    const parsed = JSON.parse(out) as Array<Record<string, unknown>>;
    expect(Object.keys(parsed[0])).toEqual(['id', 'name', 'notes']);
  });

  it('yaml emits keys in ColumnSpec order', async () => {
    const out = await run(['--format', 'yaml'], rows, cols);
    expect(out.split('\n').slice(0, 4)).toEqual([
      '- id: \'1\'',
      '  name: Alice',
      '  notes: a',
      '- id: \'2\'',
    ]);
  });

  it('text/lines uses ColumnSpec order', async () => {
    const out = await run(
      ['--format', 'text', '--format-opt', 'style=lines'],
      rows,
      cols,
    );
    expect(out).toBe('1\tAlice\ta\n2\tBob\tb\n');
  });

  it('falls back to payload key order when no ColumnSpec supplied', async () => {
    const out = await run(['--format', 'csv'], rows);
    expect(out).toBe('notes,name,id\na,Alice,1\nb,Bob,2\n');
  });
});

// ---------------------------------------------------------------------------
// Rule 2 — --cols reorders as well as selects; user order wins.
// ---------------------------------------------------------------------------

describe('rule 2 — --cols reorders, user order wins', () => {
  it('table honors user order against ColumnSpec order', async () => {
    const out = await run(['--cols', 'notes,id'], rows, cols);
    expect(out.split('\n')[0]).toBe('notes  id');
  });

  it('csv honors user order against ColumnSpec order', async () => {
    const out = await run(['--format', 'csv', '--cols', 'notes,id'], rows, cols);
    expect(out).toBe('notes,id\na,1\nb,2\n');
  });

  it('json honors user order against ColumnSpec order', async () => {
    const out = await run(
      ['--format', 'json', '--cols', 'name,id'],
      rows,
      cols,
    );
    const parsed = JSON.parse(out) as Array<Record<string, unknown>>;
    expect(Object.keys(parsed[0])).toEqual(['name', 'id']);
  });

  it('honors user order on the no-ColumnSpec fallback path too', async () => {
    const out = await run(['--format', 'csv', '--cols', 'id,notes'], rows);
    expect(out).toBe('id,notes\n1,a\n2,b\n');
  });

  it('filterColumns returns selected order, not schema order', () => {
    expect(filterColumns(cols, ['notes', 'id']).map(c => c.header)).toEqual([
      'notes',
      'id',
    ]);
  });

  it('filterColumns rejects unknown names with the valid list', () => {
    expect(() => filterColumns(cols, ['bogus'])).toThrow(
      /unknown column "bogus" \(valid: id, name, notes\)/,
    );
  });
});

// ---------------------------------------------------------------------------
// Rule 3 — header == key.
// ---------------------------------------------------------------------------

describe('rule 3 — header == key', () => {
  it('deriveHeaders reads the ColumnSpec list in order', () => {
    expect(deriveHeaders(rows, cols)).toEqual(['id', 'name', 'notes']);
  });

  it('deriveHeaders falls back to first-row key order', () => {
    expect(deriveHeaders(rows)).toEqual(['notes', 'name', 'id']);
  });

  it('projectRows keys output by the column name itself', () => {
    const got = projectRows(rows, cols, []);
    expect(Object.keys(got[0])).toEqual(['id', 'name', 'notes']);
    expect(got[0]).toEqual({ id: '1', name: 'Alice', notes: 'a' });
  });

  it('projectRows applies user order when cols supplied', () => {
    const got = projectRows(rows, cols, ['notes', 'id']);
    expect(Object.keys(got[0])).toEqual(['notes', 'id']);
  });

  it('columnName returns the single effective name', () => {
    expect(columnName({ header: 'id', key: 'id' })).toBe('id');
  });

  it('columnName rejects a header/key split so drift stays impossible', () => {
    expect(() => columnName({ header: 'Name', key: 'name' })).toThrow(
      /column "Name": header and key must match \(got key "name"\)/,
    );
  });
});

// ---------------------------------------------------------------------------
// Rule 4 — zero rows emits nothing, decided by row count.
// ---------------------------------------------------------------------------

describe('rule 4 — zero rows emits nothing', () => {
  it('table emits nothing for an empty payload even with a ColumnSpec', async () => {
    expect(await run([], [], cols)).toBe('');
  });

  it('csv emits nothing for an empty payload even with a ColumnSpec', async () => {
    expect(await run(['--format', 'csv'], [], cols)).toBe('');
  });

  it('text emits nothing for an empty payload even with a ColumnSpec', async () => {
    expect(await run(['--format', 'text'], [], cols)).toBe('');
  });
});

// ---------------------------------------------------------------------------
// Rule 5 — priority accepted, stored, ignored.
// ---------------------------------------------------------------------------

describe('rule 5 — priority is accepted and ignored', () => {
  it('does not reorder or drop columns by priority', async () => {
    const noisy: readonly ColumnSpec[] = [
      { header: 'id', key: 'id', priority: 1 },
      { header: 'name', key: 'name', priority: 99 },
      { header: 'notes', key: 'notes', priority: 50 },
    ];
    const out = await run(['--format', 'csv'], rows, noisy);
    expect(out).toBe('id,name,notes\n1,Alice,a\n2,Bob,b\n');
  });
});
