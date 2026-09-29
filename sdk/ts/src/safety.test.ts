import { describe, it, expect, afterEach } from 'vitest';
import { safetyGuard } from './safety';

// A piped (non-TTY) stdout has no `isTTY` at all; `@types/node` types it
// as a plain `boolean`, so model the absent value via defineProperty.
function setStdoutTTY(value: boolean | undefined): void {
  Object.defineProperty(process.stdout, 'isTTY', {
    value,
    configurable: true,
    writable: true,
  });
}

describe('safetyGuard', () => {
  const origIsTTY: boolean | undefined = process.stdout.isTTY;

  afterEach(() => {
    setStdoutTTY(origIsTTY);
  });

  it('read level always passes', () => {
    expect(() => safetyGuard('read')).not.toThrow();
  });

  it('read level passes even in non-TTY', () => {
    setStdoutTTY(undefined);
    expect(() => safetyGuard('read')).not.toThrow();
  });

  it('caution level passes in TTY', () => {
    setStdoutTTY(true);
    expect(() => safetyGuard('caution')).not.toThrow();
  });

  it('caution level requires --force in non-TTY', () => {
    setStdoutTTY(undefined);
    expect(() => safetyGuard('caution')).toThrow(/--force/);
  });

  it('caution level passes with force in non-TTY', () => {
    setStdoutTTY(undefined);
    expect(() => safetyGuard('caution', { force: true })).not.toThrow();
  });

  it('dangerous level throws without --force', () => {
    setStdoutTTY(true);
    expect(() => safetyGuard('dangerous')).toThrow(/--force/);
  });

  it('dangerous level passes with --force', () => {
    expect(() => safetyGuard('dangerous', { force: true })).not.toThrow();
  });

  it('dangerous level requires --force in non-TTY', () => {
    setStdoutTTY(undefined);
    expect(() => safetyGuard('dangerous')).toThrow(/--force/);
  });

  it('dangerous level passes with --force in non-TTY', () => {
    setStdoutTTY(undefined);
    expect(() => safetyGuard('dangerous', { force: true })).not.toThrow();
  });
});
