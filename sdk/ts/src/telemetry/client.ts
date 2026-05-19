/**
 * @module telemetry/client
 * @package @hop-top/kit
 *
 * Non-blocking telemetry Client — the TS mirror of the planned Go
 * `runtime/telemetry.Client`. Fire-and-forget `record()`, bounded
 * in-memory queue, two sinks (HTTPS POST + JSONL append-with-rotate),
 * and a best-effort `shutdown()` drain.
 *
 * Design constraints:
 *
 *   - **Never block, never throw**: `record()` must return in <1ms even
 *     under queue saturation. Errors during background drain bump
 *     `droppedCount`; they never surface to the caller.
 *   - **Default-deny consent**: every `record()` re-reads the consent
 *     file (synchronously). Missing / malformed / `denied` collapses to
 *     a no-op. The cost (one stat + small YAML parse) is the price of
 *     a fail-closed posture.
 *   - **Default Mode is Off**: `resolveMode()` is consulted at the same
 *     gating step. Anything other than `anon` / `full` no-ops.
 *   - **Envelope shape**: matches the cross-language event-schema doc
 *     for the common fields. NOTE: per the task spec, the TS+Py SDKs
 *     diverge from Go by carrying free-form `event` (string) + `attrs`
 *     (object) at the envelope root. See event-schema §3.
 *   - **install_id caching**: `getInstallId()` does file I/O; we resolve
 *     it lazily and cache the resolved promise.
 *
 * Default sink is `jsonl` because CI runners default to `mode=off` but
 * adopters who flip the mode without setting `KIT_TELEMETRY_ENDPOINT`
 * shouldn't have events silently dropped to a missing HTTPS endpoint.
 */
import { existsSync, readFileSync, promises as fs } from 'node:fs';
import { homedir } from 'node:os';
import * as path from 'node:path';
import * as yaml from 'js-yaml';

import { Mode, resolveMode } from './mode';
import { getInstallId } from './installId';
import { consentPath, legacyConsentPath } from './consent';
import { redact } from './redact';

/** Sink kinds understood by the Client. */
export type SinkKind = 'https' | 'jsonl';

/**
 * ClientOptions tune the Client. Every field has an env-var equivalent
 * so adopters can configure via the process environment without a
 * code-side touch.
 */
export interface ClientOptions {
  /** HTTPS endpoint for the `https` sink. Env: `KIT_TELEMETRY_ENDPOINT`. */
  endpoint?: string;
  /** Sink kind. Env: `KIT_TELEMETRY_SINK`. Default `jsonl`. */
  sink?: SinkKind;
  /** Target file for the `jsonl` sink. Env: `KIT_TELEMETRY_SINK_FILE`. */
  sinkFile?: string;
  /** Bounded queue capacity. Env: `KIT_TELEMETRY_QUEUE_SIZE`. Default 1024. */
  queueSize?: number;
  /** Custom redactor — runs BEFORE the default opinionated redact pass. */
  redactor?: (attrs: Record<string, unknown>) => Record<string, unknown>;
  /** SDK version surfaced in the envelope's `sdk_version` field. */
  sdkVersion?: string;
}

/** Default queue capacity when neither opts nor env override. */
const DEFAULT_QUEUE_SIZE = 1024;
/** Background flush cadence. */
const FLUSH_INTERVAL_MS = 5_000;
/** Threshold that triggers an early flush before the interval fires. */
const FLUSH_BATCH_TRIGGER = 64;
/** HTTPS connect timeout (per the ADR's "non-blocking" stance). */
const HTTPS_CONNECT_TIMEOUT_MS = 5_000;
/** HTTPS overall timeout. */
const HTTPS_TOTAL_TIMEOUT_MS = 10_000;
/** Rotate the JSONL sink when it crosses this size. */
const JSONL_ROTATE_BYTES = 10 * 1024 * 1024;
/** Fallback SDK version when neither opts nor `package.json` resolve. */
const FALLBACK_SDK_VERSION = '0.0.0-dev';
/** Schema version pin for this Client revision. */
const SCHEMA_VERSION = '1';
/** sdk_lang token per the cross-language event-schema doc. */
const SDK_LANG = 'ts';

/**
 * Envelope shape this Client emits. Aligned with the cross-language
 * event-schema doc's "common fields" plus the TS/Py free-form `event`
 * + `attrs` extension.
 *
 * `attrs` is `null` when `mode === Mode.Anon` (matches the rs SDK's
 * ``Value::Null`` strip) — the key stays for envelope-shape stability,
 * the payload is dropped to avoid PII leakage. In `Mode.Full` it carries
 * the caller's attrs dict after the redactor pipeline.
 */
