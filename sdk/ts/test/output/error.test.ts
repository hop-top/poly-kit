import { describe, it, expect } from 'vitest';
import * as yaml from 'js-yaml';
import {
  CODE_CONFLICT,
  CODE_GENERIC,
  CODE_NOT_FOUND,
  CODE_PROVENANCE_MISSING,
  CODE_RATE_LIMITED,
  CODE_TRANSIENT,
  CODE_UNAUTHORIZED,
  CODE_USAGE,
  EXIT_GENERIC,
  EXIT_PROVENANCE_MISSING,
  EXIT_RATE_LIMITED,
  EXIT_TRANSIENT,
  TRANSIENCE_PERMANENT,
  TRANSIENCE_TRANSIENT,
  TRANSIENCE_UNKNOWN,
  conflictError,
  genericError,
  notFoundError,
  provenanceMissingError,
  rateLimitedError,
  renderError,
  transienceForCode,
  transientError,
  unauthorizedError,
  unwrapError,
  usageError,
  withTransience,
  wrapError,
} from '../../src/output/error';
import type { CliError } from '../../src/output/error';

/** Collects renderError output into a string. */
function capture(): { write(chunk: string): void; text(): string } {
  let buf = '';
  return {
    write(chunk: string) {
      buf += chunk;
    },
    text: () => buf,
  };
}

describe('constructors — code/exit/transience', () => {
  const cases: Array<[string, CliError, string, number, string]> = [
    ['Generic', genericError('boom'), CODE_GENERIC, 1, TRANSIENCE_PERMANENT],
    ['NotFound', notFoundError('nope'), CODE_NOT_FOUND, 3, TRANSIENCE_PERMANENT],
    ['Conflict', conflictError('dup'), CODE_CONFLICT, 4, TRANSIENCE_PERMANENT],
    [
      'Unauthorized',
      unauthorizedError('nope'),
      CODE_UNAUTHORIZED,
      5,
      TRANSIENCE_PERMANENT,
    ],
    ['Usage', usageError('bad flag'), CODE_USAGE, 2, TRANSIENCE_PERMANENT],
    [
      'RateLimited',
      rateLimitedError('budget'),
      CODE_RATE_LIMITED,
      64,
      TRANSIENCE_TRANSIENT,
    ],
    [
      'Transient',
      transientError('upstream timeout'),
      CODE_TRANSIENT,
      6,
      TRANSIENCE_TRANSIENT,
    ],
    [
      'ProvenanceMissing',
      provenanceMissingError('/email'),
      CODE_PROVENANCE_MISSING,
      65,
      TRANSIENCE_PERMANENT,
    ],
  ];
  it.each(cases)('%s', (_name, got, wantCode, wantExit, wantTransience) => {
    expect(got.code).toBe(wantCode);
    expect(got.exit_code).toBe(wantExit);
    expect(got.transience).toBe(wantTransience);
  });

  it('exit-code table is unique, 1 generic / 6 transient / 65 provenance', () => {
    const exits = new Map(cases.map(([, e]) => [e.exit_code, e.code]));
    expect(exits.size).toBe(8);
    expect(EXIT_GENERIC).toBe(1);
    expect(EXIT_TRANSIENT).toBe(6);
    expect(EXIT_RATE_LIMITED).toBe(64);
    expect(EXIT_PROVENANCE_MISSING).toBe(65);
    expect(exits.get(1)).toBe(CODE_GENERIC);
    expect(exits.get(6)).toBe(CODE_TRANSIENT);
    expect(exits.get(65)).toBe(CODE_PROVENANCE_MISSING);
  });
});

describe('transienceForCode', () => {
  it.each([
    [CODE_USAGE, TRANSIENCE_PERMANENT],
    [CODE_NOT_FOUND, TRANSIENCE_PERMANENT],
    [CODE_CONFLICT, TRANSIENCE_PERMANENT],
    [CODE_UNAUTHORIZED, TRANSIENCE_PERMANENT],
    [CODE_PROVENANCE_MISSING, TRANSIENCE_PERMANENT],
    [CODE_RATE_LIMITED, TRANSIENCE_TRANSIENT],
    [CODE_TRANSIENT, TRANSIENCE_TRANSIENT],
    [CODE_GENERIC, TRANSIENCE_UNKNOWN],
    ['ADOPTER_SPECIFIC', TRANSIENCE_UNKNOWN],
    ['', TRANSIENCE_UNKNOWN],
  ])('%s -> %s', (code, want) => {
    expect(transienceForCode(code)).toBe(want);
  });
});

