/**
 * Tests for scope.ts — TS port parity with hop.top/kit/go/core/scope.
 *
 * Parity strategy:
 *
 * - Load the canonical contracts/parity/scope-defaults.json directly and
 *   compare it byte-for-byte (post JSON parse) with the local copy at
 *   sdk/ts/src/scope-defaults.json. If they drift, the test fails — keeping
 *   ports honest about syncing the contract.
 * - SecretPaths() is verified to be (common ∪ platform-specific) on the
 *   current host, with `~` left unexpanded (the Go port also leaves it for
 *   late expansion at match time).
 */

import * as fs from 'fs';
import * as path from 'path';
import { describe, expect, it, afterEach } from 'vitest';

import {
  Decision,
  Default,
  ErrDenied,
  Mode,
  New,
  Op,
  Policy,
  SecretPaths,
  setDefault,
} from './scope';

// ─── Contract sync ───────────────────────────────────────────────────────────

describe('scope contract parity', () => {
  it('local scope-defaults.json matches contracts/parity/scope-defaults.json', () => {
    const local = JSON.parse(
      fs.readFileSync(path.join(__dirname, 'scope-defaults.json'), 'utf8'),
    );
    const canonical = JSON.parse(
      fs.readFileSync(
        path.join(__dirname, '..', '..', '..', 'contracts', 'parity', 'scope-defaults.json'),
        'utf8',
      ),
    );
    expect(local).toEqual(canonical);
  });
});

// ─── SecretPaths ────────────────────────────────────────────────────────────

describe('SecretPaths', () => {
  it('returns a non-empty pattern set', () => {
    const sp = SecretPaths();
    expect(sp.length).toBeGreaterThan(0);
  });

  it('includes common patterns (~/.ssh/**)', () => {
    expect(SecretPaths()).toContain('~/.ssh/**');
  });

  it('includes platform-specific patterns', () => {
    const sp = SecretPaths();
    if (process.platform === 'darwin') {
      expect(sp).toContain('~/Library/Keychains/**');
    } else if (process.platform === 'linux') {
      expect(sp).toContain('~/.mozilla/firefox/**/cookies.sqlite');
    }
  });
});

// ─── Policy: Allow / Deny / Check ───────────────────────────────────────────

describe('Policy.check', () => {
  it('returns Unknown for an empty policy', () => {
    const p = New();
    expect(p.check('/tmp/anything', Op.Read)).toBe(Decision.Unknown);
  });

  it('Allow + matching path → Allowed', () => {
    const p = New().allow('/tmp/**');
    expect(p.check('/tmp/foo.txt', Op.Read)).toBe(Decision.Allowed);
  });

  it('Deny + matching path → Denied (deny wins)', () => {
    const p = New().allow('/tmp/**').deny('/tmp/sensitive/**');
    expect(p.check('/tmp/sensitive/x', Op.Read)).toBe(Decision.Denied);
  });

  it('Op bitset: Read-only rule does not match Write', () => {
    const p = New().allowOp(Op.Read, '/tmp/**');
    expect(p.check('/tmp/x', Op.Read)).toBe(Decision.Allowed);
    expect(p.check('/tmp/x', Op.Write)).toBe(Decision.Unknown);
  });
});

// ─── Policy.enforce ─────────────────────────────────────────────────────────

describe('Policy.enforce', () => {
  it('Strict + Unknown → throws ErrDenied', () => {
    const p = New();
    expect(() => p.enforce('/tmp/x', Op.Read)).toThrow(ErrDenied);
  });

  it('Strict + Allowed → no throw', () => {
    const p = New().allow('/tmp/**');
    expect(() => p.enforce('/tmp/x', Op.Read)).not.toThrow();
  });

  it('Warn + Denied → logs, no throw', () => {
    const p = New().setMode(Mode.Warn).deny('/tmp/x');
    expect(() => p.enforce('/tmp/x', Op.Read)).not.toThrow();
  });

  it('Prompt + Denied + cb=true → no throw', () => {
    const p = New()
      .setMode(Mode.Prompt)
      .deny('/tmp/x')
      .setPromptFunc(() => true);
    expect(() => p.enforce('/tmp/x', Op.Read)).not.toThrow();
  });

  it('Prompt + Denied + cb=false → throws', () => {
    const p = New()
      .setMode(Mode.Prompt)
      .deny('/tmp/x')
      .setPromptFunc(() => false);
    expect(() => p.enforce('/tmp/x', Op.Read)).toThrow(ErrDenied);
  });
});

// ─── Snapshot / SetDefault ───────────────────────────────────────────────────

describe('Policy.snapshot + setDefault', () => {
  let restore: (() => void) | null = null;
  afterEach(() => {
    if (restore) {
      restore();
      restore = null;
    }
  });

  it('snapshot is independent of original', () => {
    const orig = New().allow('/tmp/**');
    const cp = orig.snapshot();
    cp.deny('/tmp/x');
    expect(orig.check('/tmp/x', Op.Read)).toBe(Decision.Allowed);
    expect(cp.check('/tmp/x', Op.Read)).toBe(Decision.Denied);
  });

  it('setDefault swaps singleton and restore puts it back', () => {
    const before = Default();
    const swap = New().allow('/tmp/swap/**');
    restore = setDefault(swap);
    expect(Default()).toBe(swap);
    restore();
    restore = null;
    expect(Default()).toBe(before);
  });
});

// ─── Default singleton seeded with SecretPaths ──────────────────────────────

describe('Default()', () => {
  let restore: (() => void) | null = null;
  afterEach(() => {
    if (restore) {
      restore();
      restore = null;
    }
  });

  it('denies a known secret path on the current platform', () => {
    // ~ expands to homedir at match time; pick a path that matches **/.env
    // (in the common deny set on every platform).
    const policy: Policy = Default();
    expect(policy.check('/tmp/whatever/.env', Op.Read)).toBe(Decision.Denied);
  });
});
