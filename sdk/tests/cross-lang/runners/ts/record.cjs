#!/usr/bin/env node
/**
 * TypeScript/Node runner for the cross-language telemetry contract harness.
 *
 * Imports the *built* CJS bundle at ``../../../ts/dist/telemetry/index.js``
 * (no TS toolchain at runtime — the harness assumes ``npm run build`` has
 * already produced ``dist/``).
 *
 * Reads ``fixtures/input.json``, instantiates a ``Client`` with the jsonl
 * sink, calls ``record()``, then awaits ``shutdown()``.
 *
 * Pre-conditions are identical to py/record.py — orchestrator-owned.
 */
'use strict';

const fs = require('node:fs');
const path = require('node:path');

const here = __dirname;
const crossLang = path.resolve(here, '..', '..');
const distEntry = path.resolve(
  crossLang,
  '..',
  '..',
  'ts',
  'dist',
  'telemetry',
  'index.js',
);

if (!fs.existsSync(distEntry)) {
  console.error(
    `ts runner: expected built bundle at ${distEntry}. ` +
      `Run "npm run build" inside hops/main/sdk/ts/ first.`,
  );
  process.exit(2);
}

// eslint-disable-next-line @typescript-eslint/no-var-requires
const { Client } = require(distEntry);

const fixtures = path.join(crossLang, 'fixtures');
const inputPath =
  process.env.KIT_CROSS_LANG_INPUT && process.env.KIT_CROSS_LANG_INPUT.length > 0
    ? process.env.KIT_CROSS_LANG_INPUT
    : path.join(fixtures, 'input.json');
const payload = JSON.parse(fs.readFileSync(inputPath, 'utf8'));

async function main() {
  const client = new Client({ sdkVersion: 'cross-lang-test' });
  client.record(payload.event, payload.attrs);
  await client.shutdown(5000);
}

main().then(
  () => process.exit(0),
  (err) => {
    console.error('ts runner error:', err);
    process.exit(1);
  },
);
