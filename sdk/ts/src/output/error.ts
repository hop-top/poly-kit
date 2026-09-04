/**
 * @module output/error
 *
 * Structured-error envelope. Mirrors Go's `go/console/output` error
 * envelope: when a command fails under `--format json|yaml`, the error is
 * materialized as a {@link CliError} and rendered to stderr by
 * {@link renderError}. Plaintext mode (`--format table` or unset) prints
 * `"Code: Message\nFix: ...\n"`.
 *
 * Fields use the on-wire snake_case keys directly (like the telemetry
 * `Envelope`) so `JSON.stringify` is wire-exact; empty optional fields
 * stay off the wire, mirroring Go's `omitempty`.
 */

import * as yaml from 'js-yaml';

// ---------------------------------------------------------------------------
// Transience classes
// ---------------------------------------------------------------------------

/** Marks a failure a retry may clear (rate limit, timeout, upstream blip). */
export const TRANSIENCE_TRANSIENT = 'transient';
/**
 * Marks a failure retrying cannot clear without changing the input or the
 * environment.
 */
export const TRANSIENCE_PERMANENT = 'permanent';
/**
 * Marks a failure kit cannot classify. Agents should treat retries as
 * best-effort and bounded.
 */
export const TRANSIENCE_UNKNOWN = 'unknown';

// ---------------------------------------------------------------------------
// Standard codes mapping the cross-tool exit codes (conventions §8.1)
// ---------------------------------------------------------------------------

export const CODE_OK = 'OK'; // exit 0
export const CODE_GENERIC = 'GENERIC'; // exit 1
export const CODE_USAGE = 'USAGE'; // exit 2
export const CODE_NOT_FOUND = 'NOT_FOUND'; // exit 3
export const CODE_CONFLICT = 'CONFLICT'; // exit 4
export const CODE_UNAUTHORIZED = 'UNAUTHORIZED'; // exit 5
export const CODE_TRANSIENT = 'TRANSIENT'; // exit 6 — Factor-11 transient/retryable failure
export const CODE_PROVENANCE_MISSING = 'PROVENANCE_MISSING'; // exit 65 — Factor-12 strict-mode refusal
export const CODE_RATE_LIMITED = 'RATE_LIMITED'; // exit 64 — Factor-10 max-ops budget exceeded

/**
 * Spec-assigned exit code for the generic failure class: the command
 * failed and no narrower code applies. Pair it with {@link genericError}
 * rather than hand-rolling exit 1, so the envelope carries a transience
 * class.
 */
export const EXIT_GENERIC = 1;
/**
 * Spec-assigned exit code for transient/retryable failures (Factor 11).
 * Agents branch on it before parsing stderr: exit 6 means a retry may
 * clear the failure.
 */
export const EXIT_TRANSIENT = 6;
/** Conventional exit code for Factor-10 rate-limit refusals. */
export const EXIT_RATE_LIMITED = 64;
/**
 * Conventional exit code for Factor-12 strict-mode provenance refusals.
 * Lives at 65 in kit's extension band (alongside RATE_LIMITED at 64):
 * the spec reserves 0-6 for its core taxonomy and leaves >6 to per-tool
 * codes, and kit as a library stays out of the low per-tool range.
 */
export const EXIT_PROVENANCE_MISSING = 65;

/**
 * Structured-error envelope rendered to stderr when `--format json|yaml`
 * is set. Keys are the on-wire snake_case names.
 *
 * `transience` classifies the failure for retry decisions (Factor 4):
 * transient (retry-worthy), permanent (do not retry), or unknown.
 * Constructors and {@link wrapError} populate it; {@link renderError}
 * normalizes an unset value to unknown so every structured error carries
 * a valid class on the wire.
 */
export interface CliError {
  code: string;
  message: string;
  cause?: string;
  suggested_fix?: string;
  alternatives?: string[];
  exit_code: number;
  transience?: string;
}

/**
 * Retained source errors, keyed by envelope. Mirrors Go's unexported
 * `err` field: off the wire by construction, readable via
 * {@link unwrapError}.
 */
const retained = new WeakMap<CliError, unknown>();

/**
 * Default transience class for one of the standard codes. Unrecognized
 * (adopter-defined) codes map to unknown; adopters set `transience`
 * (or use {@link withTransience}) to classify their own codes.
 */
export function transienceForCode(code: string): string {
  switch (code) {
    case CODE_USAGE:
    case CODE_NOT_FOUND:
    case CODE_CONFLICT:
    case CODE_UNAUTHORIZED:
    case CODE_PROVENANCE_MISSING:
      return TRANSIENCE_PERMANENT;
    case CODE_RATE_LIMITED:
    case CODE_TRANSIENT:
      return TRANSIENCE_TRANSIENT;
    default:
      return TRANSIENCE_UNKNOWN;
  }
}

/**
 * Builds an envelope from `err`, retaining it for {@link unwrapError}
 * while rendering as `code` and message. Transience defaults from the
 * code via {@link transienceForCode}; use {@link withTransience} to
 * override.
 */
export function wrapError(
  err: unknown,
  code: string,
  exitCode: number,
): CliError | null {
  if (err === null || err === undefined) return null;
  const e: CliError = {
    code,
    message: err instanceof Error ? err.message : String(err),
    exit_code: exitCode,
    transience: transienceForCode(code),
  };
  retained.set(e, err);
  return e;
}

/**
 * The error this envelope was built from via {@link wrapError}, so
 * callers can classify failures by cause instead of string-matching
 * `message`. Mirrors Go's `(*Error).Unwrap`.
 */
