/**
 * web.ts — browser terminal driver for spaced.
 *
 * Wires the terminal UI (index.html) to the spaced command handlers via
 * the browser adapter in terminal.ts. Handles:
 *   - Opening demo animation sequence
 *   - User input (keyboard + run button)
 *   - Suggestion chips
 *   - Command history (↑/↓)
 *   - Copy install command
 */

import { route, DEMO_SEQUENCE, SUGGESTIONS } from './router';

// ---------------------------------------------------------------------------
// DOM refs
// ---------------------------------------------------------------------------

const outputEl  = document.getElementById('output')!;
const inputEl   = document.getElementById('input') as HTMLInputElement;
const runBtn    = document.getElementById('runBtn')!;
const chipsEl   = document.getElementById('chips')!;
const copyBtn   = document.getElementById('copyBtn') as HTMLButtonElement;

// ---------------------------------------------------------------------------
// Output rendering
// ---------------------------------------------------------------------------

function appendLine(text: string, cls: string): void {
  const el = document.createElement('div');
  el.className = cls;
  el.textContent = text;
  outputEl.appendChild(el);
}

function appendBlock(text: string, cls: string): void {
  for (const line of text.split('\n')) {
    appendLine(line, cls);
  }
}

function appendSep(): void {
  appendLine('', 'line-dim');
}

function appendHint(): void {
  const outer = document.createElement('div');
  outer.className = 'line-dim';

  const arrow = document.createElement('span');
  arrow.style.color = 'var(--green)';
  arrow.textContent = '↑';

  const text = document.createTextNode('  type a command above, or click a chip below');

  outer.appendChild(arrow);
  outer.appendChild(text);
  outputEl.appendChild(outer);
}

function scrollBottom(): void {
  outputEl.scrollTop = outputEl.scrollHeight;
}

// ---------------------------------------------------------------------------
// Command execution
// ---------------------------------------------------------------------------

let running = false;
const history: string[] = [];
let histIdx = -1;

async function execute(rawInput: string): Promise<void> {
  if (running) return;
  const input = rawInput.trim();
  if (!input) return;

  running = true;
  inputEl.disabled = true;
  runBtn.textContent = '…';

  // Strip leading "spaced " prefix if user typed it.
  const cmd = input.startsWith('spaced ') ? input.slice(7) : input;

  // Echo the command.
  appendLine(`spaced › ${cmd}`, 'line-cmd');

  try {
    const result = route(cmd);

    if (result.out.trim()) {
      appendBlock(result.out, 'line-out');
    }
    if (result.err.trim()) {
      appendBlock(result.err, 'line-err');
    }
    if (!result.out.trim() && !result.err.trim()) {
      appendLine('(no output)', 'line-dim');
    }
  } catch (e) {
    appendLine(`error: ${String(e)}`, 'line-err');
  }

  appendSep();
  scrollBottom();

  // History.
  if (history[0] !== cmd) history.unshift(cmd);
  if (history.length > 50) history.pop();
  histIdx = -1;

  running = false;
  inputEl.disabled = false;
  inputEl.value = '';
  inputEl.focus();
  runBtn.textContent = '↵ run';
}

// ---------------------------------------------------------------------------
// Opening animation
// ---------------------------------------------------------------------------

async function playDemo(): Promise<void> {
  await sleep(400);

  for (const cmd of DEMO_SEQUENCE) {
    const bare = cmd.startsWith('spaced ') ? cmd.slice(7) : cmd;
    inputEl.value = '';
    for (const ch of bare) {
      inputEl.value += ch;
      await sleep(18 + Math.random() * 14);
    }
    await sleep(320);
    await execute(bare);
    await sleep(600);
  }

  appendLine('', 'line-dim');
  appendHint();
  scrollBottom();
}

function sleep(ms: number): Promise<void> {
  return new Promise(r => setTimeout(r, ms));
}

// ---------------------------------------------------------------------------
// Event wiring
// ---------------------------------------------------------------------------

inputEl.addEventListener('keydown', async (e: KeyboardEvent) => {
  if (e.key === 'Enter') {
    e.preventDefault();
    await execute(inputEl.value);
    return;
  }
  if (e.key === 'ArrowUp') {
    e.preventDefault();
    if (histIdx < history.length - 1) {
      histIdx++;
      inputEl.value = history[histIdx];
      inputEl.setSelectionRange(inputEl.value.length, inputEl.value.length);
    }
    return;
  }
  if (e.key === 'ArrowDown') {
    e.preventDefault();
    if (histIdx > 0) {
      histIdx--;
      inputEl.value = history[histIdx];
    } else {
      histIdx = -1;
      inputEl.value = '';
    }
    return;
  }
});

runBtn.addEventListener('click', () => execute(inputEl.value));

for (const s of SUGGESTIONS) {
  const chip = document.createElement('button');
  chip.className = 'chip';
  chip.textContent = s;
  chip.addEventListener('click', () => {
    inputEl.value = s;
    inputEl.focus();
    execute(s);
  });
  chipsEl.appendChild(chip);
}

copyBtn.addEventListener('click', () => {
  navigator.clipboard.writeText('go install hop.top/kit/examples/spaced/go@latest')
    .then(() => {
      copyBtn.textContent = 'copied!';
      copyBtn.classList.add('copied');
      setTimeout(() => {
        copyBtn.textContent = 'copy';
        copyBtn.classList.remove('copied');
      }, 2000);
    })
    .catch(() => {
      copyBtn.textContent = 'copy';
    });
});

// ---------------------------------------------------------------------------
// Boot
// ---------------------------------------------------------------------------

inputEl.focus();
// Allow tests to skip the animation via ?skip-demo query param.
if (!new URLSearchParams(location.search).has('skip-demo')) {
  playDemo();
}
