/**
 * Core probe logic -- HTTP checks, latency, event publishing.
 */

import { type Bus, createEvent } from '../../../sdk/ts/src/bus';
import { type ProgressReporter } from '../../../sdk/ts/src/progress';

// ---------------------------------------------------------------------------
// Config types (matches probe.yaml)
// ---------------------------------------------------------------------------

export interface ProbeConfig {
  interval: string;
  targets: TargetEntry[];
}

export interface TargetEntry {
  name: string;
  url: string;
  method: string;
  timeout: string;
  expect: { status: number };
  alerts?: { type: string }[];
}

// ---------------------------------------------------------------------------
// Result
// ---------------------------------------------------------------------------

export interface Result {
  target: string;
  ok: boolean;
  status: number;
  latencyMs: number;
  error?: string;
}

// ---------------------------------------------------------------------------
// Parse timeout string to ms
// ---------------------------------------------------------------------------

function parseTimeoutMs(s: string): number {
  const match = s.match(/^(\d+)(s|ms)$/);
  if (!match) return 5000;
  const val = parseInt(match[1], 10);
  return match[2] === 's' ? val * 1000 : val;
}

// ---------------------------------------------------------------------------
// Single target check
// ---------------------------------------------------------------------------

async function checkTarget(t: TargetEntry): Promise<Result> {
  const timeoutMs = parseTimeoutMs(t.timeout);
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  const start = Date.now();
  try {
    const resp = await fetch(t.url, {
      method: t.method,
      signal: controller.signal,
    });
    const latencyMs = Date.now() - start;
    const ok = resp.status === t.expect.status;
    return { target: t.name, ok, status: resp.status, latencyMs };
  } catch (err: unknown) {
    const latencyMs = Date.now() - start;
    const msg = err instanceof Error ? err.message : String(err);
    return {
      target: t.name, ok: false, status: 0, latencyMs, error: msg,
    };
  } finally {
    clearTimeout(timer);
  }
}

// ---------------------------------------------------------------------------
// Run all targets sequentially
// ---------------------------------------------------------------------------

export async function checkTargets(
  cfg: ProbeConfig,
  b: Bus,
  progress: ProgressReporter,
): Promise<Result[]> {
  const results: Result[] = [];
  const prevFailing = new Set<string>();

  for (let i = 0; i < cfg.targets.length; i++) {
    const t = cfg.targets[i];

    progress.emit({
      phase: 'probe',
      step: t.name,
      current: i + 1,
      total: cfg.targets.length,
      percent: Math.round(((i + 1) / cfg.targets.length) * 100),
      message: `checking ${t.url}`,
    });

    const r = await checkTarget(t);
    results.push(r);

    const payload = {
      target: r.target,
      ok: r.ok,
      status: r.status,
      latencyMs: r.latencyMs,
      source: 'probe/ts',
      method: t.method,
      ...(r.error ? { error: r.error } : {}),
    };

    b.publish(createEvent('kit.probe.check.executed', 'probe/ts', payload));

    if (!r.ok) {
      b.publish(createEvent('kit.probe.check.alerted', 'probe/ts', payload));
      prevFailing.add(t.name);
    } else if (prevFailing.has(t.name)) {
      b.publish(createEvent('kit.probe.check.recovered', 'probe/ts', payload));
      prevFailing.delete(t.name);
    }
  }

  return results;
}
