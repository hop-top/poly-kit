import { describe, expect, it } from 'vitest';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

import * as kit from './index.js';

const pkgDir = join(__dirname, '..');
const pkg = JSON.parse(readFileSync(join(pkgDir, 'package.json'), 'utf8')) as {
  exports: Record<string, Record<string, string>>;
  typesVersions?: Record<string, Record<string, string[]>>;
  scripts: Record<string, string>;
};

/** Map a dist target like './dist/foo/index.js' to its source entry 'src/foo/index.ts'. */
function srcEntryFor(distTarget: string): string {
  return distTarget.replace(/^\.\/dist\//, 'src/').replace(/\.js$/, '.ts');
}

describe('exports map contract', () => {
  const subpaths = Object.entries(pkg.exports);

  it('declares at least the root and one subpath', () => {
    expect(subpaths.length).toBeGreaterThan(1);
    expect(pkg.exports['.']).toBeDefined();
  });

  it.each(subpaths)('%s: every condition target is emitted by the build', (subpath, conditions) => {
    const buildScript = pkg.scripts.build;
    for (const [condition, target] of Object.entries(conditions)) {
      // All runtime conditions must point into dist/.
      expect(target, `${subpath} ${condition}`).toMatch(/^\.\/dist\//);
      const entry = srcEntryFor(condition === 'types' ? target.replace(/\.d\.ts$/, '.js') : target);
      // The source entry must exist on disk...
      expect(existsSync(join(pkgDir, entry)), `${subpath} ${condition}: missing ${entry}`).toBe(true);
      // ...and be listed as a tsup build entry, or the dist target dangles.
      expect(buildScript, `${subpath} ${condition}: ${entry} not in build entries`).toContain(entry);
    }
  });

  it('root "." exposes the same condition set as the other entries', () => {
    expect(Object.keys(pkg.exports['.']).sort()).toEqual(['default', 'require', 'types']);
  });

  // moduleResolution node10 ignores `exports`; `typesVersions` is its only
  // route to subpath types, so every subpath needs a matching entry.
  it('typesVersions maps every subpath to its exports types target', () => {
    const expected = Object.fromEntries(
      subpaths
        .filter(([subpath]) => subpath !== '.')
        .map(([subpath, conditions]) => [subpath.replace(/^\.\//, ''), [conditions.types]]),
    );
    expect(pkg.typesVersions).toEqual({ '*': expected });
  });
});

describe('documented subpaths', () => {
  const docs = ['README.md', '../../docs/adopters/reference/ts-api-reference.md'];

  it.each(docs)('%s names only subpaths the exports map declares', (doc) => {
    const text = readFileSync(join(pkgDir, doc), 'utf8');
    const named = new Set(
      [...text.matchAll(/@hop-top\/kit\/([a-z][a-z0-9/-]*[a-z0-9])/g)]
        // `upgrade/drivers/{a,b}` is brace shorthand, not a subpath.
        .filter((m) => !text.startsWith('/{', m.index + m[0].length))
        .map((m) => `./${m[1]}`),
    );
    expect(named.size).toBeGreaterThan(0);
    for (const sub of named) {
      expect(pkg.exports, `${doc}: ${sub}`).toHaveProperty([sub]);
    }
  });

  // Adopter-facing modules (README, API reference, capability parity page).
  it.each(['uri', 'wizard', 'bus'])('%s is importable as a subpath', (name) => {
    expect(pkg.exports).toHaveProperty([`./${name}`]);
  });

  // Plumbing behind the cli factory: its public names are re-exported from `./cli`.
  it.each(['delegation', 'sideeffect', 'policy'])('%s stays internal (reached through ./cli)', (name) => {
    expect(pkg.exports).not.toHaveProperty([`./${name}`]);
  });
});

describe('root barrel', () => {
  it('re-exports every public subpath except sqlstore and the upgrade drivers as a namespace', () => {
    // Kebab-case subpaths become camelCase namespaces (completion-values -> completionValues).
    const expected = Object.keys(pkg.exports)
      // Driver subpaths (`upgrade/drivers/<name>`) are opt-in entries (a caller-provided
      // client, or a native or heavy module); they are nested, so they have no namespace.
      .filter((p) => p !== '.' && p !== './sqlstore' && !p.startsWith('./upgrade/drivers/'))
      .map((p) => p.replace(/^\.\//, '').replace(/-([a-z])/g, (_, c: string) => c.toUpperCase()));
    for (const name of expected) {
      expect(kit, name).toHaveProperty(name);
      expect(typeof (kit as Record<string, unknown>)[name], name).toBe('object');
    }
  });

  it('does not eagerly expose sqlstore (native deps stay behind the subpath)', () => {
    expect(kit).not.toHaveProperty('sqlstore');
  });

  it('does not expose the upgrade drivers (better-sqlite3 and tar stay behind their subpaths)', () => {
    expect(kit.upgrade).not.toHaveProperty('SqliteDriver');
    expect(kit.upgrade).not.toHaveProperty('FsDirDriver');
    expect(kit.upgrade).not.toHaveProperty('ConfigFileDriver');
  });
});
