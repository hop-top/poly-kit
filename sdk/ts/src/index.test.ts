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

describe('root barrel', () => {
  it('re-exports every public subpath except sqlstore as a namespace', () => {
    const expected = Object.keys(pkg.exports)
      .filter((p) => p !== '.' && p !== './sqlstore')
      .map((p) => p.replace(/^\.\//, ''));
    for (const name of expected) {
      expect(kit, name).toHaveProperty(name);
      expect(typeof (kit as Record<string, unknown>)[name], name).toBe('object');
    }
  });

  it('does not eagerly expose sqlstore (native deps stay behind the subpath)', () => {
    expect(kit).not.toHaveProperty('sqlstore');
  });
});
