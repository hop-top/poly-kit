// Runs the shared corpus in contracts/upgrade-v1. The vectors are the
// source of truth: a failure here means this implementation drifted from
// the contract, not that the vector should be edited to fit. The runner is
// strict like the Go and Rust ones: unknown fields, a `version` other than
// v1 and a corpus file nobody consumes all fail the run.

import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

import { describe, it, expect } from 'vitest';

import { DIR, strict, type Shape } from '../../test/upgrade/conformance-support';
import { MIGRATOR_SHAPE, runMigratorScenario, type MigratorFile } from '../../test/upgrade/conformance-migrator';

import {
  DocError,
  Registry,
  compareVersions,
  migrateDocument,
  migrateDocumentValue,
  type DocKind,
  type DocReport,
  type DocStep,
  type JsonObject,
} from './index';

// Files this runner consumes.
const CONSUMED = new Set(['versions.json', 'document.json', 'migrator.json']);
// Files it knows about but does not consume yet: listed by name with the
// reason, never silently ignored. Nothing is pending for TypeScript.
const PENDING: Record<string, string> = {};

// ─── strict loading ───────────────────────────────────────────────────

const HEADER: Shape = { $schema: true, $id: true, version: true, description: true };

function load<T>(name: string, shape: Shape): T {
  for (const f of readdirSync(DIR)) {
    if (f.endsWith('.json') && !CONSUMED.has(f) && !(f in PENDING)) {
      throw new Error(`${f} has no runner`);
    }
  }
  const raw = JSON.parse(readFileSync(join(DIR, name), 'utf8')) as { version?: string };
  strict(raw, { ...HEADER, ...shape }, name);
  if (raw.version !== 'v1') throw new Error(`${name}: version = ${raw.version}, want v1`);
  return raw as T;
}

// ─── corpus bookkeeping ───────────────────────────────────────────────

describe('upgrade-v1 corpus', () => {
  it('every .json file is consumed or listed as pending', () => {
    for (const f of readdirSync(DIR)) {
      if (!f.endsWith('.json')) continue;
      expect(CONSUMED.has(f) || f in PENDING, `${f} has no runner`).toBe(true);
    }
  });

  it('every consumed and pending file exists', () => {
    for (const f of [...CONSUMED, ...Object.keys(PENDING)]) {
      expect(existsSync(join(DIR, f)), `${f} is gone: drop it from the runner`).toBe(true);
    }
  });

  it('no file is both consumed and pending', () => {
    for (const f of Object.keys(PENDING)) expect(CONSUMED.has(f)).toBe(false);
  });
});

// ─── versions.json ────────────────────────────────────────────────────

interface VersionsFile {
  compare: { name: string; a: string; b: string; cmp: number }[];
  pending: {
    name: string;
    migrations: { schema: string; version: string }[];
    schema: string;
    from: string;
    to: string;
    pending: string[];
  }[];
}

const VERSIONS_SHAPE: Shape = {
  compare: [{ name: true, a: true, b: true, cmp: true }],
  pending: [
    {
      name: true,
      migrations: [{ schema: true, version: true }],
      schema: true,
      from: true,
      to: true,
      pending: [true],
    },
  ],
};

describe('upgrade-v1 versions.json', () => {
  const f = load<VersionsFile>('versions.json', VERSIONS_SHAPE);

  it('has vectors', () => {
    expect(f.compare.length).toBeGreaterThan(0);
    expect(f.pending.length).toBeGreaterThan(0);
  });

  describe('compare', () => {
    for (const c of f.compare) {
      it(c.name, () => {
        expect(compareVersions(c.a, c.b)).toBe(c.cmp);
        expect(compareVersions(c.b, c.a)).toBe(-c.cmp || 0);
      });
    }
  });

  describe('pending', () => {
    for (const c of f.pending) {
      it(c.name, () => {
        const reg = new Registry();
        for (const m of c.migrations) {
          reg.register({ schema: m.schema, version: m.version, up: () => {} });
        }
        expect(reg.pending(c.schema, c.from, c.to).map((m) => m.version)).toEqual(c.pending);
      });
    }
  });
});

// ─── document.json ────────────────────────────────────────────────────

interface ConfStep {
  from: number;
  to: number;
  note?: string;
  ops?: { set?: { path: string; value: unknown }; rename?: { from: string; to: string }; fail?: string }[];
  verify?: { require: string };
}

interface DocScenario {
  name: string;
  doc?: unknown;
  text?: string;
  options?: { version_field?: string; latest?: number; missing_is_zero?: boolean };
  steps: ConfStep[];
  expect: { doc?: unknown; text?: string; report?: DocReport; error?: string; kind?: DocKind };
}

const DOCUMENT_SHAPE: Shape = {
  scenarios: [
    {
      name: true,
      doc: true,
      text: true,
      options: { version_field: true, latest: true, missing_is_zero: true },
      steps: [
        {
          from: true,
          to: true,
          note: true,
          ops: [{ set: { path: true, value: true }, rename: { from: true, to: true }, fail: true }],
          verify: { require: true },
        },
      ],
      expect: {
        doc: true,
        text: true,
        report: { from: true, to: true, applied: [{ from: true, to: true, note: true }] },
        error: true,
        kind: true,
      },
    },
  ],
};

