/**
 * @module serve
 * @package @hop-top/kit
 *
 * Serve hierarchy and service lifecycle — the TypeScript port of the
 * contract in `docs/contracts/serve-lifecycle.md`, §"Cross-language
 * parity".
 *
 * `<tool> serve` supervises every configured and enabled service;
 * `<tool> serve <service>` selects exactly one and overrides aggregate
 * enablement. Both forms share one lifecycle implementation, so a
 * service started by the selector observes the same readiness,
 * shutdown, and exit semantics as the same service started by the
 * supervisor.
 *
 * The Go reference is `go/console/serve`. This module mirrors the
 * behavior the contract makes cross-language, and deliberately does
 * not mirror what the contract rules out — there is no REST/OpenAPI
 * projection, no socket service, no command-tree reflection, and no
 * permission/provenance/audit surface here, because those depend on
 * Go-only machinery.
 *
 * @example
 * ```ts
 * import { createCLI } from '@hop-top/kit/cli';
 * import { ServiceRegistry, registerServe } from '@hop-top/kit/serve';
 *
 * const registry = new ServiceRegistry();
 * registry.register({
 *   name: 'api',
 *   async start(signal, ready) {
 *     ready();
 *     await new Promise<void>(r => signal.addEventListener('abort', () => r(), { once: true }));
 *   },
 *   ready: () => true,
 *   async stop() {},
 * });
 *
 * const { program } = createCLI({ name: 'mytool', version: '1.0.0', description: 'My tool' });
 * registerServe(program, { registry, configs: { api: { enabled: true } } });
 * program.parse();
 * ```
 */

import type { Command } from 'commander';

import {
  CODE_GENERIC,
  CODE_NOT_FOUND,
  CODE_OK,
  CODE_TRANSIENT,
  CODE_UNAUTHORIZED,
  CODE_USAGE,
  type CliError,
  notFoundError,
  unauthorizedError,
  usageError,
} from './output/error.js';

// ---------------------------------------------------------------------------
// Naming
// ---------------------------------------------------------------------------

/**
 * Service identifier grammar: lowercase ASCII, digits, and internal
 * hyphens. An identifier is a CLI word, a config key segment, and an
 * event payload value at once, which is why the grammar is contract
 * rather than a local convention.
 */
export const NAME_PATTERN = /^[a-z][a-z0-9-]*$/;

/**
 * Names reserved for selector vocabulary. Registering one would make
 * `serve <name>` ambiguous with a future aggregate form, and is why
 * `--list` is a flag rather than a `serve list` child.
 */
export const RESERVED_NAMES: readonly string[] = ['all', 'none', 'list'];

/** Reports whether `name` is one of the reserved selector words. */
export function isReservedName(name: string): boolean {
  return RESERVED_NAMES.includes(name);
}

/**
 * Validates a service identifier, returning an error message or
 * `null`. Mirrors Go's `serve.ValidateName`.
 */
export function validateName(name: string): string | null {
  if (name === '') return 'serve: service name is empty';
  if (!NAME_PATTERN.test(name)) {
    return `serve: service name "${name}" must be lowercase letters, ` +
      'digits, or hyphens, starting with a letter';
  }
  if (isReservedName(name)) return `serve: service name "${name}" is reserved`;
  return null;
}

// ---------------------------------------------------------------------------
// Registration seam
// ---------------------------------------------------------------------------

/**
 * One long-running thing a tool can serve.
 *
 * The four required capabilities are the contract's minimum: a name, a
 * start that runs until aborted or failed, a readiness report, and a
 * stop. Go expresses them as an interface; here they are properties on
 * a plain object, which the contract explicitly permits — what is
 * fixed is the capability set and each one's behavior, not a method
 * table.
 */
export interface Service {
  /**
   * Stable service identifier. Must satisfy {@link validateName} and
   * must not change across releases: renaming one is a breaking change
   * to the command surface, the config file, and any subscriber.
   */
  name: string;

  /**
   * Begins serving. Resolves when `signal` aborts (a clean stop) and
   * rejects when the service fails.
   *
   * `ready` must be called exactly once, after every acquisition that
   * can fail deterministically has succeeded — the listener bound, the
   * file created, the subscription attached. Calling it more than once
   * is ignored rather than an error; the supervisor reports ready at
   * most once per start either way.
   */
  start(signal: AbortSignal, ready: () => void): Promise<void>;

  /**
   * Whether the service is currently accepting work. Readiness, not
   * liveness: a ready service may be idle, and may later fail.
   */
  ready(): boolean;

  /**
   * Drains in-flight work and releases resources. The supervisor
   * bounds it by the stop timeout and abandons a stop that exceeds it,
   * so an implementation must respect `signal` rather than assume it
   * will be allowed to finish.
   */
  stop(signal: AbortSignal): Promise<void>;

  /**
   * Optional configuration gate — the second of the three validation
   * gates. Returns an error message, or `null` when the resolved
   * configuration is complete and usable.
   */
  validate?(): string | null;

  /**
   * Optional ordering declaration. Start order is topological over
   * these, ties broken by registration order; stop order is the exact
   * reverse of the order services actually started.
   */
  dependsOn?(): string[];

  /**
   * Optional address declaration. Read once the service reports ready
   * and carried into the readiness event, so an operator learns where
   * the service actually bound — including a port the kernel picked
   * for a wildcard address, which configuration cannot reveal.
   */
  addr?(): string;

  /**
   * Optional policy declaration: the `kit/side-effect` and
   * `kit/network` classes, in that order. A service that omits it is
   * unclassified and passes the policy gate.
   */
  class?(): [sideEffect: string, network: string];
}

/**
 * The third validation gate. A service whose declared class the gate
 * denies is refused at `UNAUTHORIZED`, exit 5.
 *
 * The contract requires the gate, not Go's YAML-driven
 * side-effect × network table: a two-argument predicate satisfies it.
 * A registry with no gate passes every service, because a tool that
 * has wired no policy has expressed no restriction.
 */
export interface PolicyGate {
  allow(sideEffect: string, network: string): { ok: boolean; reason?: string };
}

