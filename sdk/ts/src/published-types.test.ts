/**
 * Consumer-side type-check of the published declarations.
 *
 * The package's own `tsc` run only ever sees `src/`; what adopters get is
 * the bundled `dist/*.d.ts` that tsup emits. The bundler can drop imports
 * or turn a type into a value while rolling declarations into shared
 * chunks, and neither `src/` type-checking nor `skipLibCheck: true`
 * consumers would notice. This test builds the package into a staged
 * `node_modules/@hop-top/kit`, then type-checks a consumer importing
 * every `exports` subpath with `skipLibCheck: false` under each
 * `moduleResolution` mode adopters use.
 */
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { copyFileSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import * as path from 'node:path';
import * as ts from 'typescript';

const pkgDir = path.join(__dirname, '..');
const pkg = JSON.parse(readFileSync(path.join(pkgDir, 'package.json'), 'utf8')) as {
  name: string;
  exports: Record<string, unknown>;
  scripts: Record<string, string>;
};

// Inside the package rather than the OS temp dir: the staged package's
// declarations import commander, zod, @types/node and friends, which
// only resolve from a path under sdk/ts/node_modules' reach.
const stageDir = path.join(pkgDir, `.consumer-types-${process.pid}`);
const stagedPkgDir = path.join(stageDir, 'node_modules', ...pkg.name.split('/'));

const modes: Record<string, Pick<ts.CompilerOptions, 'module' | 'moduleResolution'>> = {
  // node10 ignores `exports`; subpaths resolve only through `typesVersions`.
  node10: { module: ts.ModuleKind.CommonJS, moduleResolution: ts.ModuleResolutionKind.Node10 },
  node16: { module: ts.ModuleKind.Node16, moduleResolution: ts.ModuleResolutionKind.Node16 },
  nodenext: { module: ts.ModuleKind.NodeNext, moduleResolution: ts.ModuleResolutionKind.NodeNext },
  bundler: { module: ts.ModuleKind.ESNext, moduleResolution: ts.ModuleResolutionKind.Bundler },
};

/** Consumer source: one namespace import per exports subpath, plus typed uses. */
function consumerSource(): string {
  const specs = Object.keys(pkg.exports).map((k) => (k === '.' ? pkg.name : `${pkg.name}/${k.slice(2)}`));
  const imports = specs.map((s, i) => `import * as m${i} from '${s}';`).join('\n');
  return `${imports}
import type { CliError } from '${pkg.name}/output';
import { output } from '${pkg.name}';

// Types re-exported through the root barrel's namespaces must stay types.
export const viaSubpath: CliError | null = output.wrapError(new Error('x'), output.CODE_GENERIC, output.EXIT_GENERIC);
export const viaNamespace: output.CliError | null = viaSubpath;
export const all = [${specs.map((_, i) => `m${i}`).join(', ')}];
`;
}

describe('published declarations', () => {
  const files: string[] = [];

  beforeAll(() => {
    rmSync(stageDir, { recursive: true, force: true });
    mkdirSync(stagedPkgDir, { recursive: true });
    copyFileSync(path.join(pkgDir, 'package.json'), path.join(stagedPkgDir, 'package.json'));

    // Run the real build script, redirected into the staged package.
    const [bin, ...args] = pkg.scripts.build.split(/\s+/);
    expect(bin).toBe('tsup');
    execFileSync(
      path.join(pkgDir, 'node_modules', '.bin', 'tsup'),
      [...args, '--out-dir', path.join(stagedPkgDir, 'dist'), '--silent'],
      { cwd: pkgDir, stdio: 'pipe' },
    );

    // Own package.json: without it the consumer's nearest manifest is
    // sdk/ts's, and a self-reference import would resolve the package
    // to ./dist instead of the staged build.
    writeFileSync(path.join(stageDir, 'package.json'), '{ "name": "consumer", "private": true }\n');

    // Both module formats: .mts is an ESM consumer, .cts a CJS one.
    const src = consumerSource();
    for (const ext of ['mts', 'cts']) {
      const file = path.join(stageDir, `consumer.${ext}`);
      writeFileSync(file, src);
      files.push(file);
    }
  }, 120_000);

  afterAll(() => { rmSync(stageDir, { recursive: true, force: true }); });

  it.each(Object.keys(modes))('type-check clean with skipLibCheck false (moduleResolution %s)', (mode) => {
    const program = ts.createProgram(files, {
      ...modes[mode],
      target: ts.ScriptTarget.ES2022,
      strict: true,
      noEmit: true,
      skipLibCheck: false,
      // `tsc --init` default; node16/nodenext imply it. Without it a
      // node10 consumer trips on zod's own default imports (TS1259).
      esModuleInterop: true,
      types: ['node'],
      typeRoots: [path.join(pkgDir, 'node_modules', '@types')],
    });
    const diagnostics = ts.getPreEmitDiagnostics(program);
    const report = ts.formatDiagnostics(diagnostics, {
      getCanonicalFileName: (f) => f,
      getCurrentDirectory: () => stageDir,
      getNewLine: () => '\n',
    });
    expect(report).toBe('');
  }, 60_000);
});