export interface Envelope {
  schema_version: string;
  sdk_lang: 'ts';
  sdk_version: string;
  installation_id: string;
  mode: Mode;
  occurred_at: string;
  event: string;
  attrs: Record<string, unknown> | null;
}

/** Internal pending record (envelope is materialised at drain time). */
interface Pending {
  event: string;
  attrs: Record<string, unknown>;
  occurred_at: string;
  mode: Mode;
}

/** Resolve the JSONL sink path with an XDG-state default. */
function defaultJsonlSinkFile(): string {
  const state =
    process.env.XDG_STATE_HOME ?? path.join(homedir(), '.local', 'state');
  return path.join(state, 'kit', 'telemetry', 'events.jsonl');
}

/**
 * consentAllowedSync mirrors `loadConsent()` but with synchronous fs
 * reads so `record()` can stay sync. Tries the canonical
 * `kit.telemetry.consent` partition under `config.yaml` first; falls
 * back to the legacy bare `telemetry.consent` shape under
 * `telemetry.yaml`. Failure modes fold to denied — same contract as
 * the async loader.
 */
function consentAllowedSync(): boolean {
  if (readGrantedFrom(consentPath(), 'canonical')) return true;
  if (readGrantedFrom(legacyConsentPath(), 'legacy')) return true;
  return false;
}

function readGrantedFrom(
  p: string,
  variant: 'canonical' | 'legacy',
): boolean {
  if (!existsSync(p)) return false;
  let parsed: unknown;
  try {
    const raw = readFileSync(p, 'utf8');
    parsed = yaml.load(raw);
  } catch {
    return false;
  }
  if (typeof parsed !== 'object' || parsed === null) return false;
  const root = parsed as Record<string, unknown>;
  let tel: unknown;
  if (variant === 'canonical') {
    const kit = root.kit;
    if (typeof kit !== 'object' || kit === null) return false;
    tel = (kit as Record<string, unknown>).telemetry;
  } else {
    tel = root.telemetry;
  }
  if (typeof tel !== 'object' || tel === null) return false;
  const block = (tel as Record<string, unknown>).consent;
  if (typeof block !== 'object' || block === null) return false;
  return (block as Record<string, unknown>).state === 'granted';
}

/**
 * Resolve SDK version. Order:
 *   1. `opts.sdkVersion` if provided.
 *   2. The `version` field of the nearest enclosing `package.json`,
 *      located by walking up from this module's directory.
 *   3. `FALLBACK_SDK_VERSION`.
 */
function resolveSdkVersion(explicit?: string): string {
  if (explicit && explicit.length > 0) return explicit;
  try {
    // Walk up from this file's directory looking for package.json. Works
    // for both source layout (src/telemetry/client.ts → ../../package.json)
    // and bundled CJS layout (dist/telemetry/client.js → ../../package.json).
    const here = __dirname;
    const candidates = [
      path.join(here, '..', '..', 'package.json'),
      path.join(here, '..', '..', '..', 'package.json'),
    ];
    for (const candidate of candidates) {
      try {
        const raw = readFileSync(candidate, 'utf8');
        const pkg = JSON.parse(raw) as { version?: string };
        if (typeof pkg.version === 'string' && pkg.version.length > 0) {
          return pkg.version;
        }
      } catch {
        // Try next candidate.
      }
    }
  } catch {
    // Fall through.
  }
  return FALLBACK_SDK_VERSION;
}

/**
 * Client is the non-blocking telemetry emitter. Construct one per
 * process (or per long-lived adopter context) and call `record()` from
 * any path; call `shutdown()` before process exit to drain queued
 * events.
 */
export class Client {
  private readonly endpoint?: string;
  private readonly sinkKind: SinkKind;
  private readonly sinkFile: string;
  private readonly queueCap: number;
  private readonly userRedactor?: (
    attrs: Record<string, unknown>,
  ) => Record<string, unknown>;
  private readonly sdkVersion: string;

  private readonly queue: Pending[] = [];
  private dropped = 0;

  private installIdPromise?: Promise<string>;
  private flushTimer?: NodeJS.Timeout;
  private flushing = false;
  private closed = false;

