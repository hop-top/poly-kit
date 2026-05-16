/**
 * @module progress
 * @package @hop-top/kit
 *
 * Factor 9 — Observable Long-Running Operations.
 *
 * Emits structured progress events (JSON in non-TTY,
 * human-readable in TTY).
 */

export interface ProgressEvent {
  phase: string;
  step: string;
  current: number;
  total: number;
  percent: number;
  message?: string;
}

export interface JobHandle {
  id: string;
  status: 'running' | 'completed' | 'failed' | 'cancelled';
}

export class ProgressReporter {
  private w: NodeJS.WritableStream;
  private tty: boolean;

  constructor(w: NodeJS.WritableStream, isTTY: boolean) {
    this.w = w;
    this.tty = isTTY;
  }

  emit(event: ProgressEvent): void {
    if (this.tty) {
      const msg = event.message
        ? ` ${event.message}`
        : '';
      this.w.write(
        `[${event.phase}] ${event.step}`
        + ` ${event.current}/${event.total}`
        + ` (${event.percent}%)${msg}\n`,
      );
      return;
    }
    this.w.write(JSON.stringify(event) + '\n');
  }

  done(message: string): void {
    if (this.tty) {
      this.w.write(`done: ${message}\n`);
      return;
    }
    this.w.write(
      JSON.stringify({ done: true, message }) + '\n',
    );
  }
}
