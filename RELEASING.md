# Releasing

Releases run via [release-please](https://github.com/googleapis/release-please).
The manifest lives at `.github/.release-please-manifest.json`; the config at
`.github/release-please-config.json`.

## Flow

1. Conventional commits on `main` trigger release-please.
2. release-please opens a release PR per component with bumped versions
   and changelog entries.
3. Merging the release PR creates GitHub releases + tags.
4. Per-language publish jobs fire: goreleaser (Go), npm (TS), PyPI
   (Python).

## Components

| Component | Path | Type |
|-----------|------|------|
| kit | `.` | Go |
| sdk/ts | `sdk/ts` | Node |
| sdk/py | `sdk/py` | Python |
| qmochi | `incubator/qmochi` | Go |
| ash | `incubator/ash` | Go |
| aim | `incubator/aim` | Go |

`kit`, `sdk/ts`, and `sdk/py` share a linked version.

## Bump policy

Pre-1.0 (current):

- `feat:` / `fix:` / `perf:` → minor (`0.x → 0.x+1`).
- `feat!:` / `BREAKING CHANGE` → minor (downgraded from major via
  `bump-minor-pre-major`).

Post-1.0:

- `feat:` → minor.
- `fix:` / `perf:` → patch.
- `feat!:` / `BREAKING CHANGE` → major.

`bump-minor-pre-major: true` is retired at `1.0.0`.
