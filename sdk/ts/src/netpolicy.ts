/**
 * @module netpolicy
 * @package @hop-top/kit
 *
 * Owns the process-wide network policy marker and the `fetch` wrapper that
 * enforces it.
 *
 * The `--offline` global (cli-parity-guide, "Global Flags") promises that
 * network access is disabled. Marking a scope alone cannot keep that
 * promise: it is advisory, so any caller that forgets to consult it still
 * reaches the wire. {@link guardFetch} closes that gap by refusing the
 * request inside the fetch implementation, beneath every caller, where no
 * naive leaf can route around it.
 *
 * The marker lives in its own module rather than in `cli` so library code
 * can enforce the policy without importing the CLI factory back.
 *
 * Loopback is deliberately exempt. `--offline` means "do not talk to the
 * network", not "do not talk to myself": a local `kit serve` peer, a dev
 * backend on 127.0.0.1 and unix sockets stay reachable so offline
 * workflows remain usable.
 *
 * ## Scope
 *
 * {@link install} wraps `globalThis.fetch`, so it covers HTTP and HTTPS
 * through the fetch API — which is every network client in kit today
 * (`api`, `aim`, `upgrade`, `routellm`, `triton`, `telemetry`, and the
 * ConnectRPC transport, which resolves `globalThis.fetch` lazily per
 * call). undici's `setGlobalDispatcher` is not used: undici is not a
 * dependency of this package, so the dispatcher hook is unavailable
 * without adding one, and it would not cover callers that inject their
 * own `fetch` anyway.
 *
 * It does NOT cover code that opens a socket directly: `node:net`,
 * `node:http`/`node:https` (`http.request`), `node:dgram`, database
 * drivers, or raw TLS. Nor does it cover a caller that captured a
 * reference to the unguarded `fetch` before {@link install} ran, or one
 * that injects its own transport (`APIClient({ fetch })`). For those,
 * `--offline` remains advisory and the call site must consult
 * {@link isOffline} itself, or wrap its own transport with
 * {@link guardFetch}. Closing that gap needs a guarded dialer threaded
 * through each such client; it is deliberately not attempted here.
 */

import { AsyncLocalStorage } from 'node:async_hooks';

/**
 * Name carried by the error {@link guardFetch} rejects with when a request
 * is attempted in an offline-marked scope. JavaScript has no `errors.Is`,
 * so the name is the matchable analogue of Go's sentinel `ErrOffline`;
 * prefer {@link isOfflineError} over comparing it by hand.
 */
export const ErrOffline = 'OfflineError';

/** Error rejected by {@link guardFetch}. Matchable via {@link isOfflineError}. */
export class OfflineError extends Error {
  override readonly name = ErrOffline;

  constructor(method: string, url: string) {
    super(`${method} ${url}: network disabled by --offline`);
  }
}

/**
 * Report whether err is the refusal raised by the offline guard. Matches on
 * the error name rather than `instanceof` so a copy of this module loaded
 * through a different resolution path (bundled adopter, dual dist/src
 * graph) still matches — the JS analogue of `errors.Is`.
 */
export function isOfflineError(err: unknown): boolean {
  return err instanceof Error && err.name === ErrOffline;
}

// ---------------------------------------------------------------------------
// Marker
// ---------------------------------------------------------------------------

/**
 * Per-invocation marker. AsyncLocalStorage is the TypeScript analogue of
 * Go's `context.Context` value: it propagates across `await` boundaries
 * into the guarded fetch without threading a parameter through every
 * library call.
 */
const store = new AsyncLocalStorage<boolean>();

/** Set when the policy applies process-wide rather than to one async scope. */
let processOffline = false;

/**
 * Run fn in a scope marked offline. Passing false runs fn unchanged so an
 * unmarked scope stays clean, mirroring Go's `WithOffline(ctx, false)`.
 */
