# Releasing

Releases run via [release-please](https://github.com/googleapis/release-please)
with hop-top/.github's **target-versioning** extension: the branch decides the
version. A prerelease line on `next` previews one target stable version,
`main` cuts exactly that version from its rc and otherwise only patches what is
already stable, and `release/x.y` lines take patches only. Nothing ships that
no prerelease previewed. Nobody picks a version and nobody writes `Release-As`.

The config lives at `.github/release-please-config.json`, the manifest at
`.github/.release-please-manifest.json`. The extension's own reference is
`references/how-to/target-versioning.md` in hop-top/.github (added by
hop-top/.github#87).

## Where does my work land first? (read this first)

Pick the branch by **what** you are contributing, not by what is convenient:

| You are contributing… | Target branch | Why |
|-----------------------|---------------|-----|
| A new feature (`feat:`) | `next` | Every new line is previewed as `*-alpha`/`*-beta`/`*-rc` on `next` first. |
| A breaking change (`feat!:` / `BREAKING CHANGE`) | `next` | Same — and on `main` it blocks the package's release. |
| A bug fix for the current stable release (`fix:` / `perf:`) | `main` | `main` takes patches for what is already stable. |
| A bug fix for an older supported release | its LTS worktree | See [LTS & backports](#lts--backports). |

**Golden rule:** feature work is `next`-first; bug fixes are `main`-first and
get **forward-ported to `next`** (and back-ported to LTS lines) where they
apply. A `feat` or breaking change landed on `main` for a package already
stable there blocks that package's release until it is reverted (see
[`main`](#main-stable)).

Open your PR against the branch above. If unsure whether something is a "fix"
or a "feature," treat behavior-changing or additive work as a feature → `next`.

## Branch model

Two long-lived integration branches, plus one worktree per supported release
line.

```
next  ──●──●──●──●───►   feature / breaking work; cuts *-alpha|beta|rc.*
         \  \  \
          \  \  └─ forward-port of a fix from main
main  ─────●──●──────►   stable: cuts x.y.z from an rc, then patches only
            \
             └─ hops/<version>  LTS worktrees (release/x.y); patches only
```

- **`next`** — the prerelease branch. All `feat:`, `feat!:`, and
  `BREAKING CHANGE` work lands here. Prereleases (`*-alpha.N`, `*-beta.N`,
  `*-rc.N`) are cut and tagged from `next`; channel promotions and opened
  lines are recorded here.
- **`main`** — the stable branch. A package whose line reached rc on `next`
  is cut stable when `next` merges into `main`; a package already stable on
  `main` takes `fix:` / `perf:` patches only. Fixes landed on `main` are
  **forward-ported to `next`**.
- **`release/<major>.<minor>`** (worktree `hops/<version>`) — LTS lines;
  patch releases only (see below).

When packages on `next` reach rc, `next` is merged into `main`: `main` cuts
those rc packages stable and leaves the others for a later merge.

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

1. Every push to `next`, `main` or a `release/<major>.<minor>` branch runs
   `.github/workflows/release-please.yml`, a caller of hop-top/.github's
   `release-please-on-push.yml` with `versioning-extension: true` (the
   googleapis action does not know `versioning: "target"`). One committed
   config serves every branch; the extension reads the branch's role from the
   `target-context` plugin (`main` stable, `next` prerelease, `release/x.y`
   maintenance).
2. release-please keeps one release PR per component current, with the
   version the rules below give and its changelog entry (one PR for the whole
   kit family when it links). A package the rules cannot version is
   **blocked**: left out, and the run fails with the reason after every other
   package was processed. A package the branch does not release (an alpha or
   beta on `main`) is **skipped** with a notice; the run stays green.
3. On every release PR, `.github/workflows/changelog-rewrite.yml` post-processes
   the bot's branch: it rewrites changelog prose, then regenerates `uv.lock` and
   `composer.lock` when release-please bumped `pyproject.toml` or
   `composer.json` (the lock checks would otherwise fail on the release PR and
   on every PR cut after it merges). Both commits are pushed with the
   release-bot app token so CI runs on the new head; runs started by a
   `GITHUB_TOKEN` push wait for maintainer approval instead. `Cargo.lock` needs
   nothing: release-please rewrites it itself.
4. Merging the release PR creates GitHub releases + tags. A release is a
   GitHub prerelease exactly when its version has a prerelease suffix.
5. `.github/workflows/publish.yml` fires on any `*/v*` tag (regardless of
   originating branch) and calls the org-wide reusable workflow
   [`hop-top/.github/.github/workflows/publish-on-tag.yml@v0`](https://github.com/hop-top/.github/blob/main/.github/workflows/publish-on-tag.yml),
   which parses `<component>/v<version>` from the tag, looks up the
   `ecosystems` entry in `publish.yml`, and dispatches to the per-language
   publish + mirror reusable workflows (`publish-ts.yml`, `publish-py.yml`,
   `publish-rs.yml`, `mirror-subtree.yml`).
6. On a `kit/v*` tag the same file's `goreleaser` job runs
   [`goreleaser-on-tag.yml@v0`](https://github.com/hop-top/.github/blob/main/.github/workflows/goreleaser-on-tag.yml)
   with the root `.goreleaser.yaml`: it builds `./cmd/kit` for
   linux/darwin/windows × amd64/arm64 and uploads
   `kit_<os>_<arch>.tar.gz` (`.zip` on windows) and `checksums.txt` to the
   `kit/v<version>` release. The engine SDKs download from there, see
   [The engine SDKs' kit pin](#the-engine-sdks-kit-pin).

`release-please.yml` and `release-please-preflight.yml` pin hop-top/.github
to the commit of hop-top/.github#87, which adds the extension; the pin moves
to the merged commit once #87 lands (`tooling-ref` too).
`scripts/promote-release.sh` and the promotion gate read the pin from
`release-please.yml`.

## Target versioning

Every package uses `versioning: "target"` and `prerelease-type: "alpha.0"`;
the `plugins` list starts with `target-context`, then the
`conditional-linked-versions` group `kit`. No `release-as` anywhere: the
preflight and the run refuse it.

The **target** of `x.y.z-alpha.N` is `x.y.z`. The **required target** is the
package's last stable release (from its tags) bumped by the commits since its
latest release:

| Change | Before 1.0 | From 1.0 |
|---|---|---|
| breaking (`feat!`, `BREAKING CHANGE:`) | `x.(y+1).0` | `(x+1).0.0` |
| `feat` | `x.(y+1).0` | `x.(y+1).0` |
| anything else (`fix`, `perf`, …) | `x.y.(z+1)` | `x.y.(z+1)` |

The table is fixed: every package keeps `bump-minor-pre-major: true` and no
`bump-patch-for-minor-pre-major`; nothing to retire at `1.0.0`.

### `next` (prerelease)

| Situation | Proposal | Example |
|---|---|---|
| The current target covers the required one | counter + 1 | `0.5.0-alpha.26` + `feat!` → `0.5.0-alpha.27` |
| The required target is higher | new line at `alpha.0`, whatever channel the old line was on | `0.5.1-alpha.3` + `feat!` → `0.6.0-alpha.0` |
| The current target was released stable (a cut, or a hotfix on `main`) | new line from that stable | `0.6.0-rc.2`, `0.6.0` tagged, + `feat` → `0.7.0-alpha.0` |
| The line is promoted (`x-channel` for this line) | `<channel>.0` on the same target, cut at once | `0.5.0-alpha.26`, beta on 0.5.0 → `0.5.0-beta.0` |
| A line is opened (`x-open-line`) | `<line>-alpha.0`, cut at once, unless a change requires more (then blocked) | `0.9.0-alpha.3`, open 1.0.0 → `1.0.0-alpha.0` |

### Channels: promote per package or group

Every line starts and stays on alpha; the engine never changes a channel.
Moving a package to beta, then rc, is a decision recorded in the config, per
package or per group:

```json
"x-channel": {"line": "0.5.0", "channel": "beta"}
```

- **One step at a time**, `alpha → beta → rc`, from the channel the manifest
  shows, never backwards. A promotion recorded but not released yet does not
  count: rc waits until `x.y.z-beta.0` is merged.
- **Cut at once**: the run on `next` that first sees the record proposes
  `x.y.z-<channel>.0` with no other commit; later releases count on that
  channel.
- **Expires with its line**: after a line move or a stable cut the package
  starts on alpha again, and the release PR on `next` that leaves the line
  removes the record. A record for a line the package is not on is refused.
- **Inert on `main` and `release/x.y`**: the records travel with the
  `next → main` merge but are neither used nor edited there.

Record a step with `scripts/promote-release.sh` (behind `make promote*`), a
wrapper around hop-top/.github's `bin/promote.js` at the pinned commit (the
code the release run uses; fetched into `~/.cache/poly-kit/` and installed on
first use, needs git, node ≥ 20 and npm). It writes the record and commits
the config alone on the current branch, as `chore(release): promote kit and
qmochi to beta` (`open 1.0.0 for group kit`, `clear channel for kit`), with
promote.js's report as the body; get that commit onto `next`. It refuses to
run with staged changes, or with uncommitted changes in the config:

```sh
make promote-beta PACKAGE='kit qmochi'        # paths or component names
make promote-rc GROUP=kit                     # every member of the kit family
make promote-beta PACKAGE=kit DRY_RUN=1       # print; no write, no commit
make promote-beta PACKAGE=kit NO_COMMIT=1     # write, commit it yourself
make promote ARGS='clear channel --package kit'
```

All or nothing: one refused package and nothing is written or committed. A
group member already on (or already promoted to) the channel is left as it
is; nothing changed, nothing committed.

`.github/workflows/release-promote-gate.yml` checks every PR to `next` that
changes the records: it changes nothing else in the config, and each added or
changed record is exactly what `promote.js` writes from the base config
(current target as the line, one step, never backwards, an open line not yet
reached). Removing a record always passes.

### Opening a line (1.0)

A line no change would open on its own — `1.0.0` from 0.x — is the same kind
of step:

```sh
make promote-open-line LINE=1.0.0 GROUP=kit
```

It records `"x-open-line": "1.0.0"`; the next run on `next` opens
`1.0.0-alpha.0` at once if that is at or above what the commits require, and
blocks the package otherwise. The release PR that reaches the line removes the
record.

### `main` (stable)

| Situation | Proposal |
|---|---|
| Manifest at `x.y.z-rc.N` (`next` merged in) | `x.y.z`, even with no commit since the merge |
| … a commit since requires more than `x.y.z` | **blocked**: stable must equal what the rc previewed; land it on `next` |
| … `x.y.z` already released (a hotfix on `main`, then `next` merged) | **blocked**: re-line on `next` (a change, or `open-line x.y.(z+1)`), promote via beta to rc, merge again |
| Manifest stable, only fixes since | patch: `0.6.0` + `fix` → `0.6.1` |
| Manifest stable, a `feat` or breaking change since | **blocked**: it would ship unpreviewed; revert it on `main`, land it on `next` |
| Manifest at an alpha or beta | **skipped** with a notice |

Only rc packages are cut: merging `next` into `main` while some packages are
still on alpha or beta cuts the rc ones and leaves the others for a later
merge. A hotfix on `main` that releases the version `next` previews moves
`next` on: its next release opens the following line.

### `release/x.y` (LTS)

Stable patches only: `fix` → `x.y.(z+1)`. A `feat` or breaking change since
the package's last release blocks it (every other package still gets its PR,
the run fails at the end). A prerelease manifest there is skipped.

### Linked family

`kit`, `kit-php`, `kit-py`, `kit-rs` and `kit-ts` form the
`conditional-linked-versions` group `kit`. They version independently except
when one member's proposal opens a **new major** (always), or a **new minor
line while the family is before 1.0** (on `next`). Then:

- one release PR for the whole group, members without commits included;
- every member moves to the group's target, the highest among them;
- each member keeps its channel: one already on the target line keeps it (or
  takes its promotion for that line), any other starts the line on alpha;
  members on the same channel share one counter;
- on `main` a linked major holds the whole family, the experimental kit-php
  and kit-rs included: every member must be at an rc of the target. kit
  `1.0.0` is not cut until kit-php, kit-py, kit-rs and kit-ts are all at
  `1.0.0-rc.N`; one member still on alpha or beta blocks the group. **An
  experimental SDK must reach rc (promote it via beta to rc on `next`) before
  the family's 1.0 ships.**
- pre-1.0 minors stay unlinked on `main`: each member is cut from its own rc,
  and a member not at rc is skipped until a later merge.

### `Release-As` footers

Ignored: the run drops them from every target package's commits and logs
`ignoring "Release-As: …"`. Old ones in history need no rewrite; do not write
new ones.

### Example: kit and qmochi to beta

With `next` at kit `0.5.0-alpha.26` and qmochi `0.2.0-alpha.1`:

```sh
make promote-beta PACKAGE='kit qmochi'
# committed: chore(release): promote kit and qmochi to beta
```

Merged into `next`, the run proposes `kit 0.5.0-beta.0` and
`qmochi 0.2.0-beta.0` at once. kit-py, kit-ts, kit-php, kit-rs and mcp-tasks
stay on alpha: kit is promoted by package, and a promotion on the same line
opens no new line, so the family does not link.

### Dry run

Before a config change lands, dry-run the extension (reads only; the config
is read from the target branch, so run it against a branch that carries the
change), from a checkout of hop-top/.github at the pinned commit:

```sh
cd scripts/release-please-target && npm ci
TOKEN="$(gh auth token)" GITHUB_REPOSITORY=hop-top/poly-kit \
TARGET_BRANCH=next \
CONFIG_FILE=.github/release-please-config.json \
MANIFEST_FILE=.github/.release-please-manifest.json \
DRY_RUN=true node bin/run.js
```

### Release brake

Release PRs on either branch are blocked until a member of `@hop-top/release`
approves them. The `production-branch-guardrail` ruleset on `main` and `next`
requires code-owner review with zero required approvals, and
`.github/CODEOWNERS` covers exactly the managed `CHANGELOG.md` files, which
release-please rewrites on every release PR. The config and manifest are
deliberately not owned: promotion records are human edits to the config and
merge review-free (the promotion gate validates them), like everything else.
The nightly auto-cut (`.github/workflows/nightly-release.yml`) merges with
`--auto`, so it only times a merge that is already approved; it cannot cut a
release nobody approved, on either branch. Approve the PR you intend to ship
and leave the sibling branch's PR alone. Approvals are dismissed on push and
release-please rebuilds the PR on every push to its base, so approve after the
last change you want in.

## Components

| Component | Path | Type | Status |
|-----------|------|------|--------|
| kit | `.` | Go | linked family |
| kit-php | `sdk/experimental/php` | PHP | linked family, experimental |
| kit-py | `sdk/py` | Python | linked family |
| kit-rs | `sdk/experimental/rs` | Rust | linked family, experimental |
| kit-ts | `sdk/ts` | Node | linked family |
| mcp-tasks | `extensions/mcp-tasks` | Go | independent |
| qmochi | `incubator/qmochi` | Go | independent |

Each starts every line on alpha and is promoted on its own (or with its
group); see [Linked family](#linked-family) for what links the five kit
packages.

### The cli-go template's kit pin

A project `kit init --from cli-go` generates requires the kit release
pinned in `templates/cli-go/go.mod.tmpl`, and `kit_version` in
`templates/cli-go/kit-template.yaml` names the same release. Both lines,
and their copies under `internal/template/builtins/cli-go/`, carry the
`x-release-please-version` annotation and are generic `extra-files` of
the `kit` package, so each `kit` release PR sets them to the version it
cuts. In `go.mod.tmpl` the annotation is a template comment
(`{{- /* x-release-please-version */}}`), so it never reaches a
generated project.

`make check-template-kit-pin` (the `template-kit-pin` CI job, also
nightly) fails when more than one version on
`proxy.golang.org/hop.top/kit/@v/list` is newer than the pin. It reads
the proxy, not local tags: the proxy is what a generated project's
`go get` resolves against. `go test ./cmd/kit/init/` holds the pin equal
to the manifest's `kit` version.

### The engine SDKs' kit pin

The engine SDKs run `kit serve` and fetch a `kit` binary when no
compatible one (same major.minor) is on PATH: `find_kit_binary()` in
`engine/sdk/py-kit-engine/kit_engine/_binary.py`, and the npm
postinstall `engine/sdk/ts-kit-engine/scripts/install.js`. Both download
`https://github.com/hop-top/poly-kit/releases/download/kit/v<version>/kit_<os>_<arch>.<tar.gz|zip>`
and refuse to install it unless its SHA-256 matches that release's
`checksums.txt`. Keep those names stable in `.goreleaser.yaml`.

`<version>` is `VERSION` in `_binary.py` (marked
`x-release-please-version`, a generic extra-file) and `kit.version` in
the ts package's `package.json` (a json extra-file at `$.kit.version`),
both on the root package, so each `kit` release PR sets them to the
version it cuts. Each SDK's tests hold its pin equal to the manifest's
`kit` version and to that wiring.

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
- release-please runs on every push to `release/<major>.<minor>` and tags stable patch
  releases (`x.y.z`) from that line; a `feat` or breaking change there blocks
  the package (see [`release/x.y`](#releasexy-lts)).

The LTS window (how many majors/minors stay supported, and for how long) is
published separately; this file only describes the branch/worktree mechanics.
