import { describe, it, expect } from 'vitest';
import {
  CompletionRegistry,
  staticCompleter,
  staticValues,
  funcCompleter,
  prefixedCompleter,
  configKeysCompleter,
  fileCompleter,
  dirCompleter,
  type CompletionItem,
} from './completion-values';

describe('staticCompleter', () => {
  it('returns all items when prefix is empty', () => {
    const c = staticCompleter(
      { value: 'leo', description: 'Low Earth Orbit' },
      { value: 'geo', description: 'Geostationary' },
    );
    expect(c.complete('')).toEqual([
      { value: 'leo', description: 'Low Earth Orbit' },
      { value: 'geo', description: 'Geostationary' },
    ]);
  });

  it('filters by prefix', () => {
    const c = staticCompleter(
      { value: 'leo' },
      { value: 'lunar' },
      { value: 'geo' },
    );
    expect(c.complete('l')).toEqual([
      { value: 'leo' },
      { value: 'lunar' },
    ]);
  });

  it('filters case-insensitively', () => {
    const c = staticCompleter(
      { value: 'LEO' },
      { value: 'geo' },
    );
    expect(c.complete('le')).toEqual([{ value: 'LEO' }]);
    expect(c.complete('GE')).toEqual([{ value: 'geo' }]);
  });
});

describe('staticValues', () => {
  it('wraps strings into CompletionItems', () => {
    const c = staticValues('leo', 'geo', 'lunar');
    expect(c.complete('')).toEqual([
      { value: 'leo' },
      { value: 'geo' },
      { value: 'lunar' },
    ]);
  });

  it('filters by prefix', () => {
    const c = staticValues('leo', 'geo', 'lunar');
    expect(c.complete('g')).toEqual([{ value: 'geo' }]);
  });
});

describe('funcCompleter', () => {
  it('invokes callback with prefix', () => {
    let captured = '';
    const c = funcCompleter((prefix) => {
      captured = prefix;
      return [{ value: 'dynamic-' + prefix }];
    });
    const result = c.complete('foo');
    expect(captured).toBe('foo');
    expect(result).toEqual([{ value: 'dynamic-foo' }]);
  });

  it('returns empty when callback returns empty', () => {
    const c = funcCompleter(() => []);
    expect(c.complete('x')).toEqual([]);
  });
});

describe('prefixedCompleter', () => {
  it('completes dimension prefix when no colon', () => {
    const inner = staticValues('a', 'b');
    const c = prefixedCompleter('env', inner);
    const result = c.complete('');
    expect(result).toEqual([{ value: 'env:', description: 'env:...' }]);
  });

  it('completes values after colon', () => {
    const inner = staticValues('dev', 'prod', 'staging');
    const c = prefixedCompleter('env', inner);
    const result = c.complete('env:p');
    expect(result).toEqual([{ value: 'env:prod' }]);
  });

  it('returns all values when prefix matches dimension + colon', () => {
    const inner = staticValues('x', 'y');
    const c = prefixedCompleter('dim', inner);
    const result = c.complete('dim:');
    expect(result).toEqual([
      { value: 'dim:x' },
      { value: 'dim:y' },
    ]);
  });

  it('returns nothing when prefix does not match dimension', () => {
    const inner = staticValues('a');
    const c = prefixedCompleter('env', inner);
    expect(c.complete('other:')).toEqual([]);
  });
});

describe('configKeysCompleter', () => {
  it('returns top-level keys', () => {
    const c = configKeysCompleter({ name: 'x', version: '1', debug: true });
    const items = c.complete('');
    const values = items.map((i: CompletionItem) => i.value);
    expect(values).toContain('name');
    expect(values).toContain('version');
    expect(values).toContain('debug');
  });

  it('returns nested keys with dot notation', () => {
    const c = configKeysCompleter({ db: { host: 'localhost', port: 5432 } });
    const items = c.complete('db.');
    const values = items.map((i: CompletionItem) => i.value);
    expect(values).toContain('db.host');
    expect(values).toContain('db.port');
  });

  it('filters by prefix', () => {
    const c = configKeysCompleter({ name: 'x', namespace: 'y', version: '1' });
    const items = c.complete('na');
    const values = items.map((i: CompletionItem) => i.value);
    expect(values).toEqual(['name', 'namespace']);
  });
});

describe('fileCompleter', () => {
  it('returns items with value field', () => {
    const c = fileCompleter('.ts', '.js');
    const items = c.complete('');
    // file completer returns a marker item for shell-level file completion
    expect(items.length).toBeGreaterThanOrEqual(1);
    expect(items[0]).toHaveProperty('value');
  });
});

describe('dirCompleter', () => {
  it('returns items with value field', () => {
    const c = dirCompleter();
    const items = c.complete('');
    expect(items.length).toBeGreaterThanOrEqual(1);
    expect(items[0]).toHaveProperty('value');
  });
});

describe('CompletionRegistry', () => {
  it('registers and retrieves flag completer', () => {
    const reg = new CompletionRegistry();
    const c = staticValues('a', 'b');
    reg.register('--orbit', c);
    expect(reg.forFlag('--orbit')).toBe(c);
  });

  it('returns undefined for unregistered flag', () => {
    const reg = new CompletionRegistry();
    expect(reg.forFlag('--unknown')).toBeUndefined();
  });

  it('registers and retrieves arg completer', () => {
    const reg = new CompletionRegistry();
    const c = staticValues('mission-1', 'mission-2');
    reg.registerArg('launch', 0, c);
    expect(reg.forArg('launch', 0)).toBe(c);
  });

  it('returns undefined for unregistered arg', () => {
    const reg = new CompletionRegistry();
    expect(reg.forArg('launch', 0)).toBeUndefined();
  });

  it('distinguishes arg positions', () => {
    const reg = new CompletionRegistry();
    const c0 = staticValues('pos0');
    const c1 = staticValues('pos1');
    reg.registerArg('cmd', 0, c0);
    reg.registerArg('cmd', 1, c1);
    expect(reg.forArg('cmd', 0)).toBe(c0);
    expect(reg.forArg('cmd', 1)).toBe(c1);
  });

  it('overwrites previous registration for same flag', () => {
    const reg = new CompletionRegistry();
    const c1 = staticValues('old');
    const c2 = staticValues('new');
    reg.register('--flag', c1);
    reg.register('--flag', c2);
    expect(reg.forFlag('--flag')).toBe(c2);
  });
});
