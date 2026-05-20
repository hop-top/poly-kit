// Cross-language parity contract test (tlc T-0753).
//
// Loads contracts/typeid-v1/fixtures.json from the repo root and
// asserts that this TS SDK's encode (via typeid-js's TypeID.fromUUID)
// and decode (via the kit's parse) agree with the canonical wire form
// shared by go/rs/py/php. A divergence here means either typeid-js
// drifted or the contract was edited without updating all five SDKs.

import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, it, expect } from 'vitest';
import { TypeID } from 'typeid-js';

import { parse } from './index';

interface ContractVector {
  readonly name: string;
  readonly prefix: string;
  readonly uuid: string;
  readonly typeid: string;
  readonly skip_in?: ReadonlyArray<string>;
  readonly note?: string;
}

interface ContractFile {
  readonly version: string;
  readonly spec: string;
  readonly vectors: ReadonlyArray<ContractVector>;
}

// Walk up from this test file's directory until we hit a directory
// holding contracts/typeid-v1/fixtures.json. Robust against both
// `vitest run` (CWD = sdk/ts) and IDE-driven invocations whose CWD
// may vary.
function locateContract(): string {
  const here = dirname(fileURLToPath(import.meta.url));
  let dir = here;
  for (let i = 0; i < 10; i++) {
    const candidate = resolve(dir, 'contracts', 'typeid-v1', 'fixtures.json');
    if (existsSync(candidate)) return candidate;
    const parent = dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error(
    `contracts/typeid-v1/fixtures.json: not found walking up from ${here}`,
  );
}

const CONTRACT: ContractFile = JSON.parse(
  readFileSync(locateContract(), 'utf8'),
) as ContractFile;

describe('typeid-v1 contract metadata', () => {
  it('pins version + spec', () => {
    expect(CONTRACT.version).toBe('v1');
    expect(CONTRACT.spec).toBe('jetify-typeid-v0.3');
    expect(CONTRACT.vectors.length).toBeGreaterThan(0);
  });
});

describe('typeid-v1 contract generation (TS)', () => {
  for (const v of CONTRACT.vectors) {
    const skipped = v.skip_in?.includes('ts') ?? false;
    const fn = skipped ? it.skip : it;
    fn(`vector ${v.name}: TypeID.fromUUID produces the pinned canonical`, () => {
      const got = TypeID.fromUUID(v.prefix, v.uuid).toString();
      expect(got).toBe(v.typeid);
    });
  }
});

describe('typeid-v1 contract parse (TS)', () => {
  for (const v of CONTRACT.vectors) {
    const skipped = v.skip_in?.includes('ts') ?? false;
    const fn = skipped ? it.skip : it;
    fn(`vector ${v.name}: parse round-trips to (prefix, uuid)`, () => {
      const parsed = parse(v.typeid);
      expect(parsed.prefix).toBe(v.prefix);
      expect(parsed.uuid).toBe(v.uuid);
    });
  }
});
