import { describe, it, expect } from 'vitest';
import {
  createCorrectedError,
  formatError,
  type CorrectedError,
} from './errcorrect';

describe('createCorrectedError', () => {
  it('returns an Error with CorrectedError fields', () => {
    const err = createCorrectedError({
      code: 'NOT_FOUND',
      message: 'mission not found',
      cause: 'no mission matches "bogux"',
      fix: 'spaced mission list',
      alternatives: ['spaced mission search bogux'],
      retryable: false,
    });

    expect(err).toBeInstanceOf(Error);
    expect(err.message).toBe('mission not found');
    expect(err.code).toBe('NOT_FOUND');
    expect(err.cause).toBe('no mission matches "bogux"');
    expect(err.fix).toBe('spaced mission list');
    expect(err.alternatives).toEqual(['spaced mission search bogux']);
    expect(err.retryable).toBe(false);
  });

  it('works with minimal fields', () => {
    const err = createCorrectedError({
      code: 'INVALID',
      message: 'bad input',
    });

    expect(err).toBeInstanceOf(Error);
    expect(err.code).toBe('INVALID');
    expect(err.message).toBe('bad input');
    expect(err.cause).toBeUndefined();
    expect(err.fix).toBeUndefined();
    expect(err.alternatives).toBeUndefined();
    expect(err.retryable).toBeUndefined();
  });

  it('has a stack trace', () => {
    const err = createCorrectedError({
      code: 'TIMEOUT',
      message: 'timed out',
    });
    expect(err.stack).toBeDefined();
  });
});

describe('JSON serialization', () => {
  it('includes all CorrectedError fields', () => {
    const err = createCorrectedError({
      code: 'NOT_FOUND',
      message: 'mission not found',
      cause: 'no mission matches "bogux"',
      fix: 'spaced mission list',
      alternatives: ['spaced mission search bogux'],
      retryable: false,
    });

    const json = JSON.parse(JSON.stringify(err));
    expect(json.code).toBe('NOT_FOUND');
    expect(json.message).toBe('mission not found');
    expect(json.cause).toBe('no mission matches "bogux"');
    expect(json.fix).toBe('spaced mission list');
    expect(json.alternatives).toEqual(['spaced mission search bogux']);
    expect(json.retryable).toBe(false);
  });

  it('omits undefined optional fields', () => {
    const err = createCorrectedError({
      code: 'INVALID',
      message: 'bad',
    });

    const json = JSON.parse(JSON.stringify(err));
    expect(json.code).toBe('INVALID');
    expect(json.message).toBe('bad');
    expect(json).not.toHaveProperty('cause');
    expect(json).not.toHaveProperty('fix');
    expect(json).not.toHaveProperty('alternatives');
    expect(json).not.toHaveProperty('retryable');
  });
});

describe('formatError', () => {
  it('renders terminal format with all fields', () => {
    const err: CorrectedError = {
      code: 'NOT_FOUND',
      message: 'mission not found',
      cause: 'no mission matches "bogux"',
      fix: 'spaced mission list',
      alternatives: ['spaced mission search bogux'],
    };

    const out = formatError(err, { noColor: true });
    expect(out).toContain('ERROR');
    expect(out).toContain('mission not found');
    expect(out).toContain('Cause:');
    expect(out).toContain('no mission matches "bogux"');
    expect(out).toContain('Fix:');
    expect(out).toContain('spaced mission list');
    expect(out).toContain('Try:');
    expect(out).toContain('spaced mission search bogux');
  });

  it('omits missing optional sections', () => {
    const err: CorrectedError = {
      code: 'INVALID',
      message: 'bad input',
    };

    const out = formatError(err, { noColor: true });
    expect(out).toContain('ERROR');
    expect(out).toContain('bad input');
    expect(out).not.toContain('Cause:');
    expect(out).not.toContain('Fix:');
    expect(out).not.toContain('Try:');
  });

  it('renders multiple alternatives', () => {
    const err: CorrectedError = {
      code: 'AMBIGUOUS',
      message: 'ambiguous',
      alternatives: ['cmd a', 'cmd b'],
    };

    const out = formatError(err, { noColor: true });
    expect(out).toContain('cmd a');
    expect(out).toContain('cmd b');
  });
});
