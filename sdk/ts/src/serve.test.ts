/**
 * Tests for the serve hierarchy and service lifecycle.
 *
 * The contract these pin is `docs/contracts/serve-lifecycle.md`,
 * §"Cross-language parity". Where a value is cross-language (a topic
 * string, an exit code, the name grammar) it is asserted as a literal
 * rather than against the implementation's own constant, so a change to
 * the constant does not silently take the assertion with it.
 */

import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import { Command } from 'commander';
import { execFileSync, spawn } from 'node:child_process';
import { rmSync, writeFileSync } from 'node:fs';
import * as path from 'node:path';

import {
  DEFAULT_FAILURE_POLICY,
  DEFAULT_READY_TIMEOUT_MS,
  DEFAULT_SHUTDOWN_TIMEOUT_MS,
  DEFAULT_STOP_TIMEOUT_MS,
  NAME_PATTERN,
  RESERVED_NAMES,
  ServiceRegistrationError,
  LoopKeepAlive,
  ServiceRegistry,
  Supervisor,
  codeFor,
  defaultTopics,
  exitCodeFor,
  isFailure,
  isReservedName,
  applyEnableDisable,
  applyTimeoutOverrides,
  parseDurationMs,
  registerServe,
  resolve,
  signalController,
  startOrder,
  validateName,
  worstOutcome,
  type EventPayload,
  type LifecycleOutcome,
  type Publisher,
  type Service,
  type ServiceConfig,
} from './serve.js';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

interface FakeOptions {
  /** Reject from start with this message instead of serving. */
  failStartWith?: string;
  /** Never call ready(), so the readiness budget decides. */
  neverReady?: boolean;
  /** Return from start (cleanly) after this many ms rather than on abort. */
  returnAfterMs?: number;
  /** Reject from start after readiness — a runtime crash. */
  crashAfterMs?: string;
  /** Hang in stop rather than resolving, so the stop budget decides. */
  hangInStop?: boolean;
  /** Reject from stop with this message. */
  failStopWith?: string;
  validate?: () => string | null;
  dependsOn?: string[];
  addr?: string;
  cls?: [string, string];
}

/** A service whose whole lifecycle is scriptable from the test. */
function fakeService(name: string, o: FakeOptions = {}): Service & {
  startCount: number; stopCount: number; stopOrder: number[];
} {
  const state = { ready: false, startCount: 0, stopCount: 0, stopOrder: [] as number[] };
  const svc: Service = {
    name,
    async start(signal, ready) {
      state.startCount++;
      if (o.failStartWith) throw new Error(o.failStartWith);
      if (!o.neverReady) {
        state.ready = true;
        ready();
      }
      if (o.returnAfterMs !== undefined) {
        await sleep(o.returnAfterMs);
        return;
      }
      if (o.crashAfterMs !== undefined) {
        await sleep(1);
        throw new Error(o.crashAfterMs);
      }
      await abortPromise(signal);
    },
    ready: () => state.ready,
    async stop() {
      state.stopCount++;
      state.stopOrder.push(stopSequence++);
      if (o.failStopWith) throw new Error(o.failStopWith);
      if (o.hangInStop) await new Promise<void>(() => { /* never settles */ });
      state.ready = false;
    },
  };
  if (o.validate) svc.validate = o.validate;
  if (o.dependsOn) svc.dependsOn = () => o.dependsOn!;
  if (o.addr !== undefined) svc.addr = () => o.addr!;
  if (o.cls) svc.class = () => o.cls!;
  // Defined as accessors, not Object.assign: assign copies a getter's
  // *value* at call time, which would freeze every counter at 0.
  return Object.defineProperties(svc, {
    startCount: { get: () => state.startCount, enumerable: true },
    stopCount: { get: () => state.stopCount, enumerable: true },
    stopOrder: { get: () => state.stopOrder, enumerable: true },
  }) as Service & { startCount: number; stopCount: number; stopOrder: number[] };
}

let stopSequence = 0;

const sleep = (ms: number): Promise<void> =>
  new Promise((r) => { const t = setTimeout(r, ms); t.unref?.(); });

const abortPromise = (signal: AbortSignal): Promise<void> =>
  new Promise((r) => {
    if (signal.aborted) { r(); return; }
    signal.addEventListener('abort', () => r(), { once: true });
  });

/** A registry pre-loaded with the named services. */
function registryOf(...svcs: Service[]): ServiceRegistry {
  const r = new ServiceRegistry();
  for (const s of svcs) r.register(s);
  return r;
}

/** Collects every published event so a test can assert on the trace. */
function recorder(): Publisher & { events: Array<{ topic: string; payload: EventPayload }> } {
  const events: Array<{ topic: string; payload: EventPayload }> = [];
  return {
    events,
    publish(e) { events.push({ topic: e.topic, payload: e.payload }); },
  };
}

/** A run signal that aborts after `ms`, standing in for SIGTERM. */
function abortAfter(ms: number): AbortSignal {
  const ac = new AbortController();
  const t = setTimeout(() => ac.abort(), ms);
  t.unref?.();
  return ac.signal;
}

const enabled = (...names: string[]): Record<string, ServiceConfig> =>
  Object.fromEntries(names.map((n) => [n, { enabled: true }]));

// ---------------------------------------------------------------------------
// Naming
// ---------------------------------------------------------------------------

