# Releasing

Releases run via [release-please](https://github.com/googleapis/release-please).
The manifest lives at `.github/.release-please-manifest.json`; the config at
`.github/release-please-config.json`.

## Where does my work land first? (read this first)

Pick the branch by **what** you are contributing, not by what is convenient:

| You are contributing… | Target branch | Why |
|-----------------------|---------------|-----|
| A new feature (`feat:`) | `next` | Minor/major work ships from `next` as `*-alpha`/`*-beta`/`*-rc`. |
| A breaking change (`feat!:` / `BREAKING CHANGE`) | `next` | Same — everything that moves the minor/major lands on `next` first. |
| A bug fix for the current stable release (`fix:` / `perf:`) | `main` | `main` is the current release line; stable ships from here. |
| A bug fix for an older supported release | its LTS worktree | See [LTS & backports](#lts--backports). |

**Golden rule:** feature work is `next`-first; bug fixes are `main`-first and
get **forward-ported to `next`** (and back-ported to LTS lines) where they
apply. Never land a new feature directly on `main`.

Open your PR against the branch above. If unsure whether something is a "fix"
or a "feature," treat behavior-changing or additive work as a feature → `next`.

## Branch model

Two long-lived integration branches, plus one worktree per supported release
line.

```
next  ──●──●──●──●───►   feature / breaking work; cuts *-alpha|beta|rc.*
         \  \  \
          \  \  └─ forward-port of a fix from main
main  ─────●──●──────►   current stable line; cuts stable x.y.z; fixes only
            \
             └─ hops/<version>  LTS worktrees; back-ported fixes only
```

- **`next`** — integration branch for the **next** minor/major. All `feat:`,
  `feat!:`, and `BREAKING CHANGE` work lands here. Prereleases
  (`*-alpha.N`, `*-beta.N`, `*-rc.N`) are cut and tagged from `next`.
- **`main`** — the **current** stable release line. Only `fix:` / `perf:`
  land here directly. Stable versions (`x.y.z`) are cut and tagged from
  `main`. Fixes landed on `main` are **forward-ported to `next`** so the
  next release keeps them.
- **`hops/<version>`** — per-release worktrees for **LTS back-ports** (see
  below). Fixes only, no features.

When `next` stabilizes and a new minor/major goes stable, `next` is promoted:
its content merges into `main`, `main` tags the stable release, and a fresh
`next` opens for the following cycle.

### Worktree layout

This repo is a bare checkout with per-branch worktrees under `hops/`:

| Worktree | Branch | Purpose |
|----------|--------|---------|
| `hops/main` | `main` | current stable line; bug fixes + stable tags |
| `hops/next` | `next` | next minor/major; feature work + prerelease tags |
| `hops/<version>` | `release/<version>` | LTS back-port line (e.g. `hops/1.2` → `release/1.2`) |

Create an LTS worktree with `git hop` (or `git worktree add`) off the tag that
opens the line — see [LTS & backports](#lts--backports).

## Flow

Prerelease flow (from `next`) and stable flow (from `main`) are the same
release-please machinery pointed at different branches.

1. Conventional commits on `next` (prereleases) or `main` (stable) trigger
   release-please **for that branch**.
2. release-please opens a release PR per component with bumped versions and
   changelog entries. On either branch a plain merge advances the prerelease
   counter (`*-alpha.N` → `*-alpha.N+1`; `beta`/`rc` via the promote gate).
   Stable `x.y.z` is cut deliberately with a `Release-As:` footer — see
   [Branch-aware release-please](#branch-aware-release-please).
3. Merging the release PR creates GitHub releases + tags.
4. `.github/workflows/publish.yml` fires on any `*/v*` tag (regardless of
   originating branch) and calls the org-wide reusable workflow
   [`hop-top/.github/.github/workflows/publish-on-tag.yml@v0`](https://github.com/hop-top/.github/blob/main/.github/workflows/publish-on-tag.yml),
   which parses `<component>/v<version>` from the tag, looks up the
   `ecosystems` entry in `publish.yml`, and dispatches to the per-language
   publish + mirror reusable workflows (`publish-ts.yml`, `publish-py.yml`,
   `publish-rs.yml`, `mirror-subtree.yml`).

### Branch-aware release-please

release-please is run against **both** `next` and `main` via the workflow's
`target-branch`, sharing one committed config.

`.github/release-please-config.json` is byte-identical on both branches — every
package carries `prerelease: true`, `versioning: prerelease`,
`prerelease-type: alpha.0`. Nothing in the workflow patches it per branch.
Keeping one config on both branches is what makes `next → main` promotion
merges conflict-free, and it means a plain merge on **either** branch produces
the next prerelease counter — `main` and `next` propose the same versions until
a stable cut lands.

**Cutting stable.** A version already on a prerelease track only leaves it via
an explicit footer. Land a conventional commit on `main` carrying
`Release-As: x.y.z` (no suffix) — a squash-merged `chore(release): cut x.y.z`
PR is the usual vehicle — and release-please's next run on `main` proposes
`x.y.z` instead of `-alpha.N+1`. Do **not** flip `prerelease: false` in the
committed config: that diverges the file across branches and turns every later
promotion merge into a config conflict.

Two `Release-As:` facts to check before merging that commit:

- In manifest mode the footer is **global across components**: it applies to
  every package release-please would otherwise consider — `incubator/qmochi`
  included, which does not share the kit family's version line.
- Only the first `Release-As:` line in a commit body is honoured.

Dry-run first and read the proposed titles:

```sh
npx release-please@latest release-pr --dry-run \
  --token "$(gh auth token)" --repo-url hop-top/poly-kit \
  --config-file .github/release-please-config.json \
  --manifest-file .github/.release-please-manifest.json \
  --target-branch main | grep '^title:'
```

**Release brake.** Release PRs on either branch are blocked until a member of
`@hop-top/release` approves them. The `production-branch-guardrail` ruleset on
`main` and `next` requires code-owner review with zero required approvals, and
`.github/CODEOWNERS` covers exactly the files every release PR rewrites: the
manifest and each managed `CHANGELOG.md`. Everything else merges review-free.
The nightly auto-cut (`.github/workflows/nightly-release.yml`) merges with
`--auto`, so it only times a merge that is already approved; it cannot cut a
release nobody approved, on either branch. Approve the PR you intend to ship
and leave the sibling branch's PR alone. Approvals are dismissed on push and
release-please rebuilds the PR on every push to its base, so approve after the
last change you want in.

Channel transitions on `next` (`alpha → beta → rc`) are driven by the
`prerelease-type` in config plus `Release-As:` trailers, gated by
`.github/workflows/release-promote-gate.yml`. That gate permits only
`release → alpha → beta → rc → release`; it rejects skipped and backwards
transitions, and requires that a promotion PR change nothing but
`prerelease-type`, with all packages sharing one value. Promoting `next` to
stable is the `next → main` merge described in [Branch model](#branch-model).

## Components

| Component | Path | Type | Prerelease channel (`next`) |
|-----------|------|------|------------------------------|
| kit | `.` | Go | alpha → beta → rc |
| kit-ts | `sdk/ts` | Node | alpha → beta → rc |
| kit-py | `sdk/py` | Python | alpha → beta → rc |
| kit-rs | `sdk/experimental/rs` | Rust | experimental |
| kit-php | `sdk/experimental/php` | PHP | experimental |
| qmochi | `incubator/qmochi` | Go | alpha → beta → rc |

`kit`, `kit-ts`, `kit-py`, `kit-rs`, and `kit-php` share a linked version.

## Bump policy

Bump size is the same on both branches; only the prerelease suffix differs
(present on `next`, absent on `main`).

Pre-1.0 (current):

- `feat:` / `fix:` / `perf:` → minor (`0.x → 0.x+1`).
- `feat!:` / `BREAKING CHANGE` → minor (downgraded from major via
  `bump-minor-pre-major`).

Post-1.0:

- `feat:` → minor.
- `fix:` / `perf:` → patch.
- `feat!:` / `BREAKING CHANGE` → major.

`bump-minor-pre-major: true` is retired at `1.0.0`.

## LTS & backports

Each **major** release, and the **latest minor/patch** of the current major,
gets its own long-lived worktree so security and bug fixes can be back-ported
according to the published LTS window.

- Branch name: `release/<major>.<minor>` (e.g. `release/1.2`).
- Worktree path: `hops/<major>.<minor>` (e.g. `hops/1.2`).
- Cut the line from the tag that opens it:

  ```sh
  # once 1.2.0 stable is tagged on main
  git worktree add ../1.2 -b release/1.2 kit/v1.2.0
  ```

- **Only `fix:` / `perf:` land on LTS branches.** No features, no breaking
  changes.
- A fix that applies to multiple lines lands on the **oldest** supported line
  it affects, then is forward-ported up through the newer LTS lines → `main`
  → `next`. This keeps every active line and both integration branches
  consistent.
- release-please runs per LTS branch (via `target-branch`) and tags stable
  patch releases (`x.y.z`) from that line.

The LTS window (how many majors/minors stay supported, and for how long) is
published separately; this file only describes the branch/worktree mechanics.
