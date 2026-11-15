/**
 * `stopped` is a service's Stop returning, so on a canceled run the
 * events follow reverse start order however the cancellation and the
 * Start returns interleave with the supervisor's wait.
 */

import { describe, expect, it } from 'vitest';

import {
  ServiceRegistry,
  Supervisor,
  type EventPayload,
  type Publisher,
  type Service,
  type ServiceConfig,
} from './serve.js';

const enabled = (...names: string[]): Record<string, ServiceConfig> =>
  Object.fromEntries(names.map((n) => [n, { enabled: true }]));

const sleep = (ms: number): Promise<void> =>
  new Promise((r) => { const t = setTimeout(r, ms); t.unref?.(); });

const onAbort = (signal: AbortSignal): Promise<void> =>
  new Promise((r) => {
    if (signal.aborted) { r(); return; }
    signal.addEventListener('abort', () => r(), { once: true });
  });

/** Start returns as soon as the run is canceled. */
function onCancel(name: string, readyAfterMs = 0): Service {
  return {
    name,
    async start(signal, ready) {
      if (readyAfterMs > 0) await sleep(readyAfterMs);
      ready();
      await onAbort(signal);
    },
    ready: () => true,
    async stop() { /* nothing to release */ },
  };
}

/** Start returns only once its Stop was called: the listener shape. */
function untilStop(name: string, readyAfterMs = 0): Service {
  let release!: () => void;
  const released = new Promise<void>((r) => { release = r; });
  return {
    name,
    async start(_signal, ready) {
      if (readyAfterMs > 0) await sleep(readyAfterMs);
      ready();
      await released;
    },
    ready: () => true,
    async stop() { release(); },
  };
}

type Recorded = { topic: string; payload: EventPayload };

function recorder(
  hook?: (e: Recorded) => void,
): Publisher & { events: Recorded[]; stopped: () => string[] } {
  const events: Recorded[] = [];
  return {
    events,
    publish(e) {
      const rec = { topic: e.topic, payload: e.payload };
      events.push(rec);
      hook?.(rec);
    },
    stopped: () => events
      .filter((e) => e.topic === 'kit.serve.service.stopped')
      .map((e) => e.payload.service as string),
  };
}

const NAMES = ['alpha', 'beta', 'gamma', 'delta'];
const REVERSE = ['delta', 'gamma', 'beta', 'alpha'];

describe('serve stopped order on a canceled run', () => {
  it('cancel at the aggregate-ready publish keeps reverse order', async () => {
    for (let i = 0; i < 50; i++) {
      const ac = new AbortController();
      const pub = recorder((e) => {
        if (e.topic === 'kit.serve.supervisor.ready_reported') ac.abort();
      });
      const sup = new Supervisor(
        registryOf(onCancel('alpha'), untilStop('beta'), onCancel('gamma'), untilStop('delta')),
        { publisher: pub },
      );
      const res = await sup.run(ac.signal, NAMES, enabled(...NAMES));
      expect(res.error).toBeUndefined();
      expect(pub.stopped()).toEqual(REVERSE);
    }
  });

  it('cancel while the last service is still starting keeps reverse order', async () => {
    for (let i = 0; i < 20; i++) {
      const ac = new AbortController();
      const pub = recorder((e) => {
        if (e.topic === 'kit.serve.service.ready_reported' && e.payload.service === 'gamma') {
          ac.abort();
        }
      });
      const sup = new Supervisor(
        registryOf(onCancel('alpha'), untilStop('beta'), onCancel('gamma'), untilStop('delta', 20)),
        { publisher: pub },
      );
      const res = await sup.run(ac.signal, NAMES, enabled(...NAMES));
      expect(res.error).toBeUndefined();
      expect(pub.stopped()).toEqual(REVERSE);
    }
  });

  it('cancel after steady state keeps reverse order', async () => {
    for (let i = 0; i < 50; i++) {
      const ac = new AbortController();
      const pub = recorder();
      const sup = new Supervisor(
        registryOf(onCancel('alpha'), untilStop('beta'), onCancel('gamma'), untilStop('delta')),
        { publisher: pub },
      );
      const run = sup.run(ac.signal, NAMES, enabled(...NAMES));
      await sleep(5);
      ac.abort();
      await run;
      expect(pub.stopped()).toEqual(REVERSE);
    }
  });

  it('an early self-exit still reports stopped once, at exit time', async () => {
    const quitter: Service = {
      name: 'quitter',
      async start(_signal, ready) { ready(); await sleep(10); },
      ready: () => true,
      async stop() { /* nothing to release */ },
    };
    const ac = new AbortController();
    const pub = recorder();
    const sup = new Supervisor(registryOf(onCancel('steady'), quitter), { publisher: pub });
    const run = sup.run(ac.signal, ['steady', 'quitter'], enabled('steady', 'quitter'));
    for (let i = 0; i < 200 && !pub.stopped().includes('quitter'); i++) await sleep(5);
    expect(pub.stopped()).toEqual(['quitter']);
    ac.abort();
    await run;
    expect(pub.stopped()).toEqual(['quitter', 'steady']);
  });
});

function registryOf(...svcs: Service[]): ServiceRegistry {
  const r = new ServiceRegistry();
  for (const s of svcs) r.register(s);
  return r;
}
