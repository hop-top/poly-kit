import { describe, it, expect } from 'vitest';
import { Command } from 'commander';
import { registerSetFlag, registerTextFlag, FlagDisplay } from './flagregister';

describe('registerSetFlag', () => {
  it('prefix only — --tag works, no --add-tag', () => {
    const cmd = new Command();
    const sf = registerSetFlag(cmd, 'tag', 'tags', FlagDisplay.Prefix);
    cmd.parse(['node', 'test', '--tag', 'feat', '--tag', '+docs']);
    expect(sf.values()).toEqual(['feat', 'docs']);
    expect(cmd.options.find(o => o.long === '--add-tag')).toBeUndefined();
  });

  it('verbose only — --add-tag/--remove-tag work, no --tag', () => {
    const cmd = new Command();
    const sf = registerSetFlag(cmd, 'tag', 'tags', FlagDisplay.Verbose);
    cmd.parse(['node', 'test', '--add-tag', 'feat', '--add-tag', 'bug', '--remove-tag', 'bug']);
    expect(sf.values()).toEqual(['feat']);
    expect(cmd.options.find(o => o.long === '--tag')).toBeUndefined();
  });

  it('both — all forms work together', () => {
    const cmd = new Command();
    const sf = registerSetFlag(cmd, 'tag', 'tags', FlagDisplay.Both);
    cmd.parse(['node', 'test', '--tag', 'a', '--add-tag', 'b', '--tag', '-a']);
    expect(sf.values()).toEqual(['b']);
  });

  it('verbose --clear-tag clears all', () => {
    const cmd = new Command();
    const sf = registerSetFlag(cmd, 'tag', 'tags', FlagDisplay.Verbose);
    sf.set('existing');
    cmd.parse(['node', 'test', '--clear-tag']);
    expect(sf.values()).toEqual([]);
  });

  it('verbose --add-tag with + in value adds literally', () => {
    const cmd = new Command();
    const sf = registerSetFlag(cmd, 'tag', 'tags', FlagDisplay.Verbose);
    cmd.parse(['node', 'test', '--add-tag', '+ppl']);
    expect(sf.values()).toEqual(['+ppl']);
  });

  it('verbose --remove-tag with + in value removes literally', () => {
    const cmd = new Command();
    const sf = registerSetFlag(cmd, 'tag', 'tags', FlagDisplay.Verbose);
    sf.add('+ppl');
    cmd.parse(['node', 'test', '--remove-tag', '+ppl']);
    expect(sf.values()).toEqual([]);
  });

  it('default is prefix', () => {
    const cmd = new Command();
    registerSetFlag(cmd, 'tag', 'tags');
    expect(cmd.options.find(o => o.long === '--tag')).toBeDefined();
    expect(cmd.options.find(o => o.long === '--add-tag')).toBeUndefined();
  });
});

describe('registerTextFlag', () => {
  it('prefix only — --desc with + prefix', () => {
    const cmd = new Command();
    const tf = registerTextFlag(cmd, 'desc', 'description', FlagDisplay.Prefix);
    cmd.parse(['node', 'test', '--desc', 'base', '--desc', '+line2']);
    expect(tf.value()).toBe('base\nline2');
    expect(cmd.options.find(o => o.long === '--desc-append')).toBeUndefined();
  });

  it('verbose — --desc-append works', () => {
    const cmd = new Command();
    const tf = registerTextFlag(cmd, 'desc', 'description', FlagDisplay.Verbose);
    cmd.parse(['node', 'test', '--desc', 'base', '--desc-append', 'added']);
    expect(tf.value()).toBe('base\nadded');
  });

  it('verbose — --desc-append-inline works', () => {
    const cmd = new Command();
    const tf = registerTextFlag(cmd, 'desc', 'description', FlagDisplay.Verbose);
    cmd.parse(['node', 'test', '--desc', 'hello', '--desc-append-inline', ' world']);
    expect(tf.value()).toBe('hello world');
  });

  it('verbose --desc-append with + in value appends literally', () => {
    const cmd = new Command();
    const tf = registerTextFlag(cmd, 'desc', 'description', FlagDisplay.Verbose);
    cmd.parse(['node', 'test', '--desc', 'base', '--desc-append', '+1 improvement']);
    expect(tf.value()).toBe('base\n+1 improvement');
  });

  it('verbose --desc-prepend with ^ in value prepends literally', () => {
    const cmd = new Command();
    const tf = registerTextFlag(cmd, 'desc', 'description', FlagDisplay.Verbose);
    cmd.parse(['node', 'test', '--desc', 'base', '--desc-prepend', '^caret']);
    expect(tf.value()).toBe('^caret\nbase');
  });

  it('verbose — --desc-prepend works', () => {
    const cmd = new Command();
    const tf = registerTextFlag(cmd, 'desc', 'description', FlagDisplay.Verbose);
    cmd.parse(['node', 'test', '--desc', 'second', '--desc-prepend', 'first']);
    expect(tf.value()).toBe('first\nsecond');
  });
});
