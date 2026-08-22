/**
 * Mirrors `go/core/netpolicy/netpolicy_test.go` and `install_test.go`.
 *
 * The semantics under test are the Go contract: block external egress when
 * the marker is set, exempt loopback, treat DNS names as remote, leave an
 * unmarked scope untouched, and fail with a matchable error rather than a
 * silent no-op.
 */

import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  ErrOffline,
  guardFetch,
  install,
  isOffline,
  isOfflineError,
  withOffline,
} from './netpolicy';

/** A fetch stand-in recording whether the wrapped implementation was reached. */
function recorder() {
  const state = { reached: false };
  const fn = vi.fn(async (_input: any, _init?: any) => {
    state.reached = true;
    return new Response(null, { status: 204 });
  });
  return { state, fn: fn as unknown as typeof globalThis.fetch };
}

describe('guardFetch', () => {
  // A marked scope must stop the request before it reaches the wire. The
  // destination is external: loopback is exempt by design.
  it('blocks external hosts when offline', async () => {
    const rec = recorder();
    const guarded = guardFetch(rec.fn);

    const err = await withOffline(true, async () => {
      try {
        await guarded('https://example.invalid/v1/thing');
        return null;
      } catch (e) {
        return e;
      }
    });

    expect(err, 'expected an offline error, got none').not.toBeNull();
    expect(isOfflineError(err)).toBe(true);
    expect(rec.state.reached, 'request reached the transport despite offline scope').toBe(false);
  });

  // Unmarked scopes must be entirely unaffected.
  it('allows requests when not offline', async () => {
    const rec = recorder();
    const guarded = guardFetch(rec.fn);

    const res = await guarded('https://example.invalid/v1/thing');
    expect(res.status).toBe(204);
    expect(rec.state.reached).toBe(true);
  });

  // Loopback stays reachable when offline: --offline means "no network",
  // not "cannot talk to myself".
  it('allows loopback when offline', async () => {
    for (const target of [
      'http://127.0.0.1:8080/health',
      'http://localhost:9000/health',
      'http://[::1]:9000/health',
    ]) {
      const rec = recorder();
      const guarded = guardFetch(rec.fn);

      await withOffline(true, async () => {
        const res = await guarded(target);
        expect(res.status, `${target}: unexpected status`).toBe(204);
      });
      expect(rec.state.reached, `${target}: loopback request was blocked`).toBe(true);
    }
  });

  // A DNS name is remote even if it might resolve to loopback: resolving
  // it is itself network access.
  it('blocks DNS names when offline', async () => {
    const rec = recorder();
    const guarded = guardFetch(rec.fn);

    const err = await withOffline(true, async () => {
      try {
        await guarded('http://my-host.internal/health');
        return null;
      } catch (e) {
        return e;
      }
    });

    expect(isOfflineError(err), `expected ErrOffline, got ${err}`).toBe(true);
    expect(rec.state.reached, 'DNS-named host was allowed through').toBe(false);
  });

  // Request objects carry the URL on `.url`, not as a bare string.
  it('reads the target from a Request object', async () => {
    const rec = recorder();
    const guarded = guardFetch(rec.fn);

    const err = await withOffline(true, async () => {
      try {
        await guarded(new Request('https://example.invalid/x'));
        return null;
      } catch (e) {
        return e;
      }
    });

    expect(isOfflineError(err)).toBe(true);
    expect(rec.state.reached).toBe(false);
  });

  // The refusal must be matchable and name the target, mirroring Go's
  // "%s %s: %w" wrapping of ErrOffline.
  it('rejects with a matchable, informative error', async () => {
    const guarded = guardFetch(recorder().fn);

    const err = await withOffline(true, async () => {
      try {
        await guarded('https://example.invalid/x', { method: 'POST' });
        return null;
      } catch (e) {
        return e;
      }
    });

    expect(err).toBeInstanceOf(Error);
    expect((err as Error).name).toBe(ErrOffline);
    expect((err as Error).message).toContain('POST');
    expect((err as Error).message).toContain('https://example.invalid/x');
    expect((err as Error).message).toContain('--offline');
  });

  // Wrapping must be idempotent, mirroring Go's Guard.
  it('does not double-wrap', () => {
    const once = guardFetch(recorder().fn);
    expect(guardFetch(once)).toBe(once);
  });
});

describe('withOffline / isOffline', () => {
  // withOffline(false) must not mark the scope.
  it('leaves the scope clean when false', async () => {
    expect(isOffline()).toBe(false);
    await withOffline(false, async () => {
      expect(isOffline()).toBe(false);
    });
    expect(isOffline()).toBe(false);
  });

  it('marks the scope when true, and unmarks it on exit', async () => {
    await withOffline(true, async () => {
      expect(isOffline()).toBe(true);
    });
    expect(isOffline()).toBe(false);
  });

  // The marker must survive an await boundary — otherwise a guard running
  // inside an async fetch implementation would not see it.
  it('survives await boundaries', async () => {
    await withOffline(true, async () => {
      await new Promise((r) => setTimeout(r, 1));
      expect(isOffline()).toBe(true);
    });
  });
});

describe('install', () => {
  const orig = globalThis.fetch;
  afterEach(() => {
    globalThis.fetch = orig;
  });

  // install must make a bare `fetch(...)` call — the common case across kit
  // and adopter code — enforce the policy, and must be idempotent.
  it('guards globalThis.fetch and is idempotent', async () => {
    const rec = recorder();
    globalThis.fetch = rec.fn;

    install();
    const afterFirst = globalThis.fetch;
    install();
    expect(globalThis.fetch, 'install double-wrapped globalThis.fetch').toBe(afterFirst);

    const err = await withOffline(true, async () => {
      try {
        await globalThis.fetch('https://example.invalid/x');
        return null;
      } catch (e) {
        return e;
      }
    });

    expect(isOfflineError(err), `global fetch not guarded after install: ${err}`).toBe(true);
    expect(rec.state.reached).toBe(false);
  });
});
