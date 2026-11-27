/**
 * Migration lifecycle events, ported from Go `core/upgrade/migrate_events.go`.
 * Published as kit bus `Event`s (the `Bus` of the SDK's bus module):
 * pass any object with `publish(event)`, typically a `Bus`. Payload keys
 * are the Go JSON names.
 */

import { createEvent, type Event } from '../bus';

/** Anything events can be published to; a kit `Bus` fits. */
export interface EventPublisher {
  publish(event: Event): void;
}

/** Source set on every published event. */
export const PUBLISH_SOURCE = 'core.upgrade';

/**
 * Topics a Migrator publishes on. Run level: `runStarted`, then
 * `runCompleted` or `runFailed`. `baselined` follows a baseline stamp,
 * before that schema's migrations. Per migration: `started`, then
 * `applied` and `verified`, or `failed`; `reverted` follows a rollback
 * that succeeded. The final verify publishes `verified` with
 * `final: true`, or `failed` with stage `final`.
 */
export interface MigrationTopics {
  runStarted: string;
  runCompleted: string;
  runFailed: string;
  started: string;
  applied: string;
  verified: string;
  failed: string;
  reverted: string;
  baselined: string;
}

const ACTIONS: Record<keyof MigrationTopics, string> = {
  runStarted: 'run_started',
  runCompleted: 'run_completed',
  runFailed: 'run_failed',
  started: 'started',
  applied: 'applied',
  verified: 'verified',
  failed: 'failed',
  reverted: 'reverted',
  baselined: 'baselined',
};

/** `kit.core.migration.<action>`. */
export const DEFAULT_MIGRATION_TOPICS: MigrationTopics = topicsFromPrefix('kit.core.migration');

/** Every topic from a 3-segment prefix (`myapp.core.migration`). Throws a TypeError otherwise. */
export function topicsFromPrefix(prefix: string): MigrationTopics {
  const segs = prefix.split('.');
  if (segs.length !== 3 || segs.some((s) => !/^[a-z0-9_-]+$/i.test(s))) {
    throw new TypeError(`upgrade: migration topic prefix "${prefix}" must have 3 dot-separated segments`);
  }
  const out = {} as MigrationTopics;
  for (const k of Object.keys(ACTIONS) as (keyof MigrationTopics)[]) out[k] = `${prefix}.${ACTIONS[k]}`;
  return out;
}

/** Default topics, from `prefix` when given, with each non-empty field of `override` on top. */
export function resolveTopics(prefix?: string, override?: Partial<MigrationTopics>): MigrationTopics {
  const out = prefix !== undefined ? topicsFromPrefix(prefix) : { ...DEFAULT_MIGRATION_TOPICS };
  for (const [k, v] of Object.entries(override ?? {})) {
    if (v) out[k as keyof MigrationTopics] = v;
  }
  return out;
}

/** Payload of run_started, run_completed and run_failed. */
export interface MigrationRunPayload {
  tool: string;
  to: string;
  applied?: number;
  schema?: string;
  error?: string;
  duration_ms?: number;
}

/**
 * Payload of the per-migration events. `from` is the version before the
 * step, `to` the step's version. For `reverted`, `from` is the version
 * being attempted and `to` the stored version restored. For `baselined`,
 * `from` is `""` and `to` the version the store was stamped at.
 */
export interface MigrationPayload {
  tool: string;
  schema: string;
  from: string;
  to: string;
  /** failed: up, verify, record, final, canceled. */
  stage?: string;
  /** reverted: down, restore. */
  method?: string;
  /** verified by the final verify. */
  final?: boolean;
  error?: string;
  duration_ms?: number;
}

/** Whole milliseconds since `start` (a `performance.now()` reading). */
export function millisSince(start: number): number {
  return Math.max(0, Math.floor(performance.now() - start));
}

/** Publishes best-effort: a missing publisher is a no-op and a throwing one never fails a run. */
export function emit(pub: EventPublisher | undefined, topic: string, payload: object): void {
  if (!pub || topic === '') return;
  try {
    pub.publish(createEvent(topic, PUBLISH_SOURCE, payload));
  } catch {
    // dropped, like Go
  }
}