/**
 * Thrown when a registration is rejected at construction time.
 *
 * A duplicate name or an invalid one is a wiring bug in the tool's
 * entry point, not a runtime condition: it surfaces on the first run
 * rather than at the first `serve`, and there is no last-writer-wins
 * path. Go panics; throwing is this port's equivalent. What the
 * contract forbids is letting execution survive it as a warning.
 */
export class ServiceRegistrationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ServiceRegistrationError';
  }
}

/**
 * The seam kit-owned and adopter-owned services register into. A tool
 * builds one before the root command parses; the supervisor reads it.
 */
export class ServiceRegistry {
  private readonly byName = new Map<string, Service>();
  private readonly order: string[] = [];

  /**
   * Adds `svc` under its name.
   *
   * Throws on an invalid name and on a duplicate. An adopter
   * deliberately replacing a kit-shipped service calls
   * {@link ServiceRegistry.override} instead — the documented escape
   * hatch, and the only path that accepts a duplicate name.
   */
  register(svc: Service): void {
    const invalid = validateName(svc.name);
    if (invalid) throw new ServiceRegistrationError(invalid);
    if (this.byName.has(svc.name)) {
      throw new ServiceRegistrationError(
        `serve: service "${svc.name}" already registered (use override to replace)`,
      );
    }
    this.byName.set(svc.name, svc);
    this.order.push(svc.name);
  }

  /**
   * Registers `svc`, replacing any service already under its name and
   * keeping that name's original position in {@link list}.
   *
   * Still throws on an invalid name: override lifts the collision
   * rule, not the grammar.
   */
  override(svc: Service): void {
    const invalid = validateName(svc.name);
    if (invalid) throw new ServiceRegistrationError(invalid);
    if (!this.byName.has(svc.name)) this.order.push(svc.name);
    this.byName.set(svc.name, svc);
  }

  /** The service registered under `name`, if any. */
  lookup(name: string): Service | undefined {
    return this.byName.get(name);
  }

  /** Every registered identifier, in registration order. */
  names(): string[] {
    return [...this.order];
  }

  /** Every registered service, in registration order. */
  list(): Service[] {
    return this.order.map((n) => this.byName.get(n)!);
  }

