import { describe, it, expect } from 'vitest';
import { TextFlag } from './textflag';

describe('TextFlag', () => {
  it('replace by default', () => {
    const tf = new TextFlag();
    tf.set('hello');
    expect(tf.value()).toBe('hello');
    tf.set('world');
    expect(tf.value()).toBe('world');
  });

  it('replace explicit with =', () => {
    const tf = new TextFlag('old');
    tf.set('=new');
    expect(tf.value()).toBe('new');
  });

  it('append on new line with +', () => {
    const tf = new TextFlag('first');
    tf.set('+second');
    expect(tf.value()).toBe('first\nsecond');
  });

  it('append multiple lines', () => {
    const tf = new TextFlag('line1');
    tf.set('+line2');
    tf.set('+line3');
    expect(tf.value()).toBe('line1\nline2\nline3');
  });

  it('append inline with +=', () => {
    const tf = new TextFlag('hello');
    tf.set('+= world');
    expect(tf.value()).toBe('hello world');
  });

  it('prepend on new line with ^', () => {
    const tf = new TextFlag('second');
    tf.set('^first');
    expect(tf.value()).toBe('first\nsecond');
  });

  it('prepend inline with ^=', () => {
    const tf = new TextFlag('world');
    tf.set('^=hello ');
    expect(tf.value()).toBe('hello world');
  });

  it('clear with bare =', () => {
    const tf = new TextFlag('something');
    tf.set('=');
    expect(tf.value()).toBe('');
  });

  it('append to empty skips newline', () => {
    const tf = new TextFlag();
    tf.set('+line');
    expect(tf.value()).toBe('line');
  });

  it('prepend to empty skips newline', () => {
    const tf = new TextFlag();
    tf.set('^line');
    expect(tf.value()).toBe('line');
  });

  it('toString returns value', () => {
    const tf = new TextFlag('hello');
    expect(tf.toString()).toBe('hello');
  });

  it('= escapes literal + prefix', () => {
    const tf = new TextFlag();
    tf.set('=+ppl');
    expect(tf.value()).toBe('+ppl');
  });

  it('= escapes literal ^ prefix', () => {
    const tf = new TextFlag();
    tf.set('=^weird');
    expect(tf.value()).toBe('^weird');
  });

  it('= escapes literal = prefix', () => {
    const tf = new TextFlag();
    tf.set('==equals');
    expect(tf.value()).toBe('=equals');
  });

  it('mixed operations', () => {
    const tf = new TextFlag();
    tf.set('base');
    tf.set('+appended');
    tf.set('^prepended');
    expect(tf.value()).toBe('prepended\nbase\nappended');
  });

  it('parseArg works as Commander option parser', () => {
    const tf = new TextFlag();
    let result = tf.parseArg('base', '');
    result = tf.parseArg('+added', result);
    expect(result).toBe('base\nadded');
  });
});
