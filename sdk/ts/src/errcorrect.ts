/**
 * @module errcorrect
 * @package @hop-top/kit
 *
 * Factor 4 — Corrective Error Model.
 *
 * Structured errors with machine-readable code, human-readable message,
 * likely cause, suggested fix, and alternative commands.
 */

export interface CorrectedError {
  code: string;
  message: string;
  cause?: string;
  fix?: string;
  alternatives?: string[];
  retryable?: boolean;
}

/**
 * Creates an Error instance augmented with CorrectedError fields.
 * Serializes cleanly to JSON (only defined fields).
 */
export function createCorrectedError(
  opts: CorrectedError,
): Error & CorrectedError {
  const err = new Error(opts.message) as Error & CorrectedError & {
    toJSON(): Record<string, unknown>;
  };
  err.code = opts.code;
  if (opts.cause !== undefined) err.cause = opts.cause;
  if (opts.fix !== undefined) err.fix = opts.fix;
  if (opts.alternatives !== undefined) err.alternatives = opts.alternatives;
  if (opts.retryable !== undefined) err.retryable = opts.retryable;

  err.toJSON = () => {
    const o: Record<string, unknown> = {
      code: err.code,
      message: err.message,
    };
    if (err.cause !== undefined) o.cause = err.cause;
    if (err.fix !== undefined) o.fix = err.fix;
    if (err.alternatives !== undefined) o.alternatives = err.alternatives;
    if (err.retryable !== undefined) o.retryable = err.retryable;
    return o;
  };

  return err;
}

/**
 * Formats a CorrectedError for terminal display.
 *
 * ```
 *   ERROR  mission not found
 *   Cause: no mission matches "bogux"
 *   Fix:   spaced mission list
 *   Try:   spaced mission search bogux
 * ```
 */
export function formatError(
  err: CorrectedError,
  opts?: { noColor?: boolean },
): string {
  const red = opts?.noColor ? (s: string) => s : (s: string) => `\x1b[31m${s}\x1b[0m`;
  const bold = opts?.noColor ? (s: string) => s : (s: string) => `\x1b[1m${s}\x1b[0m`;

  const lines: string[] = [];
  lines.push(`  ${red(bold('ERROR'))}  ${err.message}`);

  if (err.cause) {
    lines.push(`  Cause: ${err.cause}`);
  }
  if (err.fix) {
    lines.push(`  Fix:   ${err.fix}`);
  }
  if (err.alternatives && err.alternatives.length > 0) {
    for (const alt of err.alternatives) {
      lines.push(`  Try:   ${alt}`);
    }
  }

  return lines.join('\n');
}