  /** Number of registered services. */
  get size(): number {
    return this.byName.size;
  }
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

/** Default budget from start to readiness. `services.<name>.ready_timeout`. */
export const DEFAULT_READY_TIMEOUT_MS = 30_000;
/** Default budget for one stop. `services.<name>.stop_timeout`. */
export const DEFAULT_STOP_TIMEOUT_MS = 30_000;
/** Default total shutdown budget. `services.shutdown_timeout`. */
export const DEFAULT_SHUTDOWN_TIMEOUT_MS = 60_000;

/**
 * The resolved `services.<name>` block for one service. Only the
 * lifecycle keys are modeled; service-specific keys live in the same
 * block and are read by the service itself.
 */
export interface ServiceConfig {
  /**
   * `services.<name>.enabled`. Decides whether the supervisor form
   * starts this service. Defaults to `false`: a service that starts
   * listening because a dependency upgrade added it to the registry is
   * an unrequested open port.
   */
  enabled?: boolean;
  /** `services.<name>.ready_timeout`, in milliseconds. */
  readyTimeoutMs?: number;
  /** `services.<name>.stop_timeout`, in milliseconds. */
  stopTimeoutMs?: number;
}

/** The supervisor's answer to "one service failed while others run". */
export type FailurePolicy = 'fail-fast' | 'isolate';

/** `services.failure_policy` default. */
export const DEFAULT_FAILURE_POLICY: FailurePolicy = 'fail-fast';

/** Reports whether `p` is a declared failure policy. */
export function isFailurePolicy(p: string): p is FailurePolicy {
  return p === 'fail-fast' || p === 'isolate';
}

/** The supervisor-scoped half of the `services` block. */
export interface SupervisorConfig {
  failurePolicy?: FailurePolicy;
  shutdownTimeoutMs?: number;
}

// ---------------------------------------------------------------------------
// Outcomes and exit codes
// ---------------------------------------------------------------------------

/** The kinds of ending a serve run can have. */
export type LifecycleOutcome =
  | 'clean-stop'
  | 'invalid-selection'
  | 'config-invalid'
  | 'no-services'
  | 'unknown-service'
  | 'policy-denied'
  | 'start-failed'
  | 'runtime-crash'
  | 'shutdown-timeout';

/**
 * The contract's exit-behavior table, verbatim. Codes come from the
 * shared taxonomy in `output/error`; this module allocates no new
 * numbers.
 *
 * `start-failed` and `runtime-crash` share exit 1 deliberately: they
 * differ in *when*, not in what an operator does next, and the
 * distinguishing detail belongs in the message and the failed event
 * rather than in a second numeric code.
 */
const EXIT_TABLE: Record<LifecycleOutcome, { code: string; exit: number }> = {
  'clean-stop': { code: CODE_OK, exit: 0 },
  'invalid-selection': { code: CODE_USAGE, exit: 2 },
  'config-invalid': { code: CODE_USAGE, exit: 2 },
  'no-services': { code: CODE_USAGE, exit: 2 },
  'unknown-service': { code: CODE_NOT_FOUND, exit: 3 },
  'policy-denied': { code: CODE_UNAUTHORIZED, exit: 5 },
  'start-failed': { code: CODE_GENERIC, exit: 1 },
  'runtime-crash': { code: CODE_GENERIC, exit: 1 },
  'shutdown-timeout': { code: CODE_GENERIC, exit: 1 },
};

/**
 * Process exit code for `o`. An outcome with no table row is treated
 * as a generic failure rather than a success, so a kind added without
 * a row fails loudly instead of silently exiting 0.
 */
export function exitCodeFor(o: LifecycleOutcome): number {
  return EXIT_TABLE[o]?.exit ?? 1;
}

/** The `CODE_*` string for `o`, for the rendered error envelope. */
export function codeFor(o: LifecycleOutcome): string {
  return EXIT_TABLE[o]?.code ?? CODE_GENERIC;
}

/** Whether `o` exits non-zero. */
export function isFailure(o: LifecycleOutcome): boolean {
  return exitCodeFor(o) !== 0;
}

/**
 * The outcome the process should exit on given everything observed.
 *
 * "Worst" is severity, not exit-code magnitude: any failure beats a
 * clean stop, and among failures the first observed wins, because it
 * is the one that explains the rest. Under `isolate` a process may
 * survive several failures, and the exit code must reflect the worst
 * outcome across the whole run rather than the last one.
 */
export function worstOutcome(observed: readonly LifecycleOutcome[]): LifecycleOutcome {
  let worst: LifecycleOutcome = 'clean-stop';
  for (const o of observed) {
    if (isFailure(o) && !isFailure(worst)) worst = o;
  }
  return worst;
}

// ---------------------------------------------------------------------------
// Resolution — the hierarchy and the override rule
// ---------------------------------------------------------------------------

/** A parsed `serve` invocation. */
export interface ResolveRequest {
  /**
   * Positional arguments after the `serve` word. Empty is the
   * supervisor form, exactly one the selector form, two or more a
   * usage error.
   */
  args: readonly string[];
  /**
   * Resolved `services.<name>` block per service. A service with no
   * entry is not configured, and the supervisor form skips it.
   */
  configs?: Readonly<Record<string, ServiceConfig>>;
  /** The third validation gate. Omitted passes everything. */
  policy?: PolicyGate;
}

/** The result of resolving a request against a registry. */
export interface ResolveOutcome {
  /** Identifiers to run, in registration order. Empty when `error` is set. */
  selected: string[];
  /**
   * True when the selector form was used. Under it `selected` holds
   * exactly one name and aggregate enablement was overridden rather
   * than consulted.
   */
  explicit: boolean;
  /**
   * Configured-but-disabled services the supervisor form passed over.
   * Skipping is not an error and must not affect the exit code.
   */
  skipped: string[];
  /** The refusal, already carrying its code and exit code. */
  error?: CliError;
  /** The outcome the refusal corresponds to. */
  outcome?: LifecycleOutcome;
}

/**
 * Turns a `serve` invocation into a runnable set, applying the
 * hierarchy and the override rule. Pure: nothing is started, nothing
 * binds, nothing is written.
 *
 * Selector form runs the named service **even when
 * `services.<name>.enabled` is false**, provided all three gates pass
 * in order — registration, then configuration, then policy.
 * Enablement is not a gate there: an operator naming a service on the
 * command line has already made the decision the flag exists to
 * automate.
 *
 * Supervisor form runs every service that is both configured and
 * enabled, in registration order, skipping a disabled one silently.
 * Resolving to zero services is a usage error, not a clean exit: a
 * process that exits 0 without listening is indistinguishable from a
 * successful start to systemd or a container runtime.
 */
export function resolve(
  registry: ServiceRegistry,
  req: ResolveRequest,
): ResolveOutcome {
  const base: ResolveOutcome = { selected: [], explicit: false, skipped: [] };

  if (req.args.length > 1) {
    return {
      ...base,
      outcome: 'invalid-selection',
      error: usageError(
        `serve accepts at most one service name, got ${req.args.length}`,
      ),
    };
  }
  if (req.args.length === 1) {
    return resolveExplicit(registry, req, req.args[0]!);
  }
  return resolveAggregate(registry, req);
}

/** The selector form and its override rule. */
function resolveExplicit(
  registry: ServiceRegistry,
  req: ResolveRequest,
  name: string,
): ResolveOutcome {
  const base: ResolveOutcome = { selected: [], explicit: true, skipped: [] };

  // Gate 1: registration.
  const svc = registry.lookup(name);
  if (!svc) {
    const known = registry.names();
    const err = notFoundError(
      `unknown service "${name}"; known: ${known.join(', ')}`,
    );
    const fix = nearestName(name, known);
    if (fix) err.suggested_fix = `did you mean "${fix}"?`;
    return { ...base, outcome: 'unknown-service', error: err };
  }

  // Gate 2: configuration.
  const invalid = validateConfig(svc);
  if (invalid) {
    return {
      ...base,
      outcome: 'config-invalid',
      error: usageError(`service "${name}": ${invalid}`),
    };
  }

  // Gate 3: policy.
  const denied = checkPolicy(req.policy, svc);
  if (denied) return { ...base, outcome: 'policy-denied', error: denied };

  // Enablement is deliberately not consulted here.
  return { selected: [name], explicit: true, skipped: [] };
}

/** The supervisor form. */
function resolveAggregate(
  registry: ServiceRegistry,
  req: ResolveRequest,
): ResolveOutcome {
  const out: ResolveOutcome = { selected: [], explicit: false, skipped: [] };
  const configs = req.configs ?? {};

  for (const name of registry.names()) {
    const cfg = configs[name];
    if (cfg === undefined) continue; // not configured
    if (cfg.enabled !== true) {
      out.skipped.push(name);
      continue;
    }
    const svc = registry.lookup(name);
    if (!svc) continue;

    const invalid = validateConfig(svc);
    if (invalid) {
      return {
        selected: [], explicit: false, skipped: out.skipped,
        outcome: 'config-invalid',
        error: usageError(`service "${name}": ${invalid}`),
      };
    }
    const denied = checkPolicy(req.policy, svc);
    if (denied) {
      return {
        selected: [], explicit: false, skipped: out.skipped,
        outcome: 'policy-denied', error: denied,
      };
    }
    out.selected.push(name);
  }

  if (out.selected.length === 0) {
    const err = usageError(
      'no services configured and enabled; enable one under services.* ' +
      'or name one explicitly',
    );
    err.suggested_fix = 'set services.<name>.enabled: true, or run: serve <service>';
    out.error = err;
    out.outcome = 'no-services';
  }
  return out;
}

function validateConfig(svc: Service): string | null {
  return svc.validate ? svc.validate() : null;
}

function checkPolicy(
  gate: PolicyGate | undefined,
  svc: Service,
): CliError | null {
  if (!gate || !svc.class) return null;
  const [sideEffect, network] = svc.class();
  const verdict = gate.allow(sideEffect, network);
  if (verdict.ok) return null;
  let msg =
    `service "${svc.name}" denied by policy ` +
    `(side_effect=${sideEffect}, network=${network})`;
  if (verdict.reason) msg += `: ${verdict.reason}`;
  return unauthorizedError(msg);
}

/**
 * The registered name closest to `want` by edit distance, or `null`
 * when nothing is close enough to suggest. The threshold scales with
 * the typed word's length so a short name does not attract an
 * unrelated suggestion.
 */
function nearestName(want: string, known: readonly string[]): string | null {
  let best: string | null = null;
  let bestDist = -1;
  const limit = Math.floor(want.length / 2) + 1;
  for (const k of [...known].sort()) {
    const d = editDistance(want, k);
    if (d > limit) continue;
    if (bestDist === -1 || d < bestDist) {
      best = k;
      bestDist = d;
    }
  }
  return best;
}

function editDistance(a: string, b: string): number {
  let prev = Array.from({ length: b.length + 1 }, (_, j) => j);
  let cur = new Array<number>(b.length + 1).fill(0);
  for (let i = 1; i <= a.length; i++) {
    cur[0] = i;
    for (let j = 1; j <= b.length; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      cur[j] = Math.min(prev[j]! + 1, cur[j - 1]! + 1, prev[j - 1]! + cost);
    }
    [prev, cur] = [cur, prev];
  }
  return prev[b.length]!;
}

// ---------------------------------------------------------------------------
// Ordering
// ---------------------------------------------------------------------------

/**
 * `selected` in topological order over the optional `dependsOn`
 * declarations, ties broken by the order in `selected` (which
 * {@link resolve} returns in registration order).
 *
 * A dependency naming a service outside `selected` is ignored rather
 * than an error: under the selector form exactly one service runs, and
 * its dependencies are the operator's business, not a reason to refuse
 * a deliberate single-service start.
 *
 * A dependency cycle throws, in the same class as a name collision: it
 * is a wiring bug that can only be fixed by editing the registrations,
 * and there is no order the supervisor could pick that would be right.
 */
export function startOrder(
  registry: ServiceRegistry,
  selected: readonly string[],
): string[] {
  const inSet = new Set(selected);
  const deps = new Map<string, string[]>();

  for (const name of selected) {
    const svc = registry.lookup(name);
    if (!svc?.dependsOn) continue;
    const want = svc.dependsOn().filter((d) => inSet.has(d) && d !== name);
    if (want.length > 0) deps.set(name, want);
  }

  const WHITE = 0, GREY = 1, BLACK = 2;
  const mark = new Map<string, number>();
  const out: string[] = [];

  const visit = (name: string, path: string[]): void => {
    const state = mark.get(name) ?? WHITE;
    if (state === BLACK) return;
    if (state === GREY) {
      throw new ServiceRegistrationError(
        `serve: dependency cycle: ${[...path, name].join(' -> ')}`,
      );
    }
    mark.set(name, GREY);
    for (const want of deps.get(name) ?? []) visit(want, [...path, name]);
    mark.set(name, BLACK);
    out.push(name);
  };

  for (const name of selected) visit(name, []);
  return out;
}

// ---------------------------------------------------------------------------
// Lifecycle events
// ---------------------------------------------------------------------------

/** The 2-segment source.category prefix serve events publish under. */
export const DEFAULT_TOPIC_PREFIX = 'kit.serve';

/** Action segments. A bare `ready` does not validate; do not use one. */
export const ACTION_STARTED = 'started';
export const ACTION_READY_REPORTED = 'ready_reported';
export const ACTION_FAILED = 'failed';
export const ACTION_STOPPED = 'stopped';

/**
 * Object segments. The service identifier travels in the payload, not
 * the topic, so subscribers are not forced to re-bind when a tool
 * gains a service.
 */
export const OBJECT_SERVICE = 'service';
export const OBJECT_SUPERVISOR = 'supervisor';

/**
 * The conformant topic set for `prefix`, keyed `<object>.<action>`.
 * These strings are contract: a subscriber is written against them and
 * does not know which language published.
 */
export function defaultTopics(
  prefix: string = DEFAULT_TOPIC_PREFIX,
): Record<string, string> {
  const p = prefix === '' ? DEFAULT_TOPIC_PREFIX : prefix;
  const out: Record<string, string> = {};
  for (const [object, action] of [
    [OBJECT_SERVICE, ACTION_STARTED],
    [OBJECT_SERVICE, ACTION_READY_REPORTED],
    [OBJECT_SERVICE, ACTION_FAILED],
    [OBJECT_SERVICE, ACTION_STOPPED],
    [OBJECT_SUPERVISOR, ACTION_READY_REPORTED],
    [OBJECT_SUPERVISOR, ACTION_STOPPED],
  ] as const) {
    out[`${object}.${action}`] = `${p}.${object}.${action}`;
  }
  return out;
}

/** The body of every serve lifecycle event. */
export interface EventPayload {
  /** Service the event concerns. Absent for supervisor-scoped events. */
  service?: string;
  /** Failure text for a `failed` event. Never in the topic. */
  error?: string;
  /** Why, for events that have a reason. */
  reason?: string;
  /** Milliseconds since the supervisor began the run. */
  elapsed_ms: number;
  /** Where the service accepts work, on `ready_reported` only. */
  address?: string;
}

/**
 * The narrow slice of a bus the supervisor needs. Omitting one means
 * events are not published; the log counterpart still runs, so a tool
 * with no bus still produces an operator-legible startup trace.
 */
export interface Publisher {
  publish(event: { topic: string; source: string; payload: EventPayload }): void;
}

/**
 * The narrow slice of a structured logger the supervisor needs. It
 * matches the shape `@hop-top/kit/log` returns, so a kit logger
 * satisfies it without an adapter.
 */
export interface ServeLogger {
  info(msg: string, ...keyvals: unknown[]): void;
  error(msg: string, ...keyvals: unknown[]): void;
}

/** Publishes one lifecycle transition to both sinks. */
class Emitter {
  constructor(
    private readonly topics: Record<string, string>,
    private readonly source: string,
    private readonly pub?: Publisher,
    private readonly log?: ServeLogger,
  ) {}

