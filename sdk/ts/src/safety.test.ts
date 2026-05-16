import { describe, it, expect, afterEach } from 'vitest';
import { safetyGuard } from './safety';

describe('safetyGuard', () => {
  const origIsTTY = process.stdout.isTTY;

  afterEach(() => {
    process.stdout.isTTY = origIsTTY;
  });

  it('read level always passes', () => {
    expect(() => safetyGuard('read')).not.toThrow();
  });

  it('read level passes even in non-TTY', () => {
    process.stdout.isTTY = undefined;
    expect(() => safetyGuard('read')).not.toThrow();
  });

  it('caution level passes in TTY', () => {
    process.stdout.isTTY = true;
    expect(() => safetyGuard('caution')).not.toThrow();
  });

  it('caution level requires --force in non-TTY', () => {
    process.stdout.isTTY = undefined;
    expect(() => safetyGuard('caution')).toThrow(/--force/);
  });

  it('caution level passes with force in non-TTY', () => {
    process.stdout.isTTY = undefined;
    expect(() => safetyGuard('caution', { force: true })).not.toThrow();
  });

  it('dangerous level throws without --force', () => {
    process.stdout.isTTY = true;
    expect(() => safetyGuard('dangerous')).toThrow(/--force/);
  });

  it('dangerous level passes with --force', () => {
    expect(() => safetyGuard('dangerous', { force: true })).not.toThrow();
  });

  it('dangerous level requires --force in non-TTY', () => {
    process.stdout.isTTY = undefined;
    expect(() => safetyGuard('dangerous')).toThrow(/--force/);
  });

  it('dangerous level passes with --force in non-TTY', () => {
    process.stdout.isTTY = undefined;
    expect(() => safetyGuard('dangerous', { force: true })).not.toThrow();
  });
});
