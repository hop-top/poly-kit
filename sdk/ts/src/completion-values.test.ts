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
  resolveCompletions,
  directiveName,
  ShellCompDirective,
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
    expect(result).toEqual([{ value: 'env:', description: 'env values' }]);
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
  it('returns top-level keys', async () => {
    const c = configKeysCompleter({ name: 'x', version: '1', debug: true });
    const items = await c.complete('');
    const values = items.map((i: CompletionItem) => i.value);
    expect(values).toContain('name');
    expect(values).toContain('version');
    expect(values).toContain('debug');
  });

  it('returns nested keys with dot notation', async () => {
    const c = configKeysCompleter({ db: { host: 'localhost', port: 5432 } });
    const items = await c.complete('db.');
    const values = items.map((i: CompletionItem) => i.value);
    expect(values).toContain('db.host');
    expect(values).toContain('db.port');
  });

  it('returns leaf keys only, lowercased', async () => {
    const c = configKeysCompleter({ Server: { Port: 1 }, tags: ['a'] });
    const values = (await c.complete('')).map((i: CompletionItem) => i.value);
    expect(values).toEqual(['server.port', 'tags']);
    expect((await c.complete('SER')).map((i: CompletionItem) => i.value))
      .toEqual(['server.port']);
  });

  it('filters by prefix', async () => {
    const c = configKeysCompleter({ name: 'x', namespace: 'y', version: '1' });
    const items = await c.complete('na');
    const values = items.map((i: CompletionItem) => i.value);
    expect(values).toEqual(['name', 'namespace']);
  });
});

describe('fileCompleter', () => {
  it('returns its extensions and filters by extension', async () => {
    const c = fileCompleter('.ts', '.js');
    expect(await c.complete('')).toEqual([{ value: '.ts' }, { value: '.js' }]);
    expect(await resolveCompletions(c, 'x')).toEqual({
      completions: ['.ts', '.js'], directive: ShellCompDirective.FilterFileExt,
    });
  });

  it('without extensions falls back to default completion', async () => {
    expect(await resolveCompletions(fileCompleter(), '')).toEqual({
      completions: [], directive: ShellCompDirective.Default,
    });
  });
});

describe('dirCompleter', () => {
  it('completes directories only', async () => {
    const c = dirCompleter();
    expect(await c.complete('')).toEqual([]);
    expect(await resolveCompletions(c, 'src')).toEqual({
      completions: [], directive: ShellCompDirective.FilterDirs,
    });
  });
});

describe('resolveCompletions', () => {
  it('joins descriptions with a tab and completes no files', async () => {
    const c = staticCompleter({ value: 'a', description: 'A' }, { value: 'b' });
    expect(await resolveCompletions(c, '')).toEqual({
      completions: ['a\tA', 'b'], directive: ShellCompDirective.NoFileComp,
    });
  });

  it('turns a thrown or rejected completer into the error directive', async () => {
    const sync = funcCompleter(() => { throw new Error('down'); });
    const rejected = funcCompleter(async () => { throw new Error('down'); });
    for (const c of [sync, rejected, prefixedCompleter('env', rejected)]) {
      expect(await resolveCompletions(c, 'env:')).toEqual({
        completions: [], directive: ShellCompDirective.Error,
      });
    }
  });

  it('passes the context through to the completer', async () => {
    let seen;
    const c = funcCompleter((_p, ctx) => { seen = ctx; return []; });
    await resolveCompletions(c, '', { commandPath: 'deploy', args: ['x'] });
    expect(seen).toEqual({ commandPath: 'deploy', args: ['x'] });
  });

  it('awaits async inner completers of prefixed', async () => {
    const c = prefixedCompleter('env', funcCompleter(async () => [{ value: 'prod' }]));
    expect(await resolveCompletions(c, 'env:')).toEqual({
      completions: ['env:prod'], directive: ShellCompDirective.NoFileComp,
    });
  });
});

describe('directiveName', () => {
  it('names cobra directives', () => {
    expect(directiveName(0)).toBe('ShellCompDirectiveDefault');
    expect(directiveName(4)).toBe('ShellCompDirectiveNoFileComp');
    expect(directiveName(6)).toBe('ShellCompDirectiveNoSpace, ShellCompDirectiveNoFileComp');
    expect(directiveName(64)).toBe('ERROR: unexpected ShellCompDirective value: 64');
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
