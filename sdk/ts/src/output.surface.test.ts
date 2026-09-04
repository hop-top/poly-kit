import { describe, it, expect } from 'vitest';
import * as output from './output';

/**
 * Guards the public symbol surface of the `./output` subpath export.
 *
 * The exports-map contract test in `index.test.ts` only asserts that each
 * declared subpath has a source file and a build entry — it says nothing
 * about which symbols a subpath exposes. `registerOutputFlags` and
 * `dispatch` were defined under `output/` but never re-exported here, so
 * the README's Commander wiring and the generated cli-ts template both
 * threw `TypeError: registerOutputFlags is not a function` against the
 * published package. `output/` is not itself a published subpath, so
 * consumers had no alternate import path.
 *
 * These names are load-bearing for documented adopter code. Removing one
 * is a breaking change and must fail here first.
 */
describe('output — documented symbol surface', () => {
  // README "Wiring into a Commander CLI" + templates/cli-ts/src/cli.ts.tmpl.
  it.each(['registerOutputFlags', 'dispatch'])(
    'exports %s as a function',
    (name) => {
      expect(typeof (output as Record<string, unknown>)[name]).toBe('function');
    },
  );

  it('exports the rest of the flag-wiring surface', () => {
    expect(typeof output.registryFor).toBe('function');
    expect(typeof output.resolveCols).toBe('function');
  });

  it('keeps the render shim and format constants', () => {
    expect(typeof output.render).toBe('function');
    expect(output.JSON_FORMAT).toBe('json');
    expect(output.YAML_FORMAT).toBe('yaml');
    expect(output.TABLE_FORMAT).toBe('table');
    expect(output.CSV_FORMAT).toBe('csv');
    expect(output.TEXT_FORMAT).toBe('text');
  });
});