describe('wrapError', () => {
  it('defaults transience from code', () => {
    const base = new Error('boom');
    expect(wrapError(base, CODE_CONFLICT, 4)?.transience).toBe(
      TRANSIENCE_PERMANENT,
    );
    expect(wrapError(base, CODE_RATE_LIMITED, 64)?.transience).toBe(
      TRANSIENCE_TRANSIENT,
    );
    expect(wrapError(base, CODE_GENERIC, 1)?.transience).toBe(
      TRANSIENCE_UNKNOWN,
    );
  });

  it('retains the source error and passes null through', () => {
    const base = new Error('boom');
    const e = wrapError(base, CODE_CONFLICT, 4);
    expect(e?.message).toBe('boom');
    expect(unwrapError(e as CliError)).toBe(base);
    expect(wrapError(null, CODE_GENERIC, 1)).toBeNull();
  });
});

describe('withTransience', () => {
  it('copies and sets, never mutates the shared envelope', () => {
    const orig: CliError = { code: 'SHARED', message: 'm', exit_code: 9 };
    const got = withTransience(orig, TRANSIENCE_TRANSIENT);
    expect(got).not.toBe(orig);
    expect(got.transience).toBe(TRANSIENCE_TRANSIENT);
    expect(orig.transience).toBeUndefined();
    expect(got.code).toBe(orig.code);
    expect(got.message).toBe(orig.message);
    expect(got.exit_code).toBe(orig.exit_code);
  });

  it('carries the retained source error to the copy', () => {
    const base = new Error('boom');
    const e = wrapError(base, CODE_GENERIC, 1) as CliError;
    expect(unwrapError(withTransience(e, TRANSIENCE_PERMANENT))).toBe(base);
  });
});

describe('renderError — structured always carries transience', () => {
  it('normalizes unset transience to unknown in JSON', () => {
    const w = capture();
    renderError(w, 'json', { code: 'ADOPTER_SPECIFIC', message: 'm', exit_code: 9 });
    expect(JSON.parse(w.text()).transience).toBe(TRANSIENCE_UNKNOWN);
  });

  it('normalizes unset transience to unknown in YAML', () => {
    const w = capture();
    renderError(w, 'yaml', { code: 'ADOPTER_SPECIFIC', message: 'm', exit_code: 9 });
    expect(w.text()).toContain('transience: unknown');
  });

  it('renders an explicit class untouched', () => {
    const w = capture();
    renderError(w, 'json', rateLimitedError('budget'));
    expect(JSON.parse(w.text()).transience).toBe(TRANSIENCE_TRANSIENT);
  });

  it('does not mutate the input envelope', () => {
    const e: CliError = { code: 'ADOPTER_SPECIFIC', message: 'm', exit_code: 9 };
    renderError(capture(), 'json', e);
    expect(e.transience).toBeUndefined();
  });
});

describe('renderError — wire round-trip', () => {
  it('JSON: full envelope, empty optionals off the wire', () => {
    let w = capture();
    renderError(w, 'json', provenanceMissingError('/email'));
    const full = JSON.parse(w.text());
    expect(full.code).toBe(CODE_PROVENANCE_MISSING);
    expect(full.cause).toBe('/email');
    expect(full.exit_code).toBe(65);
    expect(full.transience).toBe(TRANSIENCE_PERMANENT);

    w = capture();
    renderError(w, 'json', transientError('upstream timeout'));
    const bare = JSON.parse(w.text());
    expect(Object.keys(bare).sort()).toEqual([
      'code',
      'exit_code',
      'message',
      'transience',
    ]);
    expect(bare.exit_code).toBe(6);
  });

  it('YAML: parses back to the wire shape', () => {
    const w = capture();
    renderError(w, 'yaml', transientError('upstream timeout'));
    expect(yaml.load(w.text())).toEqual({
      code: CODE_TRANSIENT,
      message: 'upstream timeout',
      exit_code: 6,
      transience: TRANSIENCE_TRANSIENT,
    });
  });
});

describe('renderError — plain text + null', () => {
  it('renders each populated field on its own line', () => {
    const w = capture();
    renderError(w, 'table', {
      code: 'NOT_FOUND',
      message: 'missing thing',
      cause: 'root',
      suggested_fix: 'try --all',
      alternatives: ['other'],
      exit_code: 3,
    });
    expect(w.text()).toBe(
      'NOT_FOUND: missing thing\nCause: root\nFix: try --all\nAlternative: other\n',
    );
  });

  it('renders bare message without code prefix', () => {
    const w = capture();
    renderError(w, '', { code: '', message: 'just text', exit_code: 1 });
    expect(w.text()).toBe('just text\n');
  });

  it('null renders nothing', () => {
    const w = capture();
    renderError(w, 'json', null);
    expect(w.text()).toBe('');
  });
});
