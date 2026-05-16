import { describe, it, expect } from 'vitest';
import { PassThrough } from 'node:stream';
import { ExitCode, createStreamWriter } from './stream';

describe('ExitCode', () => {
  it('maps OK to 0', () => {
    expect(ExitCode.OK).toBe(0);
  });

  it('maps Error to 1', () => {
    expect(ExitCode.Error).toBe(1);
  });

  it('maps Usage to 2', () => {
    expect(ExitCode.Usage).toBe(2);
  });

  it('maps NotFound to 3', () => {
    expect(ExitCode.NotFound).toBe(3);
  });

  it('maps Conflict to 4', () => {
    expect(ExitCode.Conflict).toBe(4);
  });

  it('maps Auth to 5', () => {
    expect(ExitCode.Auth).toBe(5);
  });

  it('maps Permission to 6', () => {
    expect(ExitCode.Permission).toBe(6);
  });

  it('maps Timeout to 7', () => {
    expect(ExitCode.Timeout).toBe(7);
  });

  it('maps Cancelled to 8', () => {
    expect(ExitCode.Cancelled).toBe(8);
  });
});

describe('createStreamWriter', () => {
  it('returns a StreamWriter with data, human, and isTTY', () => {
    const sw = createStreamWriter();
    expect(sw).toHaveProperty('data');
    expect(sw).toHaveProperty('human');
    expect(sw).toHaveProperty('isTTY');
    expect(typeof sw.isTTY).toBe('boolean');
  });

  it('defaults data to stdout', () => {
    const sw = createStreamWriter();
    expect(sw.data).toBe(process.stdout);
  });

  it('defaults human to stderr', () => {
    const sw = createStreamWriter();
    expect(sw.human).toBe(process.stderr);
  });

  it('accepts custom streams', () => {
    const data = new PassThrough();
    const human = new PassThrough();
    const sw = createStreamWriter({ data, human });
    expect(sw.data).toBe(data);
    expect(sw.human).toBe(human);
  });
});
