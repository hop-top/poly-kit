import { describe, it, expect } from 'vitest';
import { SetFlag } from './setflag';

describe('SetFlag', () => {
  it('appends by default', () => {
    const sf = new SetFlag();
    sf.set('feat');
    sf.set('docs');
    expect(sf.values()).toEqual(['feat', 'docs']);
  });

  it('appends with + prefix', () => {
    const sf = new SetFlag();
    sf.set('+feat');
    sf.set('+docs');
    expect(sf.values()).toEqual(['feat', 'docs']);
  });

  it('removes with - prefix', () => {
    const sf = new SetFlag(['feat', 'bug', 'docs']);
    sf.set('-bug');
    expect(sf.values()).toEqual(['feat', 'docs']);
  });

  it('remove nonexistent is no-op', () => {
    const sf = new SetFlag(['feat']);
    sf.set('-nope');
    expect(sf.values()).toEqual(['feat']);
  });

  it('replaces all with = prefix', () => {
    const sf = new SetFlag(['old1', 'old2']);
    sf.set('=new1,new2');
    expect(sf.values()).toEqual(['new1', 'new2']);
  });

  it('clears all with bare =', () => {
    const sf = new SetFlag(['a', 'b', 'c']);
    sf.set('=');
    expect(sf.values()).toEqual([]);
  });

  it('toString returns comma-joined', () => {
    const sf = new SetFlag(['a', 'b']);
    expect(sf.toString()).toBe('a,b');
  });

  it('toString empty returns empty string', () => {
    const sf = new SetFlag();
    expect(sf.toString()).toBe('');
  });

  it('deduplicates', () => {
    const sf = new SetFlag();
    sf.set('feat');
    sf.set('feat');
    expect(sf.values()).toEqual(['feat']);
  });

  it('mixed operations', () => {
    const sf = new SetFlag();
    sf.set('a');
    sf.set('b');
    sf.set('c');
    sf.set('-b');
    sf.set('+d');
    expect(sf.values()).toEqual(['a', 'c', 'd']);
  });

  it('replace after append', () => {
    const sf = new SetFlag();
    sf.set('a');
    sf.set('b');
    sf.set('=x');
    expect(sf.values()).toEqual(['x']);
  });

  it('comma in append splits', () => {
    const sf = new SetFlag();
    sf.set('a,b');
    expect(sf.values()).toEqual(['a', 'b']);
  });

  it('= escapes literal + prefix', () => {
    const sf = new SetFlag();
    sf.set('=+ppl');
    expect(sf.values()).toEqual(['+ppl']);
  });

  it('= escapes literal - prefix', () => {
    const sf = new SetFlag();
    sf.set('=-negative');
    expect(sf.values()).toEqual(['-negative']);
  });

  it('= escapes literal = prefix', () => {
    const sf = new SetFlag();
    sf.set('==equals');
    expect(sf.values()).toEqual(['=equals']);
  });

  it('parseArg works as Commander option parser', () => {
    const sf = new SetFlag();
    // Commander calls parseArg(value, previous)
    let result = sf.parseArg('feat', []);
    result = sf.parseArg('+docs', result);
    result = sf.parseArg('-feat', result);
    expect(result).toEqual(['docs']);
  });
});