describe('service names', () => {
  it('accepts the contract grammar', () => {
    for (const name of ['api', 'socket', 'a', 'a1', 'a-b', 'mcp-stdio']) {
      expect(NAME_PATTERN.test(name), name).toBe(true);
      expect(validateName(name), name).toBeNull();
    }
  });

  it('rejects anything outside it', () => {
    for (const name of ['', 'API', '1api', '-api', 'api_v2', 'api.v2', 'a b']) {
      expect(validateName(name), name).not.toBeNull();
    }
  });

  it('reserves exactly all, none and list', () => {
    expect([...RESERVED_NAMES].sort()).toEqual(['all', 'list', 'none']);
    for (const n of ['all', 'none', 'list']) {
      expect(isReservedName(n)).toBe(true);
      // A reserved word matches the grammar but is still refused: the
      // two rules are independent.
      expect(NAME_PATTERN.test(n)).toBe(true);
      expect(validateName(n)).toContain('reserved');
    }
    expect(isReservedName('api')).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// Registration seam
// ---------------------------------------------------------------------------

describe('ServiceRegistry', () => {
  it('lists in registration order', () => {
    const r = registryOf(fakeService('zeta'), fakeService('alpha'), fakeService('mid'));
    expect(r.names()).toEqual(['zeta', 'alpha', 'mid']);
    expect(r.list().map((s) => s.name)).toEqual(['zeta', 'alpha', 'mid']);
    expect(r.size).toBe(3);
  });

  it('refuses a duplicate name at registration', () => {
    const r = registryOf(fakeService('api'));
    expect(() => r.register(fakeService('api'))).toThrow(ServiceRegistrationError);
    // Last-writer-wins is forbidden: the first registration survives.
    expect(r.size).toBe(1);
  });

  it('refuses an invalid or reserved name', () => {
    const r = new ServiceRegistry();
    expect(() => r.register(fakeService('API'))).toThrow(ServiceRegistrationError);
    expect(() => r.register(fakeService('list'))).toThrow(/reserved/);
  });

  it('override replaces in place and keeps position', () => {
    const r = registryOf(fakeService('a'), fakeService('b'), fakeService('c'));
    const replacement = fakeService('b', { addr: ':9999' });
    r.override(replacement);
    expect(r.names()).toEqual(['a', 'b', 'c']);
    expect(r.lookup('b')!.addr!()).toBe(':9999');
  });

  it('override still refuses an invalid name', () => {
    const r = new ServiceRegistry();
    expect(() => r.override(fakeService('Bad'))).toThrow(ServiceRegistrationError);
  });
});

// ---------------------------------------------------------------------------
// The hierarchy
// ---------------------------------------------------------------------------

describe('resolve — the hierarchy', () => {
  it('supervisor form selects every configured and enabled service', () => {
    const r = registryOf(fakeService('api'), fakeService('socket'), fakeService('bus'));
    const out = resolve(r, {
      args: [],
      configs: { api: { enabled: true }, socket: { enabled: false }, bus: { enabled: true } },
    });
    expect(out.error).toBeUndefined();
    expect(out.explicit).toBe(false);
    expect(out.selected).toEqual(['api', 'bus']);
  });

  it('skips a disabled service silently rather than failing', () => {
    const r = registryOf(fakeService('api'), fakeService('socket'));
    const out = resolve(r, {
      args: [], configs: { api: { enabled: true }, socket: { enabled: false } },
    });
    expect(out.skipped).toEqual(['socket']);
    expect(out.error).toBeUndefined();
  });

  it('ignores a service with no config block at all', () => {
    const r = registryOf(fakeService('api'), fakeService('ghost'));
    const out = resolve(r, { args: [], configs: { api: { enabled: true } } });
    expect(out.selected).toEqual(['api']);
    // Unconfigured is not the same as disabled: it is not even skipped.
    expect(out.skipped).toEqual([]);
  });

  it('selector form selects exactly the named service', () => {
    const r = registryOf(fakeService('api'), fakeService('socket'));
    const out = resolve(r, { args: ['socket'], configs: enabled('api', 'socket') });
    expect(out.selected).toEqual(['socket']);
    expect(out.explicit).toBe(true);
  });

  it('selection preserves registration order, not argument order', () => {
    const r = registryOf(fakeService('zeta'), fakeService('alpha'));
    const out = resolve(r, { args: [], configs: enabled('zeta', 'alpha') });
    expect(out.selected).toEqual(['zeta', 'alpha']);
  });
});

// ---------------------------------------------------------------------------
// The override rule
// ---------------------------------------------------------------------------

describe('resolve — the override rule', () => {
  it('starts a disabled service when it is named explicitly', () => {
    const r = registryOf(fakeService('api'));
    const out = resolve(r, { args: ['api'], configs: { api: { enabled: false } } });
    expect(out.error).toBeUndefined();
    expect(out.selected).toEqual(['api']);
  });

  it('starts a service with no config block at all when named', () => {
    // Enablement answers "does the supervisor start this by default";
    // it is not an authorization decision, and an absent block is not
    // a refusal under the selector form.
    const r = registryOf(fakeService('api'));
    const out = resolve(r, { args: ['api'], configs: {} });
    expect(out.error).toBeUndefined();
    expect(out.selected).toEqual(['api']);
  });

  it('the same service is refused under the supervisor form', () => {
    // The contrast is the whole point of the rule: identical config,
    // opposite answers, decided only by the explicit naming.
    const r = registryOf(fakeService('api'));
    const supervisor = resolve(r, { args: [], configs: { api: { enabled: false } } });
    expect(supervisor.error?.exit_code).toBe(2);
    const selector = resolve(r, { args: ['api'], configs: { api: { enabled: false } } });
    expect(selector.error).toBeUndefined();
  });

  it('does NOT override the configuration gate', () => {
    const r = registryOf(fakeService('api', { validate: () => 'addr: missing' }));
    const out = resolve(r, { args: ['api'], configs: { api: { enabled: false } } });
    expect(out.error?.code).toBe('USAGE');
    expect(out.error?.exit_code).toBe(2);
    expect(out.error?.message).toBe('service "api": addr: missing');
  });

  it('does NOT override the policy gate', () => {
    const r = registryOf(fakeService('api', { cls: ['write-shared', 'ingress'] }));
    const out = resolve(r, {
      args: ['api'],
      policy: { allow: () => ({ ok: false, reason: 'ingress denied' }) },
    });
    expect(out.error?.code).toBe('UNAUTHORIZED');
    expect(out.error?.exit_code).toBe(5);
    expect(out.error?.message).toContain('side_effect=write-shared, network=ingress');
    expect(out.error?.message).toContain('ingress denied');
  });

  it('evaluates the gates in order: registration, config, policy', () => {
    // A service that is both unregistered and would fail policy must
    // answer NOT_FOUND, not UNAUTHORIZED — the operator's next step
    // differs.
    const r = registryOf(fakeService('api', { validate: () => 'broken' }));
    const denyAll = { allow: () => ({ ok: false }) };

    expect(resolve(r, { args: ['ghost'], policy: denyAll }).error?.code).toBe('NOT_FOUND');
    // Registered but invalid config beats the policy gate.
    expect(resolve(r, { args: ['api'], policy: denyAll }).error?.code).toBe('USAGE');
  });

  it('passes an unclassified service through the policy gate', () => {
    const r = registryOf(fakeService('api'));
    const out = resolve(r, { args: ['api'], policy: { allow: () => ({ ok: false }) } });
    expect(out.error).toBeUndefined();
  });

  it('passes every service when no gate is wired', () => {
    const r = registryOf(fakeService('api', { cls: ['destructive', 'ingress'] }));
    expect(resolve(r, { args: ['api'] }).error).toBeUndefined();
  });
});

// ---------------------------------------------------------------------------
// Invalid selection
// ---------------------------------------------------------------------------

describe('resolve — invalid selection', () => {
  it('refuses two or more positional arguments as USAGE/2', () => {
    const r = registryOf(fakeService('api'), fakeService('socket'));
    const out = resolve(r, { args: ['api', 'socket'], configs: enabled('api', 'socket') });
    expect(out.error?.code).toBe('USAGE');
    expect(out.error?.exit_code).toBe(2);
    expect(out.error?.message).toBe('serve accepts at most one service name, got 2');
    expect(out.selected).toEqual([]);
  });

  it('refuses an unknown service as NOT_FOUND/3, naming the known set', () => {
    const r = registryOf(fakeService('api'), fakeService('socket'));
    const out = resolve(r, { args: ['ghost'] });
    expect(out.error?.code).toBe('NOT_FOUND');
    expect(out.error?.exit_code).toBe(3);
    expect(out.error?.message).toBe('unknown service "ghost"; known: api, socket');
  });

  it('suggests the nearest name on a near miss', () => {
    const r = registryOf(fakeService('socket'));
    expect(resolve(r, { args: ['sockat'] }).error?.suggested_fix).toBe('did you mean "socket"?');
  });

  it('suggests nothing when no name is close', () => {
    const r = registryOf(fakeService('socket'));
    expect(resolve(r, { args: ['zzzzzzzz'] }).error?.suggested_fix).toBeUndefined();
  });

  it('refuses a reserved word as a selection', () => {
    // `list` can never be registered, so naming it can only ever be
    // NOT_FOUND — which is what makes --list a flag rather than a child.
    const r = registryOf(fakeService('api'));
    expect(resolve(r, { args: ['list'] }).error?.code).toBe('NOT_FOUND');
  });

  it('refuses zero resolved services under the supervisor form', () => {
    // Exiting 0 without listening is indistinguishable from a
    // successful start to systemd or a container runtime.
    const r = registryOf(fakeService('api'));
    const out = resolve(r, { args: [], configs: { api: { enabled: false } } });
    expect(out.error?.code).toBe('USAGE');
    expect(out.error?.exit_code).toBe(2);
    expect(out.error?.suggested_fix).toContain('services.<name>.enabled: true');
  });

  it('refuses an empty registry under the supervisor form', () => {
    expect(resolve(new ServiceRegistry(), { args: [] }).error?.exit_code).toBe(2);
  });
});

// ---------------------------------------------------------------------------
// Exit codes
// ---------------------------------------------------------------------------

describe('exit-code taxonomy', () => {
  it('maps every outcome onto the contract table', () => {
    // Literals, not the module's own constants: this is the table the
    // sibling ports implement against.
    const table: Array<[LifecycleOutcome, string, number]> = [
      ['clean-stop', 'OK', 0],
      ['invalid-selection', 'USAGE', 2],
      ['config-invalid', 'USAGE', 2],
      ['no-services', 'USAGE', 2],
      ['unknown-service', 'NOT_FOUND', 3],
      ['policy-denied', 'UNAUTHORIZED', 5],
      ['start-failed', 'GENERIC', 1],
      ['runtime-crash', 'GENERIC', 1],
      ['shutdown-timeout', 'GENERIC', 1],
    ];
    for (const [outcome, code, exit] of table) {
      expect(codeFor(outcome), outcome).toBe(code);
      expect(exitCodeFor(outcome), outcome).toBe(exit);
    }
  });

  it('treats a clean stop as success and everything else as failure', () => {
    expect(isFailure('clean-stop')).toBe(false);
    for (const o of ['invalid-selection', 'config-invalid', 'no-services',
      'unknown-service', 'policy-denied', 'start-failed', 'runtime-crash',
      'shutdown-timeout'] as LifecycleOutcome[]) {
      expect(isFailure(o), o).toBe(true);
    }
  });

  it('treats an unknown outcome as a failure, not a success', () => {
    // A kind added without a table row must fail loudly rather than
    // silently exit 0.
    const rogue = 'invented-outcome' as LifecycleOutcome;
    expect(exitCodeFor(rogue)).toBe(1);
    expect(codeFor(rogue)).toBe('GENERIC');
  });

  it('worstOutcome keeps the first failure across a whole run', () => {
    expect(worstOutcome([])).toBe('clean-stop');
    expect(worstOutcome(['clean-stop'])).toBe('clean-stop');
    // Severity, not magnitude: the first failure explains the rest,
    // so a later one does not displace it.
    expect(worstOutcome(['start-failed', 'shutdown-timeout'])).toBe('start-failed');
    expect(worstOutcome(['clean-stop', 'policy-denied'])).toBe('policy-denied');
    expect(worstOutcome(['runtime-crash', 'unknown-service'])).toBe('runtime-crash');
  });
});

// ---------------------------------------------------------------------------
// Ordering
// ---------------------------------------------------------------------------

describe('startOrder', () => {
  it('is registration order with no declarations', () => {
    const r = registryOf(fakeService('a'), fakeService('b'), fakeService('c'));
    expect(startOrder(r, ['a', 'b', 'c'])).toEqual(['a', 'b', 'c']);
  });

  it('puts a dependency before its dependent', () => {
    const r = registryOf(fakeService('api', { dependsOn: ['db'] }), fakeService('db'));
    expect(startOrder(r, ['api', 'db'])).toEqual(['db', 'api']);
  });

  it('ignores a dependency outside the selected set', () => {
    // Under the selector form exactly one service runs, and its
    // dependencies are the operator's business.
    const r = registryOf(fakeService('api', { dependsOn: ['db'] }), fakeService('db'));
    expect(startOrder(r, ['api'])).toEqual(['api']);
  });

  it('throws on a dependency cycle', () => {
    const r = registryOf(
      fakeService('a', { dependsOn: ['b'] }),
      fakeService('b', { dependsOn: ['a'] }),
    );
    expect(() => startOrder(r, ['a', 'b'])).toThrow(/dependency cycle/);
  });
});

// ---------------------------------------------------------------------------
// Topics
// ---------------------------------------------------------------------------

describe('lifecycle topics', () => {
  it('produces exactly the six contract topic strings', () => {
    expect(defaultTopics()).toEqual({
      'service.started': 'kit.serve.service.started',
      'service.ready_reported': 'kit.serve.service.ready_reported',
      'service.failed': 'kit.serve.service.failed',
      'service.stopped': 'kit.serve.service.stopped',
      'supervisor.ready_reported': 'kit.serve.supervisor.ready_reported',
      'supervisor.stopped': 'kit.serve.supervisor.stopped',
    });
  });

  it('never emits a bare "ready" action', () => {
    // A bare `ready` fails the past-tense validation in
    // event-topics.md; Go subscribers reject it.
    for (const t of Object.values(defaultTopics())) {
      expect(t.endsWith('.ready'), t).toBe(false);
    }
  });

  it('rebrands the prefix', () => {
    expect(defaultTopics('acme.serve')['service.started'])
      .toBe('acme.serve.service.started');
  });
});

// ---------------------------------------------------------------------------
// Supervisor lifecycle
// ---------------------------------------------------------------------------

describe('Supervisor', () => {
  it('starts, reports ready, and stops cleanly on a signal', async () => {
    const api = fakeService('api', { addr: '127.0.0.1:8080' });
    const pub = recorder();
    const sup = new Supervisor(registryOf(api), { publisher: pub });

    const res = await sup.run(abortAfter(10), ['api'], enabled('api'));

    expect(res.outcome).toBe('clean-stop');
    expect(res.exitCode).toBe(0);
    expect(res.error).toBeUndefined();
    expect(res.started).toEqual(['api']);
    expect(res.ready).toEqual(['api']);
    expect(api.stopCount).toBe(1);

    const topics = pub.events.map((e) => e.topic);
    expect(topics).toEqual([
      'kit.serve.service.started',
      'kit.serve.service.ready_reported',
      'kit.serve.supervisor.ready_reported',
      'kit.serve.service.stopped',
      'kit.serve.supervisor.stopped',
    ]);
  });

  it('carries the resolved address on ready_reported and nowhere else', async () => {
    const pub = recorder();
    const sup = new Supervisor(registryOf(fakeService('api', { addr: '127.0.0.1:0' })), {
      publisher: pub,
    });
    await sup.run(abortAfter(10), ['api'], enabled('api'));

    for (const e of pub.events) {
      if (e.topic === 'kit.serve.service.ready_reported') {
        expect(e.payload.address).toBe('127.0.0.1:0');
      } else {
        expect(e.payload.address).toBeUndefined();
      }
    }
  });

  it('carries the service identifier in the payload, never in the topic', async () => {
    const pub = recorder();
    const sup = new Supervisor(registryOf(fakeService('api')), { publisher: pub });
    await sup.run(abortAfter(10), ['api'], enabled('api'));

    for (const e of pub.events) {
      expect(e.topic).not.toContain('api');
      if (e.topic.includes('.service.')) expect(e.payload.service).toBe('api');
    }
  });

  it('reports the aggregate ready only when every service is ready', async () => {
    const pub = recorder();
    const sup = new Supervisor(registryOf(fakeService('a'), fakeService('b')), {
      publisher: pub,
    });
    await sup.run(abortAfter(20), ['a', 'b'], enabled('a', 'b'));

    const order = pub.events.map((e) => `${e.topic}:${e.payload.service ?? ''}`);
    const aggregate = order.indexOf('kit.serve.supervisor.ready_reported:');
    expect(aggregate).toBeGreaterThan(order.indexOf('kit.serve.service.ready_reported:a'));
    expect(aggregate).toBeGreaterThan(order.indexOf('kit.serve.service.ready_reported:b'));
  });

  it('stops in the exact reverse of start order', async () => {
    const a = fakeService('a');
    const b = fakeService('b');
    const c = fakeService('c');
    stopSequence = 0;
    const sup = new Supervisor(registryOf(a, b, c));
    const res = await sup.run(abortAfter(20), ['a', 'b', 'c'], enabled('a', 'b', 'c'));

    expect(res.started).toEqual(['a', 'b', 'c']);
    // Lower sequence number = stopped earlier. c must be first.
    expect(c.stopOrder[0]).toBeLessThan(b.stopOrder[0]!);
    expect(b.stopOrder[0]).toBeLessThan(a.stopOrder[0]!);
  });

  it('treats a start failure as start-failed at exit 1', async () => {
    const pub = recorder();
    const sup = new Supervisor(
      registryOf(fakeService('api', { failStartWith: 'bind: address in use' })),
      { publisher: pub },
    );
    const res = await sup.run(abortAfter(50), ['api'], enabled('api'));

    expect(res.outcome).toBe('start-failed');
    expect(res.exitCode).toBe(1);
    expect(res.error?.code).toBe('GENERIC');
    expect(res.error?.message).toContain('api: bind: address in use');
    expect(res.ready).toEqual([]);
    expect(pub.events.some((e) =>
      e.topic === 'kit.serve.service.failed' && e.payload.reason === 'start')).toBe(true);
  });

  it('treats a readiness timeout as a start failure', async () => {
    const pub = recorder();
    const sup = new Supervisor(registryOf(fakeService('api', { neverReady: true })));
    const res = await sup.run(abortAfter(500), ['api'], {
      api: { enabled: true, readyTimeoutMs: 15 },
    });

    expect(res.outcome).toBe('start-failed');
    expect(res.exitCode).toBe(1);
    expect(res.error?.message).toContain('not ready within 15ms');
    void pub;
  });

  it('treats a service returning before ready as a start failure', async () => {
    // Even a clean return is a failure here: it was asked to serve and
    // it did not.
    const sup = new Supervisor(registryOf(fakeService('api', { neverReady: true, returnAfterMs: 1 })));
    const res = await sup.run(abortAfter(200), ['api'], enabled('api'));

    expect(res.outcome).toBe('start-failed');
    expect(res.error?.message).toContain('returned before reporting ready');
  });

  it('does not start a later service after an earlier one fails', async () => {
    const second = fakeService('b');
    const sup = new Supervisor(
      registryOf(fakeService('a', { failStartWith: 'boom' }), second),
    );
    const res = await sup.run(abortAfter(100), ['a', 'b'], enabled('a', 'b'));

    expect(res.outcome).toBe('start-failed');
    expect(second.startCount).toBe(0);
  });

  it('brings everything down under fail-fast when one service crashes', async () => {
    const survivor = fakeService('b');
    const sup = new Supervisor(
      registryOf(fakeService('a', { crashAfterMs: 'upstream gone' }), survivor),
      { config: { failurePolicy: 'fail-fast' } },
    );
    const res = await sup.run(abortAfter(500), ['a', 'b'], enabled('a', 'b'));

    expect(res.outcome).toBe('runtime-crash');
    expect(res.exitCode).toBe(1);
    expect(res.failed['a']).toBe('upstream gone');
    expect(survivor.stopCount).toBe(1);
  });

  it('keeps the rest running under isolate until the last one stops', async () => {
    const sup = new Supervisor(
      registryOf(
        fakeService('a', { crashAfterMs: 'gone' }),
        fakeService('b', { returnAfterMs: 40 }),
      ),
      { config: { failurePolicy: 'isolate' } },
    );
    const res = await sup.run(abortAfter(1000), ['a', 'b'], enabled('a', 'b'));

    // The process survived a's failure and ran until b returned, but
    // the exit code still reflects the worst outcome of the whole run.
    expect(res.failed['a']).toBe('gone');
    expect(res.exitCode).toBe(1);
  });

  it('abandons a stop that exceeds its budget and moves on', async () => {
    const later = fakeService('b');
    const pub = recorder();
    const sup = new Supervisor(
      registryOf(fakeService('a', { hangInStop: true }), later),
      { publisher: pub },
    );
    const res = await sup.run(abortAfter(10), ['a', 'b'], {
      a: { enabled: true, stopTimeoutMs: 20 },
      b: { enabled: true },
    });

    // b stops first (reverse order), then a's stop is abandoned rather
    // than blocking the whole shutdown.
    expect(later.stopCount).toBe(1);
    expect(res.failed['a']).toContain('stop exceeded');
    expect(pub.events.some((e) =>
      e.topic === 'kit.serve.service.failed' && e.payload.reason === 'stop_timeout')).toBe(true);
  });

  it('reports a stop that rejects as a failure', async () => {
    const sup = new Supervisor(registryOf(fakeService('api', { failStopWith: 'drain failed' })));
    const res = await sup.run(abortAfter(10), ['api'], enabled('api'));
    expect(res.failed['api']).toBe('drain failed');
    expect(res.exitCode).toBe(1);
  });

  it('abandons the drain when the escalation signal fires', async () => {
    const escalated = new AbortController();
    escalated.abort();
    const svc = fakeService('api');
    const pub = recorder();
    const sup = new Supervisor(registryOf(svc), {
      escalate: escalated.signal, publisher: pub,
    });
    const res = await sup.run(abortAfter(10), ['api'], enabled('api'));

    expect(svc.stopCount).toBe(0);
    expect(res.exitCode).toBe(1);
    expect(pub.events.some((e) =>
      e.topic === 'kit.serve.service.failed' && e.payload.reason === 'escalated')).toBe(true);
  });

  it('refuses an empty selection rather than exiting 0', async () => {
    const sup = new Supervisor(new ServiceRegistry());
    const res = await sup.run(abortAfter(10), [], {});
    expect(res.outcome).toBe('no-services');
    expect(res.exitCode).toBe(2);
  });

  it('emits the log counterpart when no bus is wired', async () => {
    // A tool with no bus still produces an operator-legible trace, with
    // the identifier and address as structured fields.
    const log = { info: vi.fn(), error: vi.fn() };
    const sup = new Supervisor(registryOf(fakeService('api', { addr: ':8080' })), { logger: log });
    await sup.run(abortAfter(10), ['api'], enabled('api'));

    const readyCall = log.info.mock.calls.find((c) => c[0] === 'serve: ready_reported');
    expect(readyCall).toBeDefined();
    expect(readyCall).toContain('service');
    expect(readyCall).toContain('api');
    expect(readyCall).toContain('address');
    expect(readyCall).toContain(':8080');
  });

  it('logs a failure at error level, not info', async () => {
    const log = { info: vi.fn(), error: vi.fn() };
    const sup = new Supervisor(registryOf(fakeService('api', { failStartWith: 'nope' })), {
      logger: log,
    });
    await sup.run(abortAfter(50), ['api'], enabled('api'));

    expect(log.error).toHaveBeenCalled();
    expect(log.error.mock.calls[0]![0]).toBe('serve: failed');
    expect(log.info.mock.calls.every((c) => c[0] !== 'serve: failed')).toBe(true);
  });

  it('survives a publisher that throws', async () => {
    // An event sink is observability, not correctness.
    const sup = new Supervisor(registryOf(fakeService('api')), {
      publisher: { publish() { throw new Error('bus down'); } },
    });
    const res = await sup.run(abortAfter(10), ['api'], enabled('api'));
    expect(res.outcome).toBe('clean-stop');
  });

  it('runs twice on one registry, each observing only its own signal', async () => {
    // A run is over when the supervisor returns; the registry it ran
    // on is not consumed.
    const reg = registryOf(fakeService('api'));
    const sup = new Supervisor(reg);
    const first = await sup.run(abortAfter(10), ['api'], enabled('api'));
    const second = await sup.run(abortAfter(10), ['api'], enabled('api'));

    expect(first.outcome).toBe('clean-stop');
    expect(second.outcome).toBe('clean-stop');
    expect(second.ready).toEqual(['api']);
  });

  it('returns immediately when handed an already-aborted signal', async () => {
    const ac = new AbortController();
    ac.abort();
    const sup = new Supervisor(registryOf(fakeService('api')));
    const res = await sup.run(ac.signal, ['api'], enabled('api'));
    expect(res.outcome).toBe('clean-stop');
  });
});

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

describe('configuration defaults', () => {
  it('matches the contract table', () => {
    expect(DEFAULT_READY_TIMEOUT_MS).toBe(30_000);
    expect(DEFAULT_STOP_TIMEOUT_MS).toBe(30_000);
    expect(DEFAULT_SHUTDOWN_TIMEOUT_MS).toBe(60_000);
    expect(DEFAULT_FAILURE_POLICY).toBe('fail-fast');
  });

  it('treats enabled as false unless it is explicitly true', () => {
    // An unrequested open port is the risk this default guards against,
    // so anything short of an explicit true is disabled.
    const r = registryOf(fakeService('api'));
    for (const cfg of [{}, { enabled: false }, { enabled: undefined }] as ServiceConfig[]) {
      expect(resolve(r, { args: [], configs: { api: cfg } }).selected).toEqual([]);
    }
    expect(resolve(r, { args: [], configs: { api: { enabled: true } } }).selected).toEqual(['api']);
  });
});

// ---------------------------------------------------------------------------
// Signals
// ---------------------------------------------------------------------------

describe('signalController', () => {
  // The listeners are driven with process.emit rather than a real
  // process.kill: delivering an actual SIGINT/SIGTERM to the test
  // process kills the vitest worker. emit dispatches to exactly the
  // listeners signalController registered with process.on, so the
  // registration path under test is the real one.
  const raise = (sig: NodeJS.Signals): void => { process.emit(sig); };

  it('aborts on the first signal and escalates on the second', () => {
    const c = signalController();
    try {
      expect(c.signal.aborted).toBe(false);
      expect(c.escalate.aborted).toBe(false);

      raise('SIGTERM');
      expect(c.signal.aborted).toBe(true);
      // The first signal begins a graceful drain; it does not escalate.
      expect(c.escalate.aborted).toBe(false);

      // A second signal of *either* kind aborts the drain, so an
      // operator can escalate without reaching for SIGKILL.
      raise('SIGINT');
      expect(c.escalate.aborted).toBe(true);
    } finally {
      c.stop();
    }
  });

  it('escalates on a repeat of the same signal too', () => {
    const c = signalController();
    try {
      raise('SIGINT');
      expect(c.escalate.aborted).toBe(false);
      raise('SIGINT');
      expect(c.escalate.aborted).toBe(true);
    } finally {
      c.stop();
    }
  });

  it('ignores signals after stop', () => {
    const c = signalController();
    c.stop();
    raise('SIGTERM');
    expect(c.signal.aborted).toBe(false);
  });

  it('removes its process listeners on stop', () => {
    const before = process.listenerCount('SIGTERM');
    const c = signalController();
    expect(process.listenerCount('SIGTERM')).toBe(before + 1);
    c.stop();
    expect(process.listenerCount('SIGTERM')).toBe(before);
    c.stop(); // idempotent
    expect(process.listenerCount('SIGTERM')).toBe(before);
  });
});

// ---------------------------------------------------------------------------
// Keeping the process alive
// ---------------------------------------------------------------------------

describe('LoopKeepAlive', () => {
  // Neither a process signal listener nor an AbortSignal listener refs
  // the Node event loop. Without a referenced handle a supervisor whose
  // services are all parked on their abort signal lets the process exit
  // 0 the moment `run` yields — a process that exits 0 without
  // listening, which is indistinguishable from a successful start to
  // systemd or a container runtime.
  it('holds a referenced handle only between hold and release', () => {
    const k = new LoopKeepAlive();
    expect(k.held).toBe(false);
    k.hold();
    expect(k.held).toBe(true);
    k.hold(); // idempotent
    expect(k.held).toBe(true);
    k.release();
    expect(k.held).toBe(false);
    k.release(); // idempotent
    expect(k.held).toBe(false);
  });
});

describe('serving in a real process', () => {
  // The unit tests above all drive the supervisor with an abort signal
  // that fires on a timer, so they would pass even if nothing held the
  // event loop open. Only a subprocess proves the process actually
  // stays up: it is the difference between "the run resolved" and "the
  // tool served".
  // Inside the package rather than the temp dir: the harness
  // `require`s commander, which only resolves from a path under
  // node_modules' reach.
  const entry = path.join(__dirname, '..', `.serve-e2e-${process.pid}.cjs`);

  beforeAll(() => {
    const src = path.join(__dirname, 'serve.ts');
    const built = execFileSync(
      path.join(__dirname, '..', 'node_modules', '.bin', 'esbuild'),
      [src, '--bundle', '--platform=node', '--format=cjs', '--log-level=error'],
      { encoding: 'utf8' },
    );
    writeFileSync(entry, `${built}
const registry = new ServiceRegistry();
registry.register({
  name: 'api',
  async start(signal, ready) {
    ready();
    await new Promise((r) => signal.addEventListener('abort', () => r(), { once: true }));
  },
  ready: () => true,
  async stop() {},
});
const { Command } = require('commander');
const program = new Command('tool');
registerServe(program, { registry, configs: { api: { enabled: true } } });
// No exitOverride and no catch that rewrites the code: registerServe
// sets process.exitCode itself, and swallowing that into a blanket 1
// is exactly the masking this test exists to catch.
program.parseAsync(process.argv.slice(2), { from: 'user' });
`);
  });

  afterAll(() => { rmSync(entry, { force: true }); });

  it('stays alive while serving and exits 0 on SIGTERM', async () => {
    const child = spawn(process.execPath, [entry, 'serve', 'api'], { stdio: 'ignore' });
    try {
      await sleep(600);
      // Still running: the supervisor held the loop open rather than
      // letting node fall off the end of the script.
      expect(child.exitCode).toBeNull();

      child.kill('SIGTERM');
      const code = await new Promise<number | null>((r) => child.on('exit', r));
      // A signal-initiated stop is a clean stop. Answering SIGTERM
      // non-zero makes every rolling restart look like a crash.
      expect(code).toBe(0);
    } finally {
      if (child.exitCode === null) child.kill('SIGKILL');
    }
  }, 20_000);

  it('exits 2 on two service operands', async () => {
    const code = await runToCompletion(entry, ['serve', 'api', 'socket']);
    expect(code).toBe(2);
  }, 20_000);

  it('exits 3 on an unknown service', async () => {
    const code = await runToCompletion(entry, ['serve', 'ghost']);
    expect(code).toBe(3);
  }, 20_000);
});

/** Runs the built entry to completion and resolves its exit code. */
function runToCompletion(entry: string, argv: string[]): Promise<number | null> {
  const child = spawn(process.execPath, [entry, ...argv], { stdio: 'ignore' });
  return new Promise((r) => child.on('exit', r));
}

// ---------------------------------------------------------------------------
// Command wiring
// ---------------------------------------------------------------------------

describe('registerServe', () => {
  /** Builds a program whose serve run reports its exit code to the test. */
  function programWith(
    registry: ServiceRegistry,
    configs: Record<string, ServiceConfig>,
    extra: Partial<Parameters<typeof registerServe>[1]> = {},
  ): { program: Command; exits: Array<{ code: number; message?: string }>; out: string[] } {
    const exits: Array<{ code: number; message?: string }> = [];
    const out: string[] = [];
    const program = new Command('tool').exitOverride();
    registerServe(program, {
      registry,
      configs,
      stdout: { write: (s: string) => { out.push(s); return true; } },
      onExit: (code, error) => exits.push({ code, message: error?.message }),
      ...extra,
    });
    return { program, exits, out };
  }

  it('mounts serve with an optional service operand', () => {
    const { program } = programWith(registryOf(fakeService('api')), {});
    const serve = program.commands.find((c) => c.name() === 'serve');
    expect(serve).toBeDefined();
    expect(serve!.description()).toBe('Run configured services under one lifecycle');
  });

  it('does not mount a `list` child', () => {
    // `list` is reserved selector vocabulary; the inspection form is a
    // flag so it cannot be confused with the selector form.
    const { program } = programWith(registryOf(fakeService('api')), {});
    const serve = program.commands.find((c) => c.name() === 'serve')!;
    expect(serve.commands.map((c) => c.name())).not.toContain('list');
    expect(serve.options.some((o) => o.long === '--list')).toBe(true);
  });

  it('--list prints every service in registration order', async () => {
    const { program, out } = programWith(
      registryOf(fakeService('zeta'), fakeService('alpha')),
      { zeta: { enabled: true } },
    );
    await program.parseAsync(['serve', '--list'], { from: 'user' });

    const text = out.join('');
    expect(text).toContain('SERVICE');
    expect(text.indexOf('zeta')).toBeLessThan(text.indexOf('alpha'));
    expect(text).toMatch(/zeta\s+true\s+true/);
    // alpha has no config block: not configured, not enabled.
    expect(text).toMatch(/alpha\s+false\s+false/);
  });

  it('exits 2 on two or more service operands', async () => {
    // Commander's own excess-argument refusal exits 1; the contract
    // says 2, so the arity check is owned here.
    const { program, exits } = programWith(
      registryOf(fakeService('api'), fakeService('socket')),
      enabled('api', 'socket'),
    );
    await program.parseAsync(['serve', 'api', 'socket'], { from: 'user' });

    expect(exits).toEqual([{ code: 2, message: 'serve accepts at most one service name, got 2' }]);
  });

  it('exits 3 on an unknown service', async () => {
    const { program, exits } = programWith(registryOf(fakeService('api')), enabled('api'));
    await program.parseAsync(['serve', 'ghost'], { from: 'user' });

    expect(exits[0]!.code).toBe(3);
    expect(exits[0]!.message).toContain('unknown service "ghost"');
  });

  it('exits 5 when the policy gate denies the named service', async () => {
    const { program, exits } = programWith(
      registryOf(fakeService('api', { cls: ['destructive', 'ingress'] })),
      enabled('api'),
      { policy: { allow: () => ({ ok: false, reason: 'blocked' }) } },
    );
    await program.parseAsync(['serve', 'api'], { from: 'user' });
    expect(exits[0]!.code).toBe(5);
  });

  it('exits 2 when the supervisor form resolves to nothing', async () => {
    const { program, exits } = programWith(
      registryOf(fakeService('api')), { api: { enabled: false } },
    );
    await program.parseAsync(['serve'], { from: 'user' });
    expect(exits[0]!.code).toBe(2);
  });

  it('exits 0 for a clean signal-initiated stop', async () => {
    const { program, exits } = programWith(
      registryOf(fakeService('api', { returnAfterMs: 5 })), enabled('api'),
    );
    await program.parseAsync(['serve', 'api'], { from: 'user' });
    expect(exits).toEqual([{ code: 0, message: undefined }]);
  });

  it('runs a disabled service through the selector form and exits 0', async () => {
    // The override rule, end to end through the command surface.
    const svc = fakeService('api', { returnAfterMs: 5 });
    const { program, exits } = programWith(registryOf(svc), { api: { enabled: false } });
    await program.parseAsync(['serve', 'api'], { from: 'user' });

    expect(svc.startCount).toBe(1);
    expect(exits[0]!.code).toBe(0);
  });

  it('exits 1 when the selected service fails to start', async () => {
    const { program, exits } = programWith(
      registryOf(fakeService('api', { failStartWith: 'bind refused' })), enabled('api'),
    );
    await program.parseAsync(['serve', 'api'], { from: 'user' });

    expect(exits[0]!.code).toBe(1);
    expect(exits[0]!.message).toContain('bind refused');
  });

  // -- --enable / --disable -------------------------------------------

  it('--enable is repeatable and makes an unconfigured service configured and enabled', async () => {
    // Neither service has a config block: the flag is what configures
    // each one, and naming both starts both.
    const a = fakeService('alpha', { returnAfterMs: 5 });
    const b = fakeService('beta', { returnAfterMs: 5 });
    const { program, exits } = programWith(registryOf(a, b), {});
    await program.parseAsync(
      ['serve', '--enable', 'alpha', '--enable', 'beta'], { from: 'user' },
    );

    expect(a.startCount).toBe(1);
    expect(b.startCount).toBe(1);
    expect(exits).toEqual([{ code: 0, message: undefined }]);
  });

  it('--disable skips an enabled service silently under the supervisor form', async () => {
    const on = fakeService('on', { returnAfterMs: 5 });
    const off = fakeService('off', { returnAfterMs: 5 });
    const { program, exits } = programWith(registryOf(on, off), enabled('on', 'off'));
    await program.parseAsync(['serve', '--disable', 'off'], { from: 'user' });

    expect(on.startCount).toBe(1);
    expect(off.startCount).toBe(0);
    expect(exits).toEqual([{ code: 0, message: undefined }]);
  });

  it('--disable on every enabled service leaves nothing to run and exits 2', async () => {
    // returnAfterMs keeps a regression from hanging the suite: a service
    // the flag failed to disable would otherwise wait for a signal.
    const { program, exits } = programWith(
      registryOf(fakeService('api', { returnAfterMs: 5 })), enabled('api'),
    );
    await program.parseAsync(['serve', '--disable', 'api'], { from: 'user' });
    expect(exits[0]!.code).toBe(2);
  });

  it.each([['--enable'], ['--disable']])(
    'refuses %s under the selector form with USAGE exit 2', async (flag) => {
      const svc = fakeService('api', { returnAfterMs: 5 });
      const { program, exits } = programWith(registryOf(svc), enabled('api'));
      await program.parseAsync(['serve', 'api', flag, 'api'], { from: 'user' });

      expect(svc.startCount).toBe(0);
      expect(exits).toEqual([{
        code: 2,
        message: '--enable/--disable apply to the supervisor form; drop the service name or drop the flags',
      }]);
    },
  );

  it('does not let the flags leak into the caller\'s configs', async () => {
    const configs: Record<string, ServiceConfig> = { api: { enabled: true } };
    const { program } = programWith(registryOf(fakeService('api', { returnAfterMs: 5 })), configs);
    await program.parseAsync(['serve', '--disable', 'api'], { from: 'user' });
    expect(configs.api!.enabled).toBe(true);
  });

  // -- timeout flags ----------------------------------------------------

  it('--ready-timeout bounds start for every resolved service', async () => {
    // Without the flag the default 30s budget would outlive the test;
    // the flag is what turns a never-ready service into a start failure.
    const { program, exits } = programWith(
      registryOf(fakeService('api', { neverReady: true })), enabled('api'),
    );
    await program.parseAsync(['serve', '--ready-timeout', '15ms'], { from: 'user' });

    expect(exits[0]!.code).toBe(1);
    expect(exits[0]!.message).toContain('ready');
  });

  it('--shutdown-timeout bounds the whole stop', async () => {
    const { program, exits } = programWith(
      registryOf(fakeService('api', { hangInStop: true, returnAfterMs: 1 })), enabled('api'),
    );
    await program.parseAsync(
      ['serve', 'api', '--stop-timeout', '10ms', '--shutdown-timeout', '15ms'], { from: 'user' },
    );
    expect(exits[0]!.code).toBe(1);
  });

  it('refuses an unparseable duration as USAGE exit 2', async () => {
    const svc = fakeService('api', { returnAfterMs: 5 });
    const { program, exits } = programWith(registryOf(svc), enabled('api'));
    await program.parseAsync(['serve', '--ready-timeout', '30x'], { from: 'user' });

    expect(svc.startCount).toBe(0);
    expect(exits[0]!.code).toBe(2);
    expect(exits[0]!.message).toContain('--ready-timeout');
  });

  it('leaves no signal listeners behind after a run', async () => {
    const before = process.listenerCount('SIGTERM');
    const { program } = programWith(
      registryOf(fakeService('api', { returnAfterMs: 5 })), enabled('api'),
    );
    await program.parseAsync(['serve', 'api'], { from: 'user' });
    expect(process.listenerCount('SIGTERM')).toBe(before);
  });
});

// ---------------------------------------------------------------------------
// Flag overrides
// ---------------------------------------------------------------------------

describe('applyEnableDisable', () => {
  it('enable makes an unconfigured service configured and enabled', () => {
    expect(applyEnableDisable({}, ['api'], [])).toEqual({ api: { enabled: true } });
  });

  it('disable clears enablement on a configured service and keeps its budgets', () => {
    const out = applyEnableDisable({ api: { enabled: true, readyTimeoutMs: 5 } }, [], ['api']);
    expect(out).toEqual({ api: { enabled: false, readyTimeoutMs: 5 } });
  });

  it('disable on an unconfigured service is a no-op', () => {
    expect(applyEnableDisable({}, [], ['ghost'])).toEqual({});
  });

  it('enable wins over disable for the same name', () => {
    expect(applyEnableDisable({}, ['api'], ['api'])).toEqual({ api: { enabled: true } });
  });

  it('returns a new map rather than mutating the input', () => {
    const input: Record<string, ServiceConfig> = { api: { enabled: true } };
    const out = applyEnableDisable(input, [], ['api']);
    expect(input.api!.enabled).toBe(true);
    expect(out).not.toBe(input);
  });
});

describe('applyTimeoutOverrides', () => {
  it('applies one budget to every resolved service', () => {
    const out = applyTimeoutOverrides(
      { a: { enabled: true }, b: { enabled: false, stopTimeoutMs: 1 } }, 10, 20,
    );
    expect(out).toEqual({
      a: { enabled: true, readyTimeoutMs: 10, stopTimeoutMs: 20 },
      b: { enabled: false, readyTimeoutMs: 10, stopTimeoutMs: 20 },
    });
  });

  it('leaves the map alone when neither flag is set', () => {
    const out = applyTimeoutOverrides({ a: { enabled: true, readyTimeoutMs: 7 } });
    expect(out).toEqual({ a: { enabled: true, readyTimeoutMs: 7 } });
  });
});

describe('parseDurationMs', () => {
  it.each([
    ['30s', 30_000], ['500ms', 500], ['1m30s', 90_000], ['1h', 3_600_000],
    ['1.5s', 1_500], ['2', 2_000],
  ])('parses %s', (raw, ms) => {
    expect(parseDurationMs(raw)).toBe(ms);
  });

  it.each([[''], ['30x'], ['s'], ['1m 30s'], ['abc']])('rejects %j', (raw) => {
    expect(parseDurationMs(raw)).toBeNull();
  });
});
