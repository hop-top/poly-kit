/**
 * Errors of the Migrator, ported from Go `core/upgrade`. Go matches them
 * with `errors.Is` through wrapped errors; here a wrapped error keeps its
 * cause in `cause` (and in `errors` when two failures are reported
 * together), and the type guards walk that chain.
 */

/** Text of an unknown thrown value: a message for an Error, else String(). */
export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * A Migrator error that wraps what caused it. `cause` is the first
 * underlying failure; `errors` lists every one when a run failed and its
 * rollback failed too.
 */
export class UpgradeError extends Error {
  readonly errors: readonly unknown[];
  constructor(message: string, cause?: unknown, errors?: readonly unknown[]) {
    super(message, cause === undefined ? undefined : { cause });
    this.name = 'UpgradeError';
    this.errors = errors ?? (cause === undefined ? [] : [cause]);
  }
}

/**
 * What a driver's `backup` or `restore` throws when kit cannot back up or
 * restore its store (a server database, a file under a live connection
 * pool). A `backup` that throws it is not a failure: the run goes on with
 * no backup. A `restore` that throws it restores nothing. Wrap it with
 * `cause` to keep it matchable: {@link isBackupUnsupported}.
 */
export class BackupUnsupportedError extends Error {
  constructor(message = 'upgrade: backup unsupported', options?: { cause?: unknown }) {
    super(message, options);
    this.name = 'BackupUnsupportedError';
  }
}

/**
 * A rollback that could not undo a failed run: a `down` was missing or
 * failed and no backup could restore the schema, so the stored version
 * stays at the last step still applied. The run's error carries it in its
 * chain: {@link isNotRestored}. No `reverted` event is published for it.
 * `message` is the text the vectors pin; `cause` is typically a
 * {@link BackupUnsupportedError}.
 */
export class NotRestoredError extends Error {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = 'NotRestoredError';
  }
}

/**
 * A failed `Migration.verify` or final verify check. Keeps the check's own
 * text and carries its error as `cause`: {@link isVerifyError}. The
 * document migrator's `DocError` of kind `verify` is a different thing.
 */
export class VerifyError extends Error {
  constructor(cause: unknown) {
    super(errorText(cause), { cause });
    this.name = 'VerifyError';
  }
}

/**
 * The run's signal was aborted. The text is Go's `context canceled`, or
 * `context deadline exceeded` when the abort reason is a `TimeoutError`
 * (`AbortSignal.timeout`); `cause` is the signal's reason.
 */
export class CanceledError extends Error {
  constructor(signal: AbortSignal) {
    const reason: unknown = signal.reason;
    const timeout = reason instanceof Error && reason.name === 'TimeoutError';
    super(timeout ? 'context deadline exceeded' : 'context canceled', { cause: reason });
    this.name = 'CanceledError';
  }
}

/** The error a driver's `lock` rejects with when its signal aborts while it waits. */
export function abortError(signal: AbortSignal): CanceledError {
  return new CanceledError(signal);
}

/** True when `err` or anything in its `cause`/`errors` chain satisfies `test`. */
function inChain(err: unknown, test: (e: unknown) => boolean, seen = new Set<unknown>()): boolean {
  if (err === null || err === undefined || seen.has(err)) return false;
  seen.add(err);
  if (test(err)) return true;
  if (typeof err !== 'object') return false;
  const e = err as { cause?: unknown; errors?: unknown };
  if (inChain(e.cause, test, seen)) return true;
  return Array.isArray(e.errors) && e.errors.some((c) => inChain(c, test, seen));
}

/** Whether `err` is, or wraps, a {@link BackupUnsupportedError}. */
export function isBackupUnsupported(err: unknown): boolean {
  return inChain(err, (e) => e instanceof BackupUnsupportedError);
}

/** Whether `err` is, or wraps, a {@link NotRestoredError}. */
export function isNotRestored(err: unknown): boolean {
  return inChain(err, (e) => e instanceof NotRestoredError);
}

/** Whether `err` is, or wraps, a {@link VerifyError}. */
export function isVerifyError(err: unknown): boolean {
  return inChain(err, (e) => e instanceof VerifyError);
}

/** Whether `err` is, or wraps, a {@link CanceledError}. */
export function isCanceled(err: unknown): boolean {
  return inChain(err, (e) => e instanceof CanceledError);
}