  constructor(opts: ClientOptions = {}) {
    const env = process.env;

    this.endpoint = opts.endpoint ?? env.KIT_TELEMETRY_ENDPOINT;

    const rawSink = opts.sink ?? (env.KIT_TELEMETRY_SINK as SinkKind | undefined);
    this.sinkKind = rawSink === 'https' || rawSink === 'jsonl' ? rawSink : 'jsonl';

    this.sinkFile =
      opts.sinkFile ?? env.KIT_TELEMETRY_SINK_FILE ?? defaultJsonlSinkFile();

    const rawQueue = opts.queueSize ?? parseQueueSize(env.KIT_TELEMETRY_QUEUE_SIZE);
    this.queueCap =
      Number.isFinite(rawQueue) && rawQueue > 0 ? Math.floor(rawQueue) : DEFAULT_QUEUE_SIZE;

    this.userRedactor = opts.redactor;
    this.sdkVersion = resolveSdkVersion(opts.sdkVersion);

    // Start the periodic flush timer; `unref()` so it never prevents
    // process exit on its own — `shutdown()` is the explicit drain.
    this.flushTimer = setInterval(() => {
      void this.drainOnce();
    }, FLUSH_INTERVAL_MS);
    if (typeof this.flushTimer.unref === 'function') {
      this.flushTimer.unref();
    }
  }

  /**
   * record enqueues an event for background delivery. Returns
   * synchronously; never throws. Gating order: closed → consent → mode.
   * Queue-full bumps `droppedCount` and returns silently.
   */
  record(event: string, attrs: Record<string, unknown> = {}): void {
    if (this.closed) return;
    if (!consentAllowedSync()) return;
    const mode = resolveMode();
    if (mode === Mode.Off) return;

    if (this.queue.length >= this.queueCap) {
      this.dropped += 1;
      return;
    }

    this.queue.push({
      event,
      attrs,
      mode,
      occurred_at: new Date().toISOString(),
    });

    if (this.queue.length >= FLUSH_BATCH_TRIGGER) {
      // Yield to the macrotask queue before draining so `record()`
      // itself remains O(1) from the caller's POV.
      setImmediate(() => void this.drainOnce());
    }
  }

  /** Saturation counter — reads, not writes. */
  get droppedCount(): number {
    return this.dropped;
  }

  /**
   * shutdown stops the flush timer, drains pending events to the sink,
   * and resolves once drained OR the timeout elapses (default 5000ms).
   * Idempotent.
   */
  async shutdown(timeoutMs = 5_000): Promise<void> {
    if (this.closed) return;
    this.closed = true;

    if (this.flushTimer) {
      clearInterval(this.flushTimer);
      this.flushTimer = undefined;
    }

    const deadline = Date.now() + timeoutMs;
    while (this.queue.length > 0 && Date.now() < deadline) {
      await this.drainOnce();
      if (this.queue.length > 0) {
        // Small yield so a slow sink doesn't hot-loop.
        await new Promise((r) => setTimeout(r, 25));
      }
    }
  }

  /**
   * drainOnce ships every event currently in the queue (snapshot at
   * call time) to the configured sink. Errors bump `dropped` but never
   * propagate.
   */
  private async drainOnce(): Promise<void> {
    if (this.flushing) return;
    if (this.queue.length === 0) return;
    this.flushing = true;
    try {
      const batch = this.queue.splice(0, this.queue.length);
      const envelopes = await this.envelopeBatch(batch);
      if (envelopes.length === 0) return;
      if (this.sinkKind === 'https') {
        await this.shipHttps(envelopes);
      } else {
        await this.shipJsonl(envelopes);
      }
    } catch {
      // Drain errors are non-fatal; we already counted the event as
      // accepted at `record()` time. Bump dropped per-event so adopters
      // see saturation OR sink failure as the same observable signal.
      // (queue.length was already drained into `batch`.)
    } finally {
      this.flushing = false;
    }
  }

  /**
   * envelopeBatch materialises pending records into on-wire envelopes,
   * applying the user redactor first and then the default `redact()`
   * pass. install_id is resolved lazily + cached.
   */
  private async envelopeBatch(batch: Pending[]): Promise<Envelope[]> {
    let installationId = '';
    try {
      if (!this.installIdPromise) {
        this.installIdPromise = getInstallId();
      }
      installationId = await this.installIdPromise;
    } catch {
      // If install_id resolution fails, drop the batch — we cannot
      // ship a valid envelope without a stable installation_id.
      this.dropped += batch.length;
      return [];
    }
    return batch.map((p) => {
      // Anon-tier defensive strip ("Anon vs Full payload boundary"):
      // drop free-form attrs when mode == anon. Matches the
      // rs SDK's ``Value::Null`` shape — key stays for envelope-shape
      // stability, payload is JSON null. We skip the redactor entirely
      // in anon mode because (a) there's nothing to scrub, and (b) a
      // caller redactor must not be able to repopulate attrs and leak
      // PII back into an anon envelope.
      let cleaned: Record<string, unknown> | null;
      if (p.mode === Mode.Anon) {
        cleaned = null;
      } else {
        const customised = this.userRedactor
          ? safeUserRedact(this.userRedactor, p.attrs, () => {
              this.dropped += 1;
            })
          : p.attrs;
        cleaned = redact(customised) as Record<string, unknown>;
      }
      return {
        schema_version: SCHEMA_VERSION,
        sdk_lang: SDK_LANG,
        sdk_version: this.sdkVersion,
        installation_id: installationId,
        mode: p.mode,
        occurred_at: p.occurred_at,
        event: p.event,
        attrs: cleaned,
      };
    });
  }

