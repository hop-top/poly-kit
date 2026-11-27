/**
 * In-memory JSON document migrator, ported from Go `upgrade.MigrateDocument`.
 * A document is a JSON object with an integer version at `versionField`
 * (dotted path). Steps move it `from -> to`; the chain follows versions, not
 * the order of steps. No I/O; the caller's input is never modified.
 *
 * Same shape as Go with two TypeScript differences: JSON numbers are JS
 * numbers (integers past 2^53 lose exactness), and the re-encoded document
 * keeps key insertion order where Go sorts keys.
 */

/** A decoded JSON object. */
export type JsonObject = { [key: string]: unknown };

/**
 * Moves a document from version `from` to `to` (to > from). `apply` gets a
 * private copy and may mutate it in place; it returns the migrated document.
 * The version field is set to `to` after `apply`, so `apply` need not touch it.
 */
export interface DocStep {
  from: number;
  to: number;
  apply(doc: JsonObject): JsonObject;
  /** Optional; runs on `apply`'s result. Throw to fail the step. */
  verify?(doc: JsonObject): void;
  note?: string;
}

export interface DocOptions {
  /** Dotted path of the integer version; `version` when empty. */
  versionField?: string;
  /** The version this build writes; 0 or absent means the highest `to`. */
  latest?: number;
  /** Read a missing or non-integer version as 0 instead of failing. */
  missingIsZero?: boolean;
}

export interface DocApplied {
  from: number;
  to: number;
  note: string;
}

/** Says what the migration did. */
export interface DocReport {
  from: number;
  to: number;
  applied: DocApplied[];
}

/** Why a document migration failed. */
export type DocKind = 'newer' | 'version' | 'gap' | 'step' | 'verify' | 'invalid';

/** Every failure of the document migrator; `kind` says which. `cause` is the step's own error, when there is one. */
export class DocError extends Error {
  readonly kind: DocKind;
  constructor(kind: DocKind, message: string, cause?: unknown) {
    super(message, cause === undefined ? undefined : { cause });
    this.name = 'DocError';
    this.kind = kind;
  }
}

const isObject = (v: unknown): v is JsonObject =>
  v !== null && typeof v === 'object' && !Array.isArray(v);

const own = (o: object, k: string): boolean => Object.prototype.hasOwnProperty.call(o, k);

const errText = (e: unknown): string => (e instanceof Error ? e.message : String(e));

/** An integer literal fits Go's int: no fraction, no exponent. */
function isIntegerLiteral(src: string): boolean {
  return /^-?\d+$/.test(src) && Number.isSafeInteger(Number(src));
}

interface Parsed {
  doc: JsonObject;
  /** Source text of each number, by holder object and key. */
  literals: Map<object, Map<string, string>>;
}

type Reviver = (this: unknown, key: string, value: unknown, ctx?: { source?: string }) => unknown;

function decode(text: string): Parsed {
  const literals = new Map<object, Map<string, string>>();
  const reviver: Reviver = function (key, value, ctx) {
    if (typeof value === 'number' && ctx?.source !== undefined && this !== null && typeof this === 'object') {
      let m = literals.get(this);
      if (!m) literals.set(this, (m = new Map()));
      m.set(key, ctx.source);
    }
    return value;
  };
  let v: unknown;
  try {
    v = JSON.parse(text, reviver as Parameters<typeof JSON.parse>[1]);
  } catch (e) {
    throw new DocError('invalid', `parse document: ${errText(e)}`, e);
  }
  if (!isObject(v)) throw new DocError('invalid', 'document is not a JSON object');
  return { doc: v, literals };
}

/** The integer literal at `path`, if there is one. */
function docVersion(doc: JsonObject, path: string, literals?: Parsed['literals']): number | undefined {
  let holder: unknown = undefined;
  let cur: unknown = doc;
  let key = '';
  for (const seg of path.split('.')) {
    if (!isObject(cur) || !own(cur, seg)) return undefined;
    holder = cur;
    key = seg;
    cur = cur[seg];
  }
  if (typeof cur !== 'number') return undefined;
  const src = literals?.get(holder as object)?.get(key) ?? String(cur);
  return isIntegerLiteral(src) ? cur : undefined;
}

