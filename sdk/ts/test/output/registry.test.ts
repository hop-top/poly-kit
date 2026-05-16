import { describe, it, expect } from 'vitest';
import { Registry, newRegistry, defaultRegistry } from '../../src/output/registry';
import type { Formatter } from '../../src/output/formatter';

function fakeFormatter(key: string, exts: readonly string[] = []): Formatter {
  return {
    key,
    extensions: exts,
    options: [],
    render: () => {},
  };
}

describe('Registry — register/lookup', () => {
  it('registers and looks up a formatter', () => {
    const r = newRegistry();
    const f = fakeFormatter('foo');
    r.register(f);
    expect(r.lookup('foo')).toBe(f);
  });

  it('lookup returns undefined for unknown key', () => {
    const r = newRegistry();
    expect(r.lookup('nope')).toBeUndefined();
  });

  it('throws on duplicate key', () => {
    const r = newRegistry();
    r.register(fakeFormatter('foo'));
    expect(() => r.register(fakeFormatter('foo'))).toThrow(/already registered/);
  });

  it('throws on empty key', () => {
    const r = newRegistry();
    expect(() => r.register(fakeFormatter(''))).toThrow(/key is empty/);
  });
});

describe('Registry — override', () => {
  it('replaces existing key', () => {
    const r = newRegistry();
    const a = fakeFormatter('foo');
    const b = fakeFormatter('foo');
    r.register(a);
    r.override(b);
    expect(r.lookup('foo')).toBe(b);
  });

  it('registers when key absent', () => {
    const r = newRegistry();
    const f = fakeFormatter('foo');
    r.override(f);
    expect(r.lookup('foo')).toBe(f);
  });

  it('throws on empty key', () => {
    const r = newRegistry();
    expect(() => r.override(fakeFormatter(''))).toThrow(/key is empty/);
  });
});

describe('Registry — keys/formatters', () => {
  it('keys() returns sorted slice', () => {
    const r = newRegistry();
    r.register(fakeFormatter('json'));
    r.register(fakeFormatter('csv'));
    r.register(fakeFormatter('table'));
    expect(r.keys()).toEqual(['csv', 'json', 'table']);
  });

  it('formatters() returns sorted by key', () => {
    const r = newRegistry();
    const a = fakeFormatter('zzz');
    const b = fakeFormatter('aaa');
    r.register(a);
    r.register(b);
    expect(r.formatters().map(f => f.key)).toEqual(['aaa', 'zzz']);
  });
});

describe('Registry — extensionMap', () => {
  it('builds ext→key map', () => {
    const r = newRegistry();
    r.register(fakeFormatter('json', ['.json']));
    r.register(fakeFormatter('yaml', ['.yaml', '.yml']));
    const m = r.extensionMap();
    expect(m.get('.json')).toBe('json');
    expect(m.get('.yaml')).toBe('yaml');
    expect(m.get('.yml')).toBe('yaml');
  });

  it('lowercases extensions', () => {
    const r = newRegistry();
    r.register(fakeFormatter('csv', ['.CSV']));
    expect(r.extensionMap().get('.csv')).toBe('csv');
  });

  it('alphabetical key order is the deterministic tie-break on collision', () => {
    const r = newRegistry();
    // both claim ".x"; "z" registered first but "a" wins (later in iteration
    // order is keys-sorted, "a" then "z" — "z" wins because it overwrites).
    // Actually iteration is sorted asc, so last seen ("z") wins.
    r.register(fakeFormatter('z', ['.x']));
    r.register(fakeFormatter('a', ['.x']));
    expect(r.extensionMap().get('.x')).toBe('z');
  });
});

describe('Registry — isolation', () => {
  it('newRegistry instances are isolated', () => {
    const a = newRegistry();
    const b = newRegistry();
    a.register(fakeFormatter('foo'));
    expect(b.lookup('foo')).toBeUndefined();
  });

  it('Registry constructor produces isolated instance', () => {
    const r = new Registry();
    expect(r.keys().length).toBe(0);
  });
});

describe('Registry — defaultRegistry singleton', () => {
  it('exists at module level', () => {
    expect(defaultRegistry).toBeInstanceOf(Registry);
  });
});