  emit(object: string, action: string, payload: EventPayload): void {
    this.logEvent(object, action, payload);
    if (!this.pub) return;
    const topic = this.topics[`${object}.${action}`];
    if (!topic) return;
    try {
      this.pub.publish({ topic, source: this.source, payload });
    } catch {
      // An event sink is observability, not correctness: a publish
      // failure never fails the lifecycle.
    }
  }

  private logEvent(object: string, action: string, payload: EventPayload): void {
    if (!this.log) return;
    const kv: unknown[] = ['object', object, 'elapsed_ms', payload.elapsed_ms];
    if (payload.service) kv.push('service', payload.service);
    if (payload.address) kv.push('address', payload.address);
    if (payload.reason) kv.push('reason', payload.reason);
    if (action === ACTION_FAILED) {
      if (payload.error) kv.push('error', payload.error);
      this.log.error(`serve: ${action}`, ...kv);
      return;
    }
    this.log.info(`serve: ${action}`, ...kv);
  }
}

// ---------------------------------------------------------------------------
// Supervisor
// ---------------------------------------------------------------------------

/** What one supervised run produced. */
export interface RunResult {
  /** The worst outcome observed, which is what the process exits on. */
  outcome: LifecycleOutcome;
  /** The rendered failure carrying code and exit code; absent on a clean stop. */
  error?: CliError;
  /** Identifiers whose start was invoked, in invocation order. */
  started: string[];
  /** Identifiers that reported ready, in report order. */
  ready: string[];
  /** Identifier to the error it failed with. */
  failed: Record<string, string>;
  /** The process exit code for this run. */
  exitCode: number;
}

/** Options for {@link Supervisor}. */
export interface SupervisorOptions {
  config?: SupervisorConfig;
  publisher?: Publisher;
  logger?: ServeLogger;
  topics?: Record<string, string>;
  eventSource?: string;
  /** Replaces the notion of elapsed time. Tests use it; production does not. */
  now?: () => number;
  /**
   * Aborts the drain. A second signal fires this, so an operator can
   * escalate without reaching for SIGKILL.
   */
  escalate?: AbortSignal;
}

/**
 * Runs a resolved set of services under one lifecycle: ordered start,
 * per-service readiness, policy-driven reaction to failure, and
 * ordered stop bounded by the configured budgets.
 *
 * A Supervisor holds no module-level state, so two can run
 * concurrently in one process — which is exactly what a test does.
 */
export class Supervisor {
  private readonly failurePolicy: FailurePolicy;
  private readonly shutdownTimeoutMs: number;
  private readonly emitter: Emitter;
  private readonly now: () => number;
  private readonly escalate?: AbortSignal;

