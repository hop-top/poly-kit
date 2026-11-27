# upgrade

## What it answers

How a TypeScript tool checks for a newer release, orders migration versions leniently, registers versioned schema migrations and migrates one JSON document by its own version field. Same semantics as Go `core/upgrade`. The `Migrator` (backup, apply, rollback) is not ported yet. Wrong module for self-update (download, replace): Go only.

## Use it when

- announce a newer release: `createChecker({ name, currentVersion, owner, repo })`, then `notifyIfAvailable(process.stderr)`
- order or select migrations: `compareVersions('v1.9.0', '1.10.0')`, `new Registry().pending(schema, from, to)`
- migrate a JSON document: `migrateDocument(text, steps, { latest: 3 })`

## Quick start

```ts
import { migrateDocument, type DocStep } from '@hop-top/kit/upgrade';

const steps: DocStep[] = [
  { from: 1, to: 2, apply: (d) => ({ ...d, title: d.name }), note: 'add title' },
];
const { doc, report } = migrateDocument('{"version":1,"name":"x"}', steps);
```

## Contract

- Versions: a leading `v` and `+build` are dropped; missing or non-numeric parts are 0; a release sorts above its pre-releases. Not the strict `semver` package.
- Documents: the chain follows versions, not step order; newer, versionless or gapped documents fail with a `DocError` whose `kind` says why; the input is never modified; a current document comes back byte for byte.
- Parity: [`contracts/upgrade-v1`](../../../../contracts/upgrade-v1/README.md) `versions.json` and `document.json`, replayed by [`conformance.test.ts`](conformance.test.ts); `migrator.json` is listed there as pending.

## Neighbours

- Go reference: [`go/core/upgrade`](../../../../go/core/upgrade/README.md); Rust port: [`sdk/rs/src/upgrade`](../../../rs/src/upgrade/README.md)

## See also

- [`docs/adopters/reference/ts-api-reference.md`](../../../../docs/adopters/reference/ts-api-reference.md)
