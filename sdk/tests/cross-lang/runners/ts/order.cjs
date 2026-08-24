#!/usr/bin/env node
/**
 * Node runner for the cross-language column-ordering conformance harness.
 *
 * Reads fixtures/ordering.json, renders every case in every listed format
 * through the built CJS bundle, then RE-PARSES its own rendered bytes to
 * observe the column sequence the formatter actually serialized. Emits one
 * JSON object per case/format to KIT_CROSS_LANG_ORDER_OUT.
 *
 * Re-parsing rather than reporting the input is the point: it is the only way
 * to observe serialized key ORDER. Nothing here sorts keys, and no assertion
 * uses deep-equality on objects — key order is compared as an ordered array,
 * because `toEqual` on objects ignores key order and would be inert against
 * exactly the bugs this suite exists to catch.
 */

'use strict';

const fs = require('node:fs');
const path = require('node:path');

const SDK_TS = path.resolve(__dirname, '..', '..', '..', '..', 'ts');
const out = require(path.join(SDK_TS, 'dist', 'output.js'));

function seqFromTable(text) {
  const lines = text.split('\n').filter((l) => l.trim() !== '');
  if (lines.length === 0) return { sequence: [], empty: true };
  return { sequence: lines[0].trim().split(/\s+/), empty: false };
}

function seqFromCsv(text) {
  const lines = text.split('\n').filter((l) => l.trim() !== '');
  if (lines.length === 0) return { sequence: [], empty: true };
  return { sequence: lines[0].split(',').map((s) => s.trim()), empty: false };
}

function seqFromText(text) {
  const keys = [];
  for (const ln of text.split('\n')) {
    if (ln.trim() === '') break;
    const i = ln.indexOf('=');
    if (i < 0) continue;
    keys.push(ln.slice(0, i).trim());
  }
  return { sequence: keys, empty: keys.length === 0 };
}

function seqFromJson(text) {
  if (text.trim() === '') return { sequence: [], empty: true };
  // JSON.parse preserves insertion order for non-numeric-like keys, so
  // Object.keys reflects the serialized order faithfully.
  const doc = JSON.parse(text);
  if (Array.isArray(doc)) {
    if (doc.length === 0) return { sequence: [], empty: true };
    return { sequence: Object.keys(doc[0]), empty: false };
  }
  return { sequence: Object.keys(doc), empty: false };
}

/**
 * Minimal ordered YAML key reader. We deliberately do NOT use a YAML library
 * here: the only thing under test is the ORDER of the top-level mapping keys
 * of the first record, and scraping them off the raw text keeps the
 * observation closest to the emitted bytes.
 */
function seqFromYaml(text) {
  const lines = text.split('\n').filter((l) => l.trim() !== '');
  if (lines.length === 0) return { sequence: [], empty: true };
  if (lines.length === 1 && lines[0].trim() === '[]') {
    return { sequence: [], empty: true };
  }
  const keys = [];
  let baseIndent = null;
  for (const raw of lines) {
    let ln = raw;
    let indent = ln.length - ln.trimStart().length;
    ln = ln.trimStart();
    if (ln.startsWith('- ')) {
      // First record starts here; a second dash ends it.
      if (keys.length > 0) break;
      indent += 2;
      ln = ln.slice(2);
    } else if (ln === '-') {
      if (keys.length > 0) break;
      continue;
    }
    const m = /^([A-Za-z0-9_.-]+):/.exec(ln);
    if (!m) continue;
    if (baseIndent === null) baseIndent = indent;
    if (indent !== baseIndent) continue; // nested mapping, not a column
    keys.push(m[1]);
  }
  return { sequence: keys, empty: keys.length === 0 };
}

const EXTRACT = {
  table: seqFromTable,
  json: seqFromJson,
  yaml: seqFromYaml,
  csv: seqFromCsv,
  text: seqFromText,
};

function capture() {
  let buf = '';
  return {
    stream: {
      write(chunk) {
        buf += chunk;
        return true;
      },
    },
    get text() {
      return buf;
    },
  };
}

function main() {
  const fixtures = path.resolve(__dirname, '..', '..', 'fixtures');
  const doc = JSON.parse(fs.readFileSync(path.join(fixtures, 'ordering.json'), 'utf8'));
  const outPath = process.env.KIT_CROSS_LANG_ORDER_OUT;
  const records = [];

  for (const c of doc.cases) {
    const formats = c.formats === 'portable' ? doc.portable_formats : doc.extended_formats;
    const columns =
      c.spec === null ? undefined : c.spec.map((n) => ({ header: n, key: n }));
    for (const fmt of formats) {
      const f = out.defaultRegistry.lookup(fmt);
      if (!f) {
        records.push({ case: c.name, format: fmt, status: 'unsupported' });
        continue;
      }
      const cols = out.resolveEffectiveCols(c.cols, columns);
      const cap = capture();
      f.render(cap.stream, c.rows, out.parseOptions([], f.options), cols);
      const { sequence, empty } = EXTRACT[fmt](cap.text);
      records.push({ case: c.name, format: fmt, status: 'ok', sequence, empty });
    }
  }

  // Contract rule 3: a header != key ColumnSpec must not round-trip. ts
  // enforces via the read-time columnName guard rather than at construction,
  // since ColumnSpec is a structural interface with no constructor to hook.
  let rejected = false;
  try {
    out.columnName({ header: 'Name', key: 'name' });
  } catch {
    rejected = true;
  }
  records.push({ case: 'header-key-enforced', format: '-', status: 'ok', rejected });

  fs.writeFileSync(
    outPath,
    records.map((r) => JSON.stringify(r, Object.keys(r).sort())).join('\n') + '\n',
  );
}

main();