  constructor(
    private readonly registry: ServiceRegistry,
    opts: SupervisorOptions = {},
  ) {
    this.failurePolicy = opts.config?.failurePolicy ?? DEFAULT_FAILURE_POLICY;
    this.shutdownTimeoutMs =
      opts.config?.shutdownTimeoutMs && opts.config.shutdownTimeoutMs > 0
        ? opts.config.shutdownTimeoutMs
        : DEFAULT_SHUTDOWN_TIMEOUT_MS;
    this.emitter = new Emitter(
      opts.topics ?? defaultTopics(),
      opts.eventSource ?? DEFAULT_TOPIC_PREFIX,
      opts.publisher,
      opts.logger,
    );
    this.now = opts.now ?? (() => Date.now());
    this.escalate = opts.escalate;
  }

  /**
   * Starts every service in `selected`, waits for the run to end, and
   * stops everything in reverse start order.
   *
   * The run ends when `signal` aborts (the clean path: a signal, or
   * the caller's own shutdown), when a failure trips the failure
   * policy, or when every started service has returned. Run always
   * performs the ordered stop before returning, so a caller never has
   * to clean up after it.
   *
   * `selected` is normally {@link ResolveOutcome.selected}; run does
   * not re-resolve and does not consult enablement, because the
   * decision the caller already made is the one to honor.
   */
  async run(
    signal: AbortSignal,
    selected: readonly string[],
    configs: Readonly<Record<string, ServiceConfig>> = {},
  ): Promise<RunResult> {
    const st = new RunState(this.now);

    if (selected.length === 0) {
      const err = usageError(
        'no services configured and enabled; enable one under services.* ' +
        'or name one explicitly',
      );
      return this.finish(st, 'no-services', err);
    }

    const order = startOrder(this.registry, selected);

    // The run controller is the caller's signal plus a cancel the
    // supervisor itself trips when the failure policy says to bring
    // everything down. Every service observes cancellation at the same
    // instant; nothing is queued behind another service's drain.
    const runAC = new AbortController();
    const onOuterAbort = (): void => runAC.abort();
    if (signal.aborted) runAC.abort();
    else signal.addEventListener('abort', onOuterAbort, { once: true });

    // Serving is the process's reason to stay alive, and nothing else
    // here refs the event loop. Held for the whole run, released in the
    // finally so it never outlives one.
    const keepAlive = new LoopKeepAlive();
    keepAlive.hold();

    try {
      const startFailed = await this.startAll(runAC, order, configs, st);
      if (!startFailed) {
        this.emitAggregateReady(st);
        await this.await_(runAC, st);
      }
      runAC.abort();
      await this.stopAll(st, configs);
      return this.finish(st, worstOutcome(st.observed));
    } finally {
      keepAlive.release();
      signal.removeEventListener('abort', onOuterAbort);
    }
  }

  /**
   * Starts each service in order, waiting for each to report ready
   * (or fail, or exhaust its budget) before starting the next.
   *
   * Serial start is what makes `dependsOn` mean anything: a dependent
   * must not begin acquiring before its dependency is accepting work.
   * Returns true when a start failure short-circuits the sequence.
   */
  private async startAll(
    runAC: AbortController,
    order: readonly string[],
    configs: Readonly<Record<string, ServiceConfig>>,
    st: RunState,
  ): Promise<boolean> {
    for (const name of order) {
      const svc = this.registry.lookup(name);
      if (!svc) {
        const msg = `service "${name}" disappeared from the registry`;
        st.noteFailure(name, msg, 'start-failed');
        this.emitter.emit(OBJECT_SERVICE, ACTION_FAILED, {
          service: name, error: msg, reason: 'unregistered',
          elapsed_ms: st.elapsedMs(),
        });
        return true;
      }

      let reportReady!: () => void;
      const readyPromise = new Promise<void>((res) => { reportReady = res; });
      st.started.push(name);
      this.emitter.emit(OBJECT_SERVICE, ACTION_STARTED, {
        service: name, elapsed_ms: st.elapsedMs(),
      });

      const running = svc
        .start(runAC.signal, reportReady)
        .then(() => ({ name, error: null as string | null }))
        .catch((e: unknown) => ({ name, error: errText(e) }));
      st.live.set(name, running);

      const failed = await this.awaitReady(name, readyPromise, configs, st);
      if (failed) return true;
    }
    return false;
  }

