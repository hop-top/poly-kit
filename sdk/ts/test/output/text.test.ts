import { describe, it, expect, vi } from 'vitest';
import { textFormatter } from '../../src/output/formatters/text';
import { defaultRegistry } from '../../src/output/registry';
import '../../src/output/builtins';

function makeStream() {
  const chunks: string[] = [];
  const stream = {
    write: vi.fn((c: string) => {
      chunks.push(c);
      return true;
    }),
  } as unknown as NodeJS.WritableStream;
  return { stream, captured: () => chunks.join('') };
}

const rows = [
  { id: '1', name: 'Alice' },
  { id: '2', name: 'Bob' },
];

describe('text — kv style (default)', () => {
  it('emits key=value lines, blank line between records', () => {
    const s = makeStream();
    textFormatter.render(s.stream, rows, { style: 'kv', separator: '=' }, []);
    expect(s.captured()).toBe('id=1\nname=Alice\n\nid=2\nname=Bob\n');
  });

  it('honors custom separator', () => {
    const s = makeStream();
    textFormatter.render(s.stream, rows, { style: 'kv', separator: ' -> ' }, []);
    expect(s.captured()).toBe('id -> 1\nname -> Alice\n\nid -> 2\nname -> Bob\n');
  });
});

describe('text — lines style', () => {
  it('emits tab-separated rows, no header', () => {
    const s = makeStream();
    textFormatter.render(s.stream, rows, { style: 'lines' }, []);
    expect(s.captured()).toBe('1\tAlice\n2\tBob\n');
  });
});

describe('text — paragraph style', () => {
  it('emits Record N: blocks with field: value lines', () => {
    const s = makeStream();
    textFormatter.render(s.stream, rows, { style: 'paragraph' }, []);
    expect(s.captured()).toBe(
      'Record 1:\n  id: 1\n  name: Alice\n\nRecord 2:\n  id: 2\n  name: Bob\n',
    );
  });
});

describe('text — cols filter', () => {
  it('limits to selected columns in kv', () => {
    const s = makeStream();
    textFormatter.render(s.stream, rows, { style: 'kv', separator: '=' }, ['name']);
    expect(s.captured()).toBe('name=Alice\n\nname=Bob\n');
  });
});

describe('text — empty input', () => {
  it('produces no output', () => {
    const s = makeStream();
    textFormatter.render(s.stream, [], {}, []);
    expect(s.captured()).toBe('');
  });
});

describe('text — registry', () => {
  it('registered as "text" with .txt extension', () => {
    expect(defaultRegistry.lookup('text')).toBe(textFormatter);
    expect(defaultRegistry.extensionMap().get('.txt')).toBe('text');
  });
});
