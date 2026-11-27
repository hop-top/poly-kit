// Shared by the conformance runners: where the corpus lives and the strict
// shape check that makes an unknown field fail the run.

import { existsSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export function corpusDir(): string {
  let dir = dirname(fileURLToPath(import.meta.url));
  for (let i = 0; i < 10; i++) {
    const cand = join(dir, 'contracts', 'upgrade-v1');
    if (existsSync(join(cand, 'versions.json'))) return cand;
    dir = resolve(dir, '..');
  }
  throw new Error('contracts/upgrade-v1 not found above ' + dirname(fileURLToPath(import.meta.url)));
}

export const DIR = corpusDir();

// ─── strict loading ───────────────────────────────────────────────────

export interface Shape {
  [key: string]: Shape | true | readonly [Shape | true];
}

export function strict(value: unknown, shape: Shape, where: string): void {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${where}: not an object`);
  }
  for (const [key, val] of Object.entries(value as Record<string, unknown>)) {
    const spec = shape[key];
    if (spec === undefined) throw new Error(`${where}: unknown field "${key}"`);
    if (spec === true) continue;
    if (Array.isArray(spec)) {
      if (!Array.isArray(val)) throw new Error(`${where}.${key}: not an array`);
      const item = spec[0] as Shape | true;
      val.forEach((v, i) => {
        if (item !== true) strict(v, item, `${where}.${key}[${i}]`);
      });
    } else {
      strict(val, spec as Shape, `${where}.${key}`);
    }
  }
}