  /**
   * Blocks until `name` reports ready, fails, or exhausts its
   * readiness budget. A service that has not reported ready within the
   * budget is a start failure.
   */
  private async awaitReady(
    name: string,
    readyPromise: Promise<void>,
    configs: Readonly<Record<string, ServiceConfig>>,
    st: RunState,
  ): Promise<boolean> {
    const budget = configs[name]?.readyTimeoutMs ?? DEFAULT_READY_TIMEOUT_MS;
    const timer = new Timer(budget);
    const running = st.live.get(name)!;

    try {
      const winner = await Promise.race([
        readyPromise.then(() => 'ready' as const),
        running.then((e) => ({ exit: e })),
        timer.promise.then(() => 'timeout' as const),
      ]);

      if (winner === 'ready') {
        st.ready.push(name);
        this.emitter.emit(OBJECT_SERVICE, ACTION_READY_REPORTED, {
          service: name,
          address: this.addrOf(name),
          elapsed_ms: st.elapsedMs(),
        });
        return false;
      }

      if (winner === 'timeout') {
        const msg = `not ready within ${budget}ms`;
        st.noteFailure(name, msg, 'start-failed');
        this.emitter.emit(OBJECT_SERVICE, ACTION_FAILED, {
          service: name, error: msg, reason: 'ready_timeout',
          elapsed_ms: st.elapsedMs(),
        });
        return true;
      }

      // The service returned before reporting ready. That is a start
      // failure even when it returned cleanly: it was asked to serve
      // and it did not.
      st.live.delete(name);
      const msg = winner.exit.error ?? 'returned before reporting ready';
      st.noteFailure(name, msg, 'start-failed');
      this.emitter.emit(OBJECT_SERVICE, ACTION_FAILED, {
        service: name, error: msg, reason: 'start',
        elapsed_ms: st.elapsedMs(),
      });
      return true;
    } finally {
      timer.cancel();
    }
  }

  /**
   * Publishes the supervisor-scoped readiness event once every started
   * service is ready.
   */
  private emitAggregateReady(st: RunState): void {
    if (st.started.length > 0 && st.ready.length === st.started.length) {
      this.emitter.emit(OBJECT_SUPERVISOR, ACTION_READY_REPORTED, {
        elapsed_ms: st.elapsedMs(),
      });
    }
  }

  /**
   * Blocks while the services run. Returns when the run signal aborts,
   * when the failure policy trips, or when the last running service
   * has exited.
   */
  private async await_(runAC: AbortController, st: RunState): Promise<void> {
    const aborted = new Promise<'aborted'>((res) => {
      if (runAC.signal.aborted) { res('aborted'); return; }
      runAC.signal.addEventListener('abort', () => res('aborted'), { once: true });
    });

    while (st.live.size > 0) {
      const races = [...st.live.entries()].map(([, p]) => p);
      const winner = await Promise.race([aborted, ...races]);
      if (winner === 'aborted') return;

      st.live.delete(winner.name);
      if (winner.error !== null) {
        st.noteFailure(winner.name, winner.error, 'runtime-crash');
        this.emitter.emit(OBJECT_SERVICE, ACTION_FAILED, {
          service: winner.name, error: winner.error, reason: 'runtime',
          elapsed_ms: st.elapsedMs(),
        });
        if (this.failurePolicy === 'fail-fast') {
          runAC.abort();
          return;
        }
        continue;
      }
      // A clean return under isolate is not a failure of that service,
      // but the process must not survive as an empty shell: when the
      // last one is gone the run is over.
      if (!st.markStopped(winner.name)) {
        this.emitter.emit(OBJECT_SERVICE, ACTION_STOPPED, {
          service: winner.name, elapsed_ms: st.elapsedMs(),
        });
      }
    }

    if (Object.keys(st.failed).length > 0 && this.failurePolicy === 'isolate') {
      st.observed.push('runtime-crash');
    }
  }

  /**
   * Invokes stop in the exact reverse of the order services actually
   * started, one at a time, so a dependent is always fully stopped
   * before its dependency.
   *
   * Each stop is bounded by that service's budget. One that exceeds it
   * is abandoned — logged, emitted as failed, and the supervisor
   * proceeds to the next rather than blocking the whole shutdown on
   * one straggler. Exceeding the total budget ends the sequence with
   * `shutdown-timeout`.
   */
  private async stopAll(
    st: RunState,
    configs: Readonly<Record<string, ServiceConfig>>,
  ): Promise<void> {
    const order = [...st.started];
    const deadline = this.now() + this.shutdownTimeoutMs;

    for (let i = order.length - 1; i >= 0; i--) {
      const name = order[i]!;

      // A second signal aborts the drain: the remaining services are
      // abandoned and the run exits with the crash code.
      if (this.escalate?.aborted) {
        const msg = 'drain aborted by second signal';
        st.observed.push('runtime-crash');
        for (const abandoned of order.slice(0, i + 1)) {
          st.failed[abandoned] = msg;
          this.emitter.emit(OBJECT_SERVICE, ACTION_FAILED, {
            service: abandoned, error: msg, reason: 'escalated',
            elapsed_ms: st.elapsedMs(),
          });
        }
        return;
      }

      if (this.now() >= deadline) {
        st.observed.push('shutdown-timeout');
        this.emitter.emit(OBJECT_SERVICE, ACTION_FAILED, {
          service: name, reason: 'shutdown_timeout',
          error: `shutdown budget ${this.shutdownTimeoutMs}ms exhausted before stopping`,
          elapsed_ms: st.elapsedMs(),
        });
        continue;
      }

      const svc = this.registry.lookup(name);
      if (!svc) continue;

      const budget = Math.min(
        configs[name]?.stopTimeoutMs ?? DEFAULT_STOP_TIMEOUT_MS,
        Math.max(0, deadline - this.now()),
      );
      await this.stopOne(svc, name, budget, deadline, st);
    }
  }

