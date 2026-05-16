import { describe, it, expect } from 'vitest';
import { NoAuth } from './auth';

describe('NoAuth', () => {
  it('inspect returns anonymous credential', async () => {
    const auth = new NoAuth();
    const cred = await auth.inspect();
    expect(cred.source).toBe('none');
    expect(cred.identity).toBe('anonymous');
    expect(cred.scopes).toEqual([]);
    expect(cred.renewable).toBe(false);
  });

  it('inspect returns no expiration', async () => {
    const auth = new NoAuth();
    const cred = await auth.inspect();
    expect(cred.expiresAt).toBeUndefined();
  });

  it('refresh resolves without error', async () => {
    const auth = new NoAuth();
    await expect(auth.refresh()).resolves.toBeUndefined();
  });
});