export function withOffline<T>(offline: boolean, fn: () => T): T {
  if (!offline) return fn();
  return store.run(true, fn);
}

/**
 * Mark the whole process offline. The CLI uses this rather than
 * {@link withOffline} because commander dispatches an action handler
 * without giving the framework a scope to wrap around it, and because a
 * detached callback (a timer, an unawaited promise) must stay refused.
 * Mirrors the process-global reach of Go's `netpolicy.Install`.
 */
export function setProcessOffline(offline: boolean): void {
  processOffline = offline;
}

/**
 * Report whether the caller is running under the offline marker — either
 * an enclosing {@link withOffline} scope or a process-wide
 * {@link setProcessOffline}.
 */
export function isOffline(): boolean {
  return processOffline || store.getStore() === true;
}

// ---------------------------------------------------------------------------
// Enforcement
// ---------------------------------------------------------------------------

/**
 * Report whether host names a loopback address. Hosts that are not literal
 * IPs (DNS names) are treated as remote: resolving them would itself be
 * network access.
 */
function isLoopback(hostname: string): boolean {
  // URL.hostname keeps IPv6 literals bracketed and lower-cases the rest.
  const h = hostname.replace(/^\[|\]$/g, '');
  if (h === 'localhost') return true;
  if (h === '::1') return true;
  // IPv4 loopback is the whole 127.0.0.0/8 block.
  const v4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(h);
  if (v4) {
    return v4.slice(1).every((o) => Number(o) <= 255) && Number(v4[1]) === 127;
  }
  // IPv4-mapped IPv6 loopback, in either textual form.
  const mapped = /^::ffff:(.+)$/i.exec(h);
  if (mapped) {
    const inner = mapped[1] ?? '';
    if (/^7f00:1$/i.test(inner)) return true;
    return isLoopback(inner);
  }
  return false;
}

/** Resolve the method and absolute URL a fetch call targets. */
function target(input: RequestInfo | URL, init?: RequestInit): { method: string; url: string } {
  if (typeof Request !== 'undefined' && input instanceof Request) {
    return { method: init?.method ?? input.method, url: input.url };
  }
  return { method: init?.method ?? 'GET', url: String(input) };
}

/** Marks a fetch implementation as already guarded, so wrapping is idempotent. */
const guarded = Symbol.for('hop.top/kit/netpolicy.guarded');

/**
 * Wrap base so requests made in an offline-marked scope reject with an
 * {@link OfflineError} instead of reaching the network. Wrapping is
 * idempotent: guarding an already guarded fetch returns it unchanged.
 *
 * A URL that does not parse is refused when offline: an unparseable target
 * cannot be shown to be loopback, and the policy fails closed.
 */
export function guardFetch(base: typeof globalThis.fetch): typeof globalThis.fetch {
  if ((base as { [guarded]?: boolean })[guarded]) return base;

  const wrapper: typeof globalThis.fetch = async (input, init) => {
    if (isOffline()) {
      const { method, url } = target(input, init);
      let loopback = false;
      try {
        loopback = isLoopback(new URL(url).hostname);
      } catch {
        loopback = false; // fail closed on an unparseable target
      }
      if (!loopback) throw new OfflineError(method, url);
    }
    return base(input, init);
  };

  (wrapper as { [guarded]?: boolean })[guarded] = true;
  return wrapper;
}

/**
 * Wrap `globalThis.fetch` with {@link guardFetch}, so every caller that
 * does not inject its own transport — the common case across kit and
 * adopter code — enforces the policy without a per-site change.
 *
 * Idempotent and safe to call more than once. Call it once during process
 * start-up (`createCLI` does this) and never concurrently with in-flight
 * requests: it mutates a process-global.
 *
 * Callers that DO inject their own fetch must wrap it themselves with
 * {@link guardFetch}; install cannot reach them.
 */
export function install(): void {
  globalThis.fetch = guardFetch(globalThis.fetch);
}
