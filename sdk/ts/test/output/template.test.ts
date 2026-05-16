import { describe, it, expect, vi } from 'vitest';
import { Command } from 'commander';
import { renderTemplate } from '../../src/output/template';
import { dispatch } from '../../src/output/dispatch';
import { registerOutputFlags } from '../../src/output/flags';
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

function makeProgram(argv: readonly string[]) {
  const program = new Command()
    .name('test')
    .exitOverride()
    .configureOutput({ writeOut: () => {}, writeErr: () => {} });
  registerOutputFlags(program);
  program.action(() => {});
  program.parse(['node', 'test', ...argv], { from: 'node' });
  return program;
}

function captureStdout(): { restore: () => void; out: () => string } {
  const chunks: Buffer[] = [];
  const spy = vi.spyOn(process.stdout, 'write').mockImplementation(
    ((c: string | Buffer) => {
      chunks.push(Buffer.isBuffer(c) ? c : Buffer.from(c));
      return true;
    }) as typeof process.stdout.write,
  );
  return {
    restore: () => spy.mockRestore(),
    out: () => Buffer.concat(chunks).toString(),
  };
}

const rows = [
  { id: '1', name: 'Alice' },
  { id: '2', name: 'Bob' },
];

describe('renderTemplate', () => {
  it('renders simple iteration', async () => {
    const s = makeStream();
    const tpl = '<% it.items.forEach(function(item){ %><%= item.name %>;<% }) %>';
    await renderTemplate(s.stream, tpl, rows);
    expect(s.captured()).toBe('Alice;Bob;');
  });

  it('exposes cols list', async () => {
    const s = makeStream();
    const tpl = '<%= it.cols.join(",") %>';
    await renderTemplate(s.stream, tpl, rows);
    expect(s.captured()).toBe('id,name');
  });

  it('wraps parse errors', async () => {
    const s = makeStream();
    await expect(renderTemplate(s.stream, '<% invalid', rows)).rejects.toThrow(
      /template error:/,
    );
  });
});

describe('dispatch — --template', () => {
  it('runs template via Commander wiring', async () => {
    const program = makeProgram([
      '--template', '<%= it.items[0].name %>',
    ]);
    const cap = captureStdout();
    try {
      await dispatch(program, rows);
    } finally {
      cap.restore();
    }
    expect(cap.out()).toBe('Alice');
  });

  it('--template + --cols throws mutual-exclusion error', async () => {
    const program = makeProgram([
      '--template', '<%= 1 %>',
      '--cols', 'id',
    ]);
    await expect(dispatch(program, rows)).rejects.toThrow(
      /--template and --cols are mutually exclusive/,
    );
  });
});