function setDocVersion(doc: JsonObject, path: string, v: number): void {
  const segs = path.split('.');
  let cur = doc;
  for (const seg of segs.slice(0, -1)) {
    let next = cur[seg];
    if (!isObject(next)) {
      next = {};
      cur[seg] = next;
    }
    cur = next as JsonObject;
  }
  cur[segs[segs.length - 1]] = v;
}

/** Runs the chain on `doc`, which it owns. */
function run(doc: JsonObject, steps: DocStep[], opts: DocOptions, literals?: Parsed['literals']): { doc: JsonObject; report: DocReport } {
  const field = opts.versionField || 'version';
  let cur = docVersion(doc, field, literals);
  if (cur === undefined) {
    if (!opts.missingIsZero) {
      throw new DocError('version', `document has no integer version at "${field}"`);
    }
    cur = 0;
  }
  let latest = opts.latest ?? 0;
  if (latest === 0) for (const s of steps) latest = Math.max(latest, s.to);
  const report: DocReport = { from: cur, to: cur, applied: [] };
  for (const s of steps) {
    if (s.to <= s.from) {
      throw new DocError('invalid', `migration ${s.from} -> ${s.to} does not move forward`);
    }
    if (s.to > latest) {
      throw new DocError('invalid', `migration ${s.from} -> ${s.to} goes past the latest known ${latest}`);
    }
  }
  if (cur > latest) {
    throw new DocError('newer', `document version ${cur} is newer than the latest known ${latest}`);
  }
  while (cur < latest) {
    const step = steps.find((s) => s.from === cur);
    if (!step) {
      throw new DocError('gap', `no migration from version ${cur} (highest target is ${latest}); chain has a gap`);
    }
    let out: JsonObject | null | undefined;
    try {
      out = step.apply(doc);
    } catch (e) {
      throw new DocError('step', `migration ${step.from} -> ${step.to}: ${errText(e)}`, e);
    }
    if (!isObject(out)) {
      throw new DocError('step', `migration ${step.from} -> ${step.to} returned nil map`);
    }
    if (step.verify) {
      try {
        step.verify(out);
      } catch (e) {
        throw new DocError('verify', `migration ${step.from} -> ${step.to}: verify: ${errText(e)}`, e);
      }
    }
    doc = out;
    setDocVersion(doc, field, step.to);
    cur = step.to;
    report.to = cur;
    report.applied.push({ from: step.from, to: step.to, note: step.note ?? '' });
  }
  return { doc, report };
}

/**
 * Migrates one JSON document given as text or UTF-8 bytes. When it is
 * already at the latest version the input is returned as it is, byte for
 * byte; otherwise the result is re-encoded. The result has the type of the
 * input. Throws {@link DocError}; the input is untouched either way.
 */
export function migrateDocument<T extends string | Uint8Array>(
  doc: T,
  steps: DocStep[],
  opts: DocOptions = {},
): { doc: T; report: DocReport } {
  let text: string;
  try {
    text = typeof doc === 'string' ? doc : new TextDecoder('utf-8', { fatal: true }).decode(doc);
  } catch (e) {
    throw new DocError('invalid', `parse document: ${errText(e)}`, e);
  }
  const parsed = decode(text);
  const { doc: out, report } = run(parsed.doc, steps, opts, parsed.literals);
  if (report.applied.length === 0) return { doc, report };
  const encoded = JSON.stringify(out);
  return { doc: (typeof doc === 'string' ? encoded : new TextEncoder().encode(encoded)) as T, report };
}

/**
 * {@link migrateDocument} on a decoded document. The caller's object is
 * never modified; steps work on a deep copy. Throws {@link DocError}.
 */
export function migrateDocumentValue(
  doc: JsonObject,
  steps: DocStep[],
  opts: DocOptions = {},
): { doc: JsonObject; report: DocReport } {
  let copy: JsonObject;
  try {
    copy = structuredClone(doc);
  } catch (e) {
    throw new DocError('invalid', `copy document: ${errText(e)}`, e);
  }
  if (!isObject(copy)) throw new DocError('invalid', 'document is not a JSON object');
  return run(copy, steps, opts);
}
