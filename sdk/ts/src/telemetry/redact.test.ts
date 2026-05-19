import { describe, it, expect } from 'vitest';
import * as os from 'node:os';
import { redact, redactString } from './redact';

describe('redactString — single pattern coverage', () => {
  it('redacts emails', () => {
    expect(redactString('contact me at jane.doe+sub@example.co.uk now')).toBe(
      'contact me at <redacted:email> now',
    );
  });

  it('redacts IPv4 addresses', () => {
    expect(redactString('host 10.0.0.1 saw 192.168.1.255')).toBe(
      'host <redacted:ipv4> saw <redacted:ipv4>',
    );
  });

  it('redacts IPv6 addresses (full 8-group form)', () => {
    expect(
      redactString('peer 2001:db8:1234:5678:abcd:ef01:2345:6789 down'),
    ).toBe('peer <redacted:ipv6> down');
  });

  it('redacts IPv6 compressed `::` form', () => {
    // Cross-SDK pattern (matches PHP) covers compressed forms.
    expect(redactString('addr 2001:db8::1 here')).toBe(
      'addr <redacted:ipv6> here',
    );
  });

  it('redacts IPv6 link-local fe80::', () => {
    expect(redactString('peer fe80::1234:5678:abcd:ef01 up')).toBe(
      'peer <redacted:ipv6> up',
    );
  });

  it('does NOT redact bare `::1` loopback (PHP-parity under-match)', () => {
    // The PHP-parity pattern requires at least one hex group BEFORE
    // `::`, so the bare `::1` loopback is intentionally NOT matched.
    // Documented trade-off (parity > drift); see redact.ts header.
    expect(redactString('ping ::1 ok')).toBe('ping ::1 ok');
  });

  it('IPv4-mapped `::ffff:a.b.c.d` — IPv4 pass mops up the tail', () => {
    // No leading hex group before `::`, so the IPv6 pass skips the
    // `::ffff` prefix; the IPv4 pass then redacts the dotted-quad.
    const out = redactString('hybrid ::ffff:192.168.1.1 here');
    expect(out).not.toContain('192.168.1.1');
    expect(out).toContain('<redacted:ipv4>');
  });

  it('redacts sk- tokens', () => {
    expect(redactString('OPENAI=sk-AbCdEf12_-ZZ stop')).toBe(
      'OPENAI=<redacted:token> stop',
    );
  });

  it('redacts GitHub tokens (ghp/gho/ghu/ghs/ghr)', () => {
    for (const prefix of ['ghp', 'gho', 'ghu', 'ghs', 'ghr']) {
      const token = `${prefix}_${'A'.repeat(20)}`;
      expect(redactString(`token=${token} done`)).toBe(
        'token=<redacted:token> done',
      );
    }
  });

  it('redacts xoxb Slack tokens', () => {
    const tok = `xoxb-1-2-${'a'.repeat(30)}`;
    expect(redactString(`slack ${tok}!`)).toBe('slack <redacted:token>!');
  });

  it('rewrites $HOME prefix to literal $HOME', () => {
    const home = os.homedir();
    expect(redactString(`config at ${home}/.config/kit/x.yaml`)).toBe(
      'config at $HOME/.config/kit/x.yaml',
    );
  });

  it('leaves strings without secrets unchanged', () => {
    expect(redactString('all clear, nothing to see')).toBe(
      'all clear, nothing to see',
    );
  });
});

describe('redact — recursive walks', () => {
  it('walks nested objects', () => {
    const input = {
      user: 'alice@example.com',
      meta: { ip: '127.0.0.1', notes: 'safe' },
    };
    expect(redact(input)).toEqual({
      user: '<redacted:email>',
      meta: { ip: '<redacted:ipv4>', notes: 'safe' },
    });
  });

  it('walks arrays of mixed types', () => {
    const input = ['user@host.io', 42, true, null, undefined, 'plain'];
    expect(redact(input)).toEqual([
      '<redacted:email>',
      42,
      true,
      null,
      undefined,
      'plain',
    ]);
  });

  it('walks arrays of objects', () => {
    const input = [{ ip: '8.8.8.8' }, { ip: '1.1.1.1' }];
    expect(redact(input)).toEqual([
      { ip: '<redacted:ipv4>' },
      { ip: '<redacted:ipv4>' },
    ]);
  });

  it('passes non-string scalars through', () => {
    expect(redact(123)).toBe(123);
    expect(redact(0)).toBe(0);
    expect(redact(true)).toBe(true);
    expect(redact(false)).toBe(false);
    expect(redact(null)).toBeNull();
    expect(redact(undefined)).toBeUndefined();
  });

  it('does not mutate the input object', () => {
    const input = { user: 'bob@example.com', count: 1 };
    const snap = JSON.stringify(input);
    redact(input);
    expect(JSON.stringify(input)).toBe(snap);
  });
});