  /** Bounds one stop by its budget and by whatever remains of the total. */
  private async stopOne(
    svc: Service,
    name: string,
    budget: number,
    deadline: number,
    st: RunState,
  ): Promise<void> {
    const stopAC = new AbortController();
    const timer = new Timer(budget);
    try {
      const winner = await Promise.race([
        svc.stop(stopAC.signal).then(() => 'ok' as const, (e: unknown) => ({ err: errText(e) })),
        timer.promise.then(() => 'timeout' as const),
      ]);

      if (winner === 'timeout') {
        // Abandoned, not awaited: the promise is left to settle on its
        // own so one straggler cannot hold the whole shutdown.
        stopAC.abort();
        const overTotal = this.now() >= deadline;
        st.noteFailure(
          name,
          `stop exceeded ${budget}ms`,
          overTotal ? 'shutdown-timeout' : 'runtime-crash',
        );
        this.emitter.emit(OBJECT_SERVICE, ACTION_FAILED, {
          service: name, error: `stop exceeded ${budget}ms`,
          reason: overTotal ? 'shutdown_timeout' : 'stop_timeout',
          elapsed_ms: st.elapsedMs(),
        });
        return;
      }

      if (winner !== 'ok') {
        st.noteFailure(name, winner.err, 'runtime-crash');
        this.emitter.emit(OBJECT_SERVICE, ACTION_FAILED, {
          service: name, error: winner.err, reason: 'stop',
          elapsed_ms: st.elapsedMs(),
        });
        return;
      }

      // A service that returned on its own already reported stopped
      // when it did; stop released its resources, and the event is not
      // repeated — one stopped per service per run.
      if (st.markStopped(name)) return;
      this.emitter.emit(OBJECT_SERVICE, ACTION_STOPPED, {
        service: name, elapsed_ms: st.elapsedMs(),
      });
    } finally {
      timer.cancel();
    }
  }

  private addrOf(name: string): string | undefined {
    const a = this.registry.lookup(name)?.addr?.();
    return a === '' ? undefined : a;
  }

  /** Assembles the result from everything the run observed. */
  private finish(
    st: RunState,
    outcome: LifecycleOutcome,
    err?: CliError,
  ): RunResult {
    const error = err ?? (isFailure(outcome) ? failureError(outcome, st.failed) : undefined);
    this.emitter.emit(OBJECT_SUPERVISOR, ACTION_STOPPED, {
      reason: outcome, elapsed_ms: st.elapsedMs(),
    });
    return {
      outcome,
      error,
      started: [...st.started],
      ready: [...st.ready],
      failed: { ...st.failed },
      exitCode: exitCodeFor(outcome),
    };
  }
}

/** One service's start settling. */
interface ServiceExit {
  name: string;
  error: string | null;
}

/** The mutable half of one run, kept off the Supervisor. */
class RunState {
  readonly started: string[] = [];
  readonly ready: string[] = [];
  readonly failed: Record<string, string> = {};
  readonly observed: LifecycleOutcome[] = [];
  readonly live = new Map<string, Promise<ServiceExit>>();
  private readonly stopped = new Set<string>();
  private readonly begin: number;

  constructor(private readonly now: () => number) {
    this.begin = now();
  }

  elapsedMs(): number {
    return this.now() - this.begin;
  }

  noteFailure(name: string, error: string, outcome: LifecycleOutcome): void {
    this.failed[name] = error;
    this.observed.push(outcome);
  }

  /**
   * Records that `name`'s stopped event has been emitted and reports
   * whether it had been already. A service reports stopped once per
   * run, whichever path noticed it first.
   */
  markStopped(name: string): boolean {
    const already = this.stopped.has(name);
    this.stopped.add(name);
    return already;
  }
}

/**
 * Holds the Node event loop open for as long as a run is live.
 *
 * Neither a `process.on(signal)` listener nor an `AbortSignal` listener
 * refs the loop: a supervisor whose services are all parked on their
 * abort signal has no referenced handle, and Node exits 0 the moment
 * the synchronous part of `run` yields. That is the failure the
 * contract's zero-services rule exists to prevent, arriving by another
 * route — a process that exits 0 without listening is
 * indistinguishable from a successful start to systemd or a container
 * runtime.
 *
 * A long referenced interval is the cheapest handle that survives every
 * platform; it is cleared the moment the run ends, so it never delays
 * an exit.
 */
export class LoopKeepAlive {
  private handle?: ReturnType<typeof setInterval>;

  hold(): void {
    if (this.handle) return;
    this.handle = setInterval(() => { /* referenced handle only */ }, 2 ** 30);
  }

  release(): void {
    if (!this.handle) return;
    clearInterval(this.handle);
    this.handle = undefined;
  }

  /**
   * Whether a referenced handle is currently held.
   *
   * Node's `process._getActiveHandles()` does not report timers, so
   * this is the only honest way to observe the property that matters.
   */
  get held(): boolean {
    return this.handle !== undefined && this.handle.hasRef?.() !== false;
  }
}

/** A cancellable timer whose promise resolves when the budget elapses. */
class Timer {
  readonly promise: Promise<void>;
  private handle?: ReturnType<typeof setTimeout>;

  constructor(ms: number) {
    this.promise = new Promise<void>((res) => {
      this.handle = setTimeout(res, Math.max(0, ms));
      // Never hold the process open on a budget nobody is waiting for.
      this.handle.unref?.();
    });
  }

