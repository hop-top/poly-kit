import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

const src = join(dirname(fileURLToPath(import.meta.url)), '../..');

// The drivers import `better-sqlite3` and `tar`. They are reachable only
// through their own subpath exports, so importing the main entry or
// `@hop-top/kit/upgrade` never loads either module.
describe('driver isolation', () => {
  it.each(['index.ts', 'upgrade/index.ts', 'upgrade/migrator.ts'])('%s does not import the drivers', (file) => {
    const text = readFileSync(join(src, file), 'utf8');
    expect(text).not.toMatch(/^\s*(import|export)\b[^;]*\bfrom\s+['"][^'"]*\/drivers\//m);
    expect(text).not.toMatch(/\bfrom\s+['"](better-sqlite3|tar)['"]/);
  });
});
