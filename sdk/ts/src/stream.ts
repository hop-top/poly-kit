/**
 * @module stream
 * @package @hop-top/kit
 *
 * Factor 3 — Stream and Exit Discipline.
 *
 * Structured data goes to stdout; human-oriented messages
 * (logs, progress, prompts) go to stderr. Exit codes are
 * semantic, not just 0/1.
 */

export enum ExitCode {
  OK = 0,
  Error = 1,
  Usage = 2,
  NotFound = 3,
  Conflict = 4,
  Auth = 5,
  Permission = 6,
  Timeout = 7,
  Cancelled = 8,
}

export interface StreamWriter {
  /** stdout — structured/parseable output */
  data: NodeJS.WritableStream;
  /** stderr — logs, progress, human messages */
  human: NodeJS.WritableStream;
  isTTY: boolean;
}

export interface StreamWriterOptions {
  data?: NodeJS.WritableStream;
  human?: NodeJS.WritableStream;
}

export function createStreamWriter(
  opts?: StreamWriterOptions,
): StreamWriter {
  const data = opts?.data ?? process.stdout;
  const human = opts?.human ?? process.stderr;
  return {
    data,
    human,
    isTTY: !!(process.stdout as any).isTTY,
  };
}
