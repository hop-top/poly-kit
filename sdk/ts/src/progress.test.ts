import { describe, it, expect } from 'vitest';
import { PassThrough } from 'node:stream';
import { ProgressReporter, type ProgressEvent } from './progress';

function collect(stream: PassThrough): string[] {
  const lines: string[] = [];
  stream.on('data', (chunk: Buffer) => {
    const text = chunk.toString().trim();
    if (text) lines.push(text);
  });
  return lines;
}

describe('ProgressReporter', () => {
  describe('non-TTY (JSON)', () => {
    it('emits JSON event', () => {
      const out = new PassThrough();
      const lines = collect(out);
      const r = new ProgressReporter(out, false);
      const ev: ProgressEvent = {
        phase: 'download',
        step: 'fetch',
        current: 5,
        total: 10,
        percent: 50,
      };
      r.emit(ev);

      expect(lines).toHaveLength(1);
      const parsed = JSON.parse(lines[0]);
      expect(parsed.phase).toBe('download');
      expect(parsed.percent).toBe(50);
    });

    it('emits done as JSON', () => {
      const out = new PassThrough();
      const lines = collect(out);
      const r = new ProgressReporter(out, false);
      r.done('all done');

      expect(lines).toHaveLength(1);
      const parsed = JSON.parse(lines[0]);
      expect(parsed.done).toBe(true);
      expect(parsed.message).toBe('all done');
    });
  });

  describe('TTY (human)', () => {
    it('emits human-readable progress', () => {
      const out = new PassThrough();
      const lines = collect(out);
      const r = new ProgressReporter(out, true);
      r.emit({
        phase: 'upload',
        step: 'send',
        current: 3,
        total: 9,
        percent: 33,
      });

      expect(lines).toHaveLength(1);
      expect(lines[0]).toContain('upload');
      expect(lines[0]).toContain('33%');
    });

    it('emits human-readable done', () => {
      const out = new PassThrough();
      const lines = collect(out);
      const r = new ProgressReporter(out, true);
      r.done('finished');

      expect(lines).toHaveLength(1);
      expect(lines[0]).toContain('finished');
    });
  });

  it('includes optional message in event', () => {
    const out = new PassThrough();
    const lines = collect(out);
    const r = new ProgressReporter(out, false);
    r.emit({
      phase: 'build',
      step: 'compile',
      current: 1,
      total: 1,
      percent: 100,
      message: 'compiling main',
    });

    const parsed = JSON.parse(lines[0]);
    expect(parsed.message).toBe('compiling main');
  });
});