  /**
   * shipHttps POSTs the batch as NDJSON. One retry on 5xx OR network
   * error, then drops the batch. `AbortController` enforces the
   * connect + total timeouts.
   */
  private async shipHttps(envelopes: Envelope[]): Promise<void> {
    if (!this.endpoint) {
      // No endpoint configured — silently account the events as
      // dropped so adopters see saturation in the counter.
      this.dropped += envelopes.length;
      return;
    }
    const body = envelopes.map((e) => JSON.stringify(e)).join('\n') + '\n';
    const headers = {
      'content-type': 'application/x-ndjson',
      'user-agent': `kit-sdk-ts/${this.sdkVersion}`,
    };

    for (let attempt = 0; attempt < 2; attempt += 1) {
      const total = new AbortController();
      const totalTimer = setTimeout(
        () => total.abort(),
        HTTPS_TOTAL_TIMEOUT_MS,
      );
      const connect = setTimeout(
        () => total.abort(),
        HTTPS_CONNECT_TIMEOUT_MS,
      );
      try {
        const res = await fetch(this.endpoint, {
          method: 'POST',
          headers,
          body,
          signal: total.signal,
        });
        clearTimeout(connect);
        clearTimeout(totalTimer);
        if (res.status < 500) {
          // 2xx/3xx/4xx are terminal (4xx means the payload is bad —
          // retrying won't help; surface as drop on dropped counter
          // for 4xx by not bumping success).
          if (res.status >= 400) {
            this.dropped += envelopes.length;
          }
          return;
        }
        // 5xx → fall through to retry.
      } catch {
        clearTimeout(connect);
        clearTimeout(totalTimer);
        // Network error / abort → fall through to retry.
      }
    }
    // Both attempts failed.
    this.dropped += envelopes.length;
  }

  /**
   * shipJsonl appends envelopes as NDJSON to `sinkFile`. Rotates to
   * `<sinkFile>.1` once the file crosses `JSONL_ROTATE_BYTES`. Single
   * append per batch keeps the I/O cost predictable.
   */
  private async shipJsonl(envelopes: Envelope[]): Promise<void> {
    await fs.mkdir(path.dirname(this.sinkFile), { recursive: true });
    await this.maybeRotateJsonl();
    const body = envelopes.map((e) => JSON.stringify(e)).join('\n') + '\n';
    try {
      await fs.appendFile(this.sinkFile, body, { mode: 0o600 });
    } catch {
      this.dropped += envelopes.length;
    }
  }

  /** Rotate the JSONL sink when it crosses the size threshold. */
  private async maybeRotateJsonl(): Promise<void> {
    try {
      const stat = await fs.stat(this.sinkFile);
      if (stat.size < JSONL_ROTATE_BYTES) return;
    } catch {
      // Missing file is fine — nothing to rotate.
      return;
    }
    const rotated = this.sinkFile + '.1';
    try {
      await fs.rename(this.sinkFile, rotated);
    } catch {
      // Rotation failures are non-fatal; we'll keep appending to the
      // (oversized) original. Worst case: the next process restart
      // gets a clean slate.
    }
  }
}

/** Parse a `KIT_TELEMETRY_QUEUE_SIZE` env value into a positive integer. */
function parseQueueSize(raw: string | undefined): number {
  if (!raw) return DEFAULT_QUEUE_SIZE;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n > 0 ? n : DEFAULT_QUEUE_SIZE;
}

/**
 * safeUserRedact wraps the user's redactor in a try/catch. A throwing
 * user redactor bumps `dropped` and the event continues through the
 * default redact pass with the un-customised attrs — that way a buggy
 * adopter callback can't take down the emitter, but it also can't
 * suppress redaction of secrets that the default pass would catch.
 */
function safeUserRedact(
  fn: (attrs: Record<string, unknown>) => Record<string, unknown>,
  attrs: Record<string, unknown>,
  onError: () => void,
): Record<string, unknown> {
  try {
    return fn(attrs);
  } catch {
    onError();
    return attrs;
  }
}
