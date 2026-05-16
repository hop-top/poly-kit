/**
 * Captures verbatim output for each smoke command listed in T-1011 so the
 * track report can include real-world output samples. Run with:
 *
 *   pnpm exec tsx test/output/smoke-capture.ts
 */
import { Command } from 'commander';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { dispatch } from '../../src/output/dispatch';
import { registerOutputFlags } from '../../src/output/flags';
import '../../src/output/builtins';

function makeProgram(argv: readonly string[]) {
  const program = new Command()
    .name('cli')
    .exitOverride()
    .configureOutput({ writeOut: () => {}, writeErr: () => {} });
  registerOutputFlags(program);
  program.action(() => {});
  program.parse(['node', 'cli', ...argv], { from: 'node' });
  return program;
}

const rows = [
  { id: '1', name: 'Alice', status: 'active' },
  { id: '2', name: 'Bob', status: 'idle' },
];

const tmp = mkdtempSync(join(tmpdir(), 'smoke-cap-'));

async function run() {
  const out = (label: string) => process.stderr.write(`\n=== ${label} ===\n`);

  // 1. cli list
  out('cli list');
  await dispatch(makeProgram([]), rows);

  // 2. cli list --format json
  out('cli list --format json');
  await dispatch(makeProgram(['--format', 'json']), rows);

  // 3. cli list --format csv --format-opt delimiter=';'
  out("cli list --format csv --format-opt delimiter=';'");
  await dispatch(makeProgram(['--format', 'csv', '--format-opt', 'delimiter=;']), rows);

  // 4. cli list --cols name,status -o /tmp/x.csv
  const path1 = join(tmp, 'x.csv');
  out(`cli list --cols name,status -o ${path1}`);
  await dispatch(makeProgram(['--cols', 'name,status', '-o', path1]), rows);
  process.stdout.write(`(file ${path1}):\n${readFileSync(path1, 'utf8')}`);
  rmSync(path1);

  // 5. cli list -o /tmp/x.json (ext infers json)
  const path2 = join(tmp, 'x.json');
  out(`cli list -o ${path2} (ext infers json)`);
  await dispatch(makeProgram(['-o', path2]), rows);
  process.stdout.write(`(file ${path2}):\n${readFileSync(path2, 'utf8')}`);
  rmSync(path2);

  // 6. cli list -o /tmp/x.csv --format json (mismatch)
  const path3 = join(tmp, 'x.csv');
  out(`cli list -o ${path3} --format json (mismatch)`);
  try {
    await dispatch(makeProgram(['-o', path3, '--format', 'json']), rows);
  } catch (err) {
    process.stdout.write(`error: ${(err as Error).message}\n`);
  }

  // 7. cli list --format-help
  out('cli list --format-help');
  await dispatch(makeProgram(['--format-help']), []);

  // 8. cli list --format-help csv
  out('cli list --format-help csv');
  await dispatch(makeProgram(['--format-help', 'csv']), []);
}

run().catch(err => {
  console.error(err);
  process.exit(1);
});