function lookup(doc: JsonObject, path: string): boolean {
  let cur: unknown = doc;
  for (const seg of path.split('.')) {
    if (cur === null || typeof cur !== 'object' || Array.isArray(cur)) return false;
    if (!Object.prototype.hasOwnProperty.call(cur, seg)) return false;
    cur = (cur as JsonObject)[seg];
  }
  return true;
}

function setPath(doc: JsonObject, path: string, v: unknown): void {
  const segs = path.split('.');
  let cur = doc;
  for (const seg of segs.slice(0, -1)) {
    let next = cur[seg];
    if (next === null || typeof next !== 'object' || Array.isArray(next)) {
      next = {};
      cur[seg] = next;
    }
    cur = next as JsonObject;
  }
  cur[segs[segs.length - 1]] = v as never;
}

function docSteps(cases: ConfStep[]): DocStep[] {
  return cases.map((c) => {
    const step: DocStep = {
      from: c.from,
      to: c.to,
      note: c.note,
      // In place, on purpose: the migrator must hand steps a copy.
      apply: (doc) => {
        for (const op of c.ops ?? []) {
          if (op.set) setPath(doc, op.set.path, op.set.value);
          else if (op.rename) {
            if (Object.prototype.hasOwnProperty.call(doc, op.rename.from)) {
              doc[op.rename.to] = doc[op.rename.from];
              delete doc[op.rename.from];
            }
          } else if (op.fail !== undefined && op.fail !== '') throw new Error(op.fail);
        }
        return doc;
      },
    };
    if (c.verify) {
      const req = c.verify.require;
      step.verify = (doc) => {
        if (!lookup(doc, req)) throw new Error('missing ' + req);
      };
    }
    return step;
  });
}

describe('upgrade-v1 document.json', () => {
  const f = load<{ scenarios: DocScenario[] }>('document.json', DOCUMENT_SHAPE);

  it('has scenarios', () => {
    expect(f.scenarios.length).toBeGreaterThan(0);
  });

  for (const sc of f.scenarios) {
    it(sc.name, () => {
      const opts = {
        versionField: sc.options?.version_field,
        latest: sc.options?.latest,
        missingIsZero: sc.options?.missing_is_zero,
      };
      const steps = docSteps(sc.steps);
      const input = sc.text !== undefined ? sc.text : JSON.stringify(sc.doc);

      const check = (via: string, run: () => { doc: unknown; report: DocReport }) => {
        if (sc.expect.error !== undefined) {
          let err: unknown;
          try {
            run();
          } catch (e) {
            err = e;
          }
          expect(err, `${via}: no error, want ${sc.expect.error}`).toBeDefined();
          expect((err as Error).message, via).toBe(sc.expect.error);
          expect(err, via).toBeInstanceOf(DocError);
          expect((err as DocError).kind, via).toBe(sc.expect.kind);
          return;
        }
        const got = run();
        if (sc.expect.doc !== undefined) expect(got.doc, via).toEqual(sc.expect.doc);
        if (sc.expect.report !== undefined) expect(got.report, via).toEqual(sc.expect.report);
      };

      // Bytes form, as text and as Uint8Array.
      const bytes = new TextEncoder().encode(input);
      const before = bytes.slice();
      check('bytes', () => {
        const r = migrateDocument(bytes, steps, opts);
        const text = new TextDecoder().decode(r.doc);
        if (sc.expect.text !== undefined) expect(text).toBe(sc.expect.text);
        return { doc: JSON.parse(text), report: r.report };
      });
      expect(bytes, 'input bytes changed').toEqual(before);
      check('text', () => {
        const r = migrateDocument(input, steps, opts);
        expect(typeof r.doc).toBe('string');
        return { doc: JSON.parse(r.doc), report: r.report };
      });

      // The value form, when the input is an object: same result, and the
      // caller's value is untouched however the steps mutate. Exact
      // literals (text) only survive the bytes form.
      if (sc.text === undefined && sc.doc !== null && typeof sc.doc === 'object' && !Array.isArray(sc.doc)) {
        const doc = sc.doc as JsonObject;
        const snapshot = JSON.stringify(doc);
        check('value', () => {
          const r = migrateDocumentValue(doc, steps, opts);
          return { doc: r.doc, report: r.report };
        });
        expect(JSON.stringify(doc), "caller's document changed").toBe(snapshot);
      }
    });
  }
});

// ─── migrator.json ────────────────────────────────────────────────────

describe('upgrade-v1 migrator.json', () => {
  const f = load<MigratorFile>('migrator.json', MIGRATOR_SHAPE);

  it('has scenarios', () => {
    expect(f.scenarios.length).toBeGreaterThan(0);
  });

  for (const sc of f.scenarios) {
    it(sc.name, async () => {
      await runMigratorScenario(sc);
    });
  }
});
