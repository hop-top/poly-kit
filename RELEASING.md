# Releasing

Releases run via [release-please](https://github.com/googleapis/release-please).
The manifest lives at `.github/.release-please-manifest.json`; the config at
`.github/release-please-config.json`.

## Flow

1. Conventional commits on `main` trigger release-please.
2. release-please opens a release PR per component with bumped versions
   and changelog entries.
3. Merging the release PR creates GitHub releases + tags.
4. `.github/workflows/publish.yml` fires on the tag and calls the
   org-wide reusable workflow
   [`hop-top/.github/.github/workflows/publish-on-tag.yml@v0`](https://github.com/hop-top/.github/blob/main/.github/workflows/publish-on-tag.yml),
   which parses `<component>/v<version>` from the tag, looks up the
   `ecosystems` entry in `publish.yml`, and dispatches to the
   per-language publish + mirror reusable workflows
   (`publish-ts.yml`, `publish-py.yml`, `publish-rs.yml`,
   `mirror-subtree.yml`).

## Components

| Component | Path | Type | Prerelease |
|-----------|------|------|------------|
| kit | `.` | Go | alpha |
| kit-ts | `sdk/ts` | Node | alpha |
| kit-py | `sdk/py` | Python | alpha |
| kit-rs | `sdk/experimental/rs` | Rust | experimental |
| kit-php | `sdk/experimental/php` | PHP | experimental |
| qmochi | `incubator/qmochi` | Go | alpha |
| ash | `incubator/ash` | Go | alpha |

`kit`, `kit-ts`, `kit-py`, `kit-rs`, and `kit-php` share a linked version.

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
