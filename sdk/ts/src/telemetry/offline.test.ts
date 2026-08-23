import { describe, expect, it, afterEach, vi } from 'vitest';

import {
  install,
  observabilityFetch,
  setProcessOffline,
  isOfflineError,
} from '../netpolicy';

/**
 * Telemetry is logging-class egress: `--offline` stops traffic the user
 * asked for, it is not a second consent gate on diagnostics. Consent and
 * telemetry mode already govern whether anything is emitted at all.
 *
 * Destinations here are deliberately remote. Loopback is exempt from the
 * guard by design, so a 127.0.0.1 target would pass with or without the
 * carve-out and could never fail.
 */
describe('telemetry exemption from --offline', () => {
  const REMOTE = 'https://telemetry.example.com/v1/events';

  afterEach(() => {
    setProcessOffline(false);
    vi.restoreAllMocks();
  });

  it('observability fetch reaches a remote endpoint while offline', async () => {
    const base = vi.fn(async () => new Response('', { status: 200 }));
    globalThis.fetch = base as unknown as typeof globalThis.fetch;

    // Re-import so the pristine reference is captured from the stub.
    vi.resetModules();
    const fresh = await import('../netpolicy');
    fresh.install();
    fresh.setProcessOffline(true);

    const res = await fresh.observabilityFetch()(REMOTE, { method: 'POST' });
    expect(res.status).toBe(200);
    expect(base).toHaveBeenCalledTimes(1);
  });

  it('keeps the carve-out narrow: ordinary fetch is still refused', async () => {
    const base = vi.fn(async () => new Response('', { status: 200 }));
    globalThis.fetch = base as unknown as typeof globalThis.fetch;

    install();
    setProcessOffline(true);

    let caught: unknown;
    try {
      await globalThis.fetch(REMOTE, { method: 'GET' });
    } catch (err) {
      caught = err;
    }
    expect(isOfflineError(caught)).toBe(true);
    expect(base).not.toHaveBeenCalled();
  });

  it('observability fetch is not the guarded global', () => {
    install();
    expect(observabilityFetch()).not.toBe(globalThis.fetch);
  });
});