export function unwrapError(e: CliError): unknown {
  return retained.get(e);
}

/**
 * Copy of `e` with `transience` set, every other field (and the retained
 * source error) untouched. Copies rather than mutating: adopters
 * commonly share module-level envelopes, and writing to one would leak
 * across call sites.
 */
export function withTransience(e: CliError, transience: string): CliError {
  const clone: CliError = { ...e, transience };
  if (retained.has(e)) retained.set(clone, retained.get(e));
  return clone;
}

/**
 * CODE_GENERIC envelope with exit code 1. The catch-all for failures no
 * narrower code describes; permanent because retrying the same input
 * in the same environment is not expected to help. Wrapping an
 * arbitrary error as CODE_GENERIC via {@link wrapError} still defaults
 * to unknown.
 */
export function genericError(message: string): CliError {
  return {
    code: CODE_GENERIC,
    message,
    exit_code: EXIT_GENERIC,
    transience: TRANSIENCE_PERMANENT,
  };
}

/** CODE_NOT_FOUND envelope with exit code 3. */
export function notFoundError(message: string): CliError {
  return {
    code: CODE_NOT_FOUND,
    message,
    exit_code: 3,
    transience: TRANSIENCE_PERMANENT,
  };
}

/** CODE_CONFLICT envelope with exit code 4. */
export function conflictError(message: string): CliError {
  return {
    code: CODE_CONFLICT,
    message,
    exit_code: 4,
    transience: TRANSIENCE_PERMANENT,
  };
}

/** CODE_UNAUTHORIZED envelope with exit code 5. */
export function unauthorizedError(message: string): CliError {
  return {
    code: CODE_UNAUTHORIZED,
    message,
    exit_code: 5,
    transience: TRANSIENCE_PERMANENT,
  };
}

/** CODE_USAGE envelope with exit code 2. */
export function usageError(message: string): CliError {
  return {
    code: CODE_USAGE,
    message,
    exit_code: 2,
    transience: TRANSIENCE_PERMANENT,
  };
}

/**
 * CODE_TRANSIENT envelope with exit code 6 (Factor 11). Use it for
 * failures a retry may clear: upstream timeouts, connection resets,
 * service-unavailable responses.
 */
export function transientError(message: string): CliError {
  return {
    code: CODE_TRANSIENT,
    message,
    exit_code: EXIT_TRANSIENT,
    transience: TRANSIENCE_TRANSIENT,
  };
}

/** CODE_RATE_LIMITED envelope with exit code 64 (Factor 10). */
export function rateLimitedError(message: string): CliError {
  return {
    code: CODE_RATE_LIMITED,
    message,
    exit_code: EXIT_RATE_LIMITED,
    transience: TRANSIENCE_TRANSIENT,
  };
}

/**
 * CODE_PROVENANCE_MISSING envelope with exit code 65 (Factor 12).
 * `detail` is a free-form string suitable for the `cause` slot
 * (typically the JSON-pointer list of offending fields).
 */
export function provenanceMissingError(detail: string): CliError {
  return {
    code: CODE_PROVENANCE_MISSING,
    message: 'provenance not recorded for one or more output fields',
    cause: detail,
    suggested_fix:
      'record provenance for synthesized/cached fields before rendering ' +
      '(see @hop-top/kit/provenance)',
    exit_code: EXIT_PROVENANCE_MISSING,
    transience: TRANSIENCE_PERMANENT,
  };
}

/**
 * Wire form of `e`: empty optional fields dropped (omitempty parity),
 * key order mirroring the Go struct.
 */
function toWire(e: CliError): CliError {
  const wire: Record<string, unknown> = { code: e.code, message: e.message };
  if (e.cause) wire.cause = e.cause;
  if (e.suggested_fix) wire.suggested_fix = e.suggested_fix;
  if (e.alternatives && e.alternatives.length > 0) {
    wire.alternatives = [...e.alternatives];
  }
  wire.exit_code = e.exit_code;
  if (e.transience) wire.transience = e.transience;
  return wire as unknown as CliError;
}

/**
 * Writes `err` to `w` in the requested format. `format === ''` or
 * `'table'` renders human-readable plain text (`"Code: Message\nFix:
 * ..."`); JSON/YAML render the envelope structurally. An unset
 * transience is normalized to unknown on the wire (Factor 4) without
 * mutating `err`. Always returns; the caller decides the exit code from
 * `err.exit_code` after rendering.
 */
export function renderError(
  w: { write(chunk: string): unknown },
  format: string,
  err: CliError | null | undefined,
): void {
  if (!err) return;
  const e = err.transience ? err : withTransience(err, TRANSIENCE_UNKNOWN);
  if (format === 'json') {
    w.write(`${JSON.stringify(toWire(e), null, 2)}\n`);
    return;
  }
  if (format === 'yaml') {
    w.write(yaml.dump(toWire(e)));
    return;
  }
  renderErrorPlain(w, e);
}

/**
 * Human-readable form used by `--format table` (and the default empty
 * format). Each populated field appears on its own line so the output is
 * grep-friendly.
 */
function renderErrorPlain(
  w: { write(chunk: string): unknown },
  err: CliError,
): void {
  let out = err.code ? `${err.code}: ${err.message}\n` : `${err.message}\n`;
  if (err.cause) out += `Cause: ${err.cause}\n`;
  if (err.suggested_fix) out += `Fix: ${err.suggested_fix}\n`;
  for (const alt of err.alternatives ?? []) out += `Alternative: ${alt}\n`;
  w.write(out);
}