  cancel(): void {
    if (this.handle) clearTimeout(this.handle);
  }
}

function errText(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

/**
 * Renders the outcome as the error envelope the command layer returns,
 * carrying the contract's code and exit code.
 */
function failureError(
  outcome: LifecycleOutcome,
  failed: Readonly<Record<string, string>>,
): CliError {
  const names = Object.keys(failed).sort();
  let msg: string;
  switch (outcome) {
    case 'start-failed': msg = 'service failed to start'; break;
    case 'shutdown-timeout': msg = 'shutdown budget exceeded'; break;
    default: msg = 'service failed';
  }
  names.forEach((name, i) => {
    msg += `${i === 0 ? ': ' : '; '}${name}: ${failed[name]}`;
  });
  return {
    code: codeFor(outcome),
    message: msg,
    exit_code: exitCodeFor(outcome),
    transience: 'permanent',
  };
}

// ---------------------------------------------------------------------------
// Signals
// ---------------------------------------------------------------------------

/** The signals the supervisor listens for. SIGKILL is not catchable. */
export const SHUTDOWN_SIGNALS: readonly NodeJS.Signals[] = ['SIGINT', 'SIGTERM'];

/**
 * A signal handler pair: `signal` aborts on the first SIGINT/SIGTERM,
 * `escalate` on a second of either kind.
 *
 * The first signal begins graceful shutdown; a second aborts the
 * drain, so an operator can escalate without reaching for SIGKILL.
 * `stop` removes the process listeners and must be called, or the
 * handlers outlive the run.
 */
export function signalController(): {
  signal: AbortSignal;
  escalate: AbortSignal;
  stop: () => void;
} {
  const first = new AbortController();
  const second = new AbortController();
  let count = 0;

  const onSignal = (): void => {
    count++;
    if (count === 1) first.abort();
    else second.abort();
  };

  for (const sig of SHUTDOWN_SIGNALS) process.on(sig, onSignal);

  let stopped = false;
  return {
    signal: first.signal,
    escalate: second.signal,
    stop: () => {
      if (stopped) return;
      stopped = true;
      for (const sig of SHUTDOWN_SIGNALS) process.removeListener(sig, onSignal);
    },
  };
}

// ---------------------------------------------------------------------------
// Command wiring
// ---------------------------------------------------------------------------

/** Options for {@link registerServe}. */
export interface RegisterServeOptions {
  /** The seam services were registered into. */
  registry: ServiceRegistry;
  /** Resolved `services.<name>` blocks, keyed by identifier. */
  configs?: Readonly<Record<string, ServiceConfig>>;
  /** The supervisor-scoped half of the `services` block. */
  config?: SupervisorConfig;
  /** The third validation gate. */
  policy?: PolicyGate;
  publisher?: Publisher;
  logger?: ServeLogger;
  /** Where `--list` writes. Defaults to `process.stdout`. */
  stdout?: { write(s: string): unknown };
  /**
   * Called with the run's exit code instead of exiting the process.
   * Tests supply one; production omits it and the command sets
   * `process.exitCode`.
   */
  onExit?: (code: number, error?: CliError) => void;
}

/**
 * Mounts the kit-owned `serve` command on `program`.
 *
 * With no positional argument it is the supervisor over every
 * configured and enabled service; with exactly one it is the selector,
 * which overrides aggregate enablement. Two or more is a usage error
 * at exit 2 — the arity refusal is owned here rather than left to
 * commander, whose own excess-argument error exits 1.
 *
 * The inspection form is the `--list` flag, not a `list` child:
 * `list` is reserved selector vocabulary, so a `serve list` child
 * would be indistinguishable from the selector form naming a service
 * called `list`.
 */
export function registerServe(
  program: Command,
  opts: RegisterServeOptions,
): Command {
  const cmd = program
    .command('serve [service...]')
    .description('Run configured services under one lifecycle')
    .option('--list', 'List registered services and their state')
    .action(async (services: string[], flags: { list?: boolean }) => {
      if (flags.list) {
        runList(opts);
        return;
      }
      await runServe(services ?? [], opts);
    });
  return cmd;
}

/**
 * Prints the registered services with their configured, enabled, and
 * ready state, in registration order so the listing mirrors the
 * adopter's wiring.
 *
 * The columns are not contract — a port renders them through its own
 * output layer — but the ordering is.
 */
function runList(opts: RegisterServeOptions): void {
  const w = opts.stdout ?? process.stdout;
  const configs = opts.configs ?? {};
  w.write('SERVICE              CONFIGURED  ENABLED  READY\n');
  for (const svc of opts.registry.list()) {
    const cfg = configs[svc.name];
    const configured = cfg !== undefined;
    const enabled = cfg?.enabled === true;
    w.write(
      `${svc.name.padEnd(20)} ${String(configured).padEnd(11)} ` +
      `${String(enabled).padEnd(8)} ${String(svc.ready())}\n`,
    );
  }
}

/** Resolves the invocation and runs the resulting set. */
async function runServe(
  args: readonly string[],
  opts: RegisterServeOptions,
): Promise<void> {
  const outcome = resolve(opts.registry, {
    args,
    configs: opts.configs,
    policy: opts.policy,
  });

  if (outcome.error) {
    report(opts, exitCodeFor(outcome.outcome ?? 'config-invalid'), outcome.error);
    return;
  }

  // The supervisor owns the signals from here: the first begins the
  // drain, a second aborts it.
  const sig = signalController();
  try {
    const sup = new Supervisor(opts.registry, {
      config: opts.config,
      publisher: opts.publisher,
      logger: opts.logger,
      escalate: sig.escalate,
    });
    const res = await sup.run(sig.signal, outcome.selected, opts.configs ?? {});
    report(opts, res.exitCode, res.error);
  } finally {
    sig.stop();
  }
}

function report(
  opts: RegisterServeOptions,
  code: number,
  error?: CliError,
): void {
  if (opts.onExit) {
    opts.onExit(code, error);
    return;
  }
  if (error) {
    process.stderr.write(
      error.code ? `${error.code}: ${error.message}\n` : `${error.message}\n`,
    );
    if (error.suggested_fix) process.stderr.write(`Fix: ${error.suggested_fix}\n`);
  }
  process.exitCode = code;
}

/**
 * Re-exported so a caller can branch on a transient failure without
 * importing `output/error` separately. A serve failure wrapping a kit
 * transient error propagates exit 6 unchanged, so agents and retry
 * wrappers keep their existing branch.
 */
export { CODE_TRANSIENT };
