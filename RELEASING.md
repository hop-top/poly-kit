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
   counter (`*-alpha.N` → `*-alpha.N+1`; `beta`/`rc` via the promote gate),
   unless a breaking change landed since the component's last release: that
   starts a new minor line, see
   [Breaking changes start a new minor line](#breaking-changes-start-a-new-minor-line),
   and a new minor line of a kit group member moves the whole group, see
   [Linked releases](#linked-releases). Stable `x.y.z` is cut deliberately with
   a `release-as` pin — see
   [Branch-aware release-please](#branch-aware-release-please).
3. On every release PR, `.github/workflows/changelog-rewrite.yml` post-processes
   the bot's branch: it rewrites changelog prose, then regenerates `uv.lock` and
   `composer.lock` when release-please bumped `pyproject.toml` or
   `composer.json` (the lock checks would otherwise fail on the release PR and
   on every PR cut after it merges), then drops the `release-as` pins the
   release cuts. The commits are pushed with the
   release-bot app token so CI runs on the new head; runs started by a
   `GITHUB_TOKEN` push wait for maintainer approval instead. `Cargo.lock` needs
   nothing: release-please rewrites it itself.
4. Merging the release PR creates GitHub releases + tags.
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

### Branch-aware release-please

release-please is run against **both** `next` and `main` via the workflow's
`target-branch`, sharing one committed config.

`.github/release-please-config.json` is byte-identical on both branches — every
package carries `prerelease: true`, `versioning: prerelease`,
`prerelease-type: alpha.0`. Nothing in the workflow patches it per branch.
Keeping one config on both branches is what makes `next → main` promotion
merges conflict-free, and it means a plain merge on **either** branch produces
the next prerelease counter — `main` and `next` propose the same versions until
a stable cut lands. A pending `release-as` pin is the one exception, see
[Pins and the main/next window](#pins-and-the-mainnext-window).

**Cutting stable.** A version already on a prerelease track only leaves it by
an explicit pin. On `main`, pin each component you cut to `x.y.z` (no suffix)
with `scripts/release-as.sh <component> x.y.z`, or the whole kit group with
`scripts/release-as.sh --group kit x.y.z` (members without commits since their
`rc` included), and release-please's next run on `main` proposes `x.y.z`
instead of `-rc.N+1`. Do **not** flip `prerelease: false` in the committed
config: that diverges the file across branches and turns every later promotion
merge into a config conflict.

**Pins and footers.** release-please takes a forced version from two places:

- A pin: `packages["<path>"]["release-as"]` in the config, for that package
  and no other. `scripts/release-as.sh` writes these. Never set the top-level
  `release-as`: it pins every package.
- A `Release-As:` footer: for every package that sees the commit, which depends
  only on the files it touches. The root package `kit` sees every commit except
  one whose files all sit under its `exclude-paths`; any other package sees a
  commit that touches a file under its path. An empty commit touches no file,
  so it reaches every package without `exclude-paths` and misses `kit`. Only
  the first `Release-As:` line in a commit body is honoured, and a pin wins
  over a footer.

Neither opens a release PR by itself: release-please skips a package whose
commits since its release are none, or only hidden types (`chore`, `ci`,
`build`, …), before it reads either. So when `scripts/release-as.sh` pins the
root package, its commit, which touches only the config and so reaches the
root package alone, also carries a `Release-As:` footer with the pinned
version: a footer makes even a `chore` commit count. For the kit group, the
plugin then adds the members without commits (see
[Linked releases](#linked-releases)). A pin on any other package without
commits waits for that package's next commit of a visible type; the script
says so.

Dry-run first and read the proposed titles. The dry-run reads the target
branch from GitHub, so it sees a pin only once it is pushed: point
`--target-branch` at the pushed PR branch, not at its base. 17 is the
release-please major `mise.toml` pins; `googleapis/release-please-action@v5`
bundles 17.6.0.

```sh
npx release-please@17 release-pr --dry-run \
  --token "$(gh auth token)" --repo-url hop-top/poly-kit \
  --config-file .github/release-please-config.json \
  --manifest-file .github/.release-please-manifest.json \
  --target-branch <pushed-branch> | grep '^title:'
```

**Release brake.** Release PRs on either branch are blocked until a member of
`@hop-top/release` approves them. The `production-branch-guardrail` ruleset on
`main` and `next` requires code-owner review with zero required approvals, and
`.github/CODEOWNERS` covers exactly the managed `CHANGELOG.md` files, which
release-please rewrites on every release PR. The config and manifest are
deliberately not owned: promotions and counter reseeds are human edits to
those files and merge review-free, like everything else. The ruleset also
requires the `breaking-bump` check (see
[Breaking changes start a new minor line](#breaking-changes-start-a-new-minor-line)),
which passes, skipped, on every PR that is not a release PR.
The nightly auto-cut (`.github/workflows/nightly-release.yml`) merges with
`--auto`, so it only times a merge that is already approved; it cannot cut a
release nobody approved, on either branch. Approve the PR you intend to ship
and leave the sibling branch's PR alone. Approvals are dismissed on push and
release-please rebuilds the PR on every push to its base, so approve after the
last change you want in.

Channel transitions on `next` (`alpha → beta → rc`) are committed by
`scripts/promote-release.sh <stage>` (`make promote-<stage>`), which rewrites
`prerelease-type` on every package to `<stage>.0` — or removes it for
`release` — and commits `chore(release): promote to <stage>`, changing no
other line. Open that commit as a PR:
`.github/workflows/release-promote-gate.yml` permits only
`release → alpha → beta → rc → release`, rejects skipped and backwards
transitions, and requires that the PR change nothing but `prerelease-type`,
with all packages sharing one value.

release-please reads `prerelease-type` only when the version it bumps has no
prerelease suffix, i.e. at the first bump after a stable version. While the
manifest carries a suffix, every merged release PR bumps that counter whatever
the config says: `0.5.0-alpha.3` becomes `0.5.0-alpha.4` under
`prerelease-type: beta.0`. It does so whatever the commits say too, breaking
changes included; those take a pin, see
[Breaking changes start a new minor line](#breaking-changes-start-a-new-minor-line).
So each stage means:

- `alpha` seeds the next line. Once the stable cut is merged back into
  `next`, the first `feat:` proposes `x.y+1.0-alpha.0` (a `fix:` alone,
  `x.y.z+1-alpha.0`).
- `beta` and `rc` declare the channel for the gate. To move
  `0.5.0-alpha.N` to `0.5.0-beta.0`, pin each component on `next`
  (`scripts/release-as.sh kit 0.5.0-beta.0`), or the kit group at once with
  `--group` — dry-run first, as for a stable cut — after which merges count
  `beta.1`, `beta.2`, …
- `release` cuts nothing. It only resets the gate so the ladder can start
  again at `alpha`. With the manifest at `0.5.0-rc.N` a merge on `next`
  still proposes `rc.N+1`; with the manifest already stable and no
  `prerelease-type`, a merge on either branch would propose a **stable**
  version. Run `release` then `alpha` back to back right after the
  `next → main` promotion merge, while the manifest still carries `rc`, and
  land the same two commits on `main` so the config stays identical.

Promoting `next` to stable is the `next → main` merge described in
[Branch model](#branch-model); stable `x.y.z` is cut there with a pin, never
by the promote script.

### Breaking changes start a new minor line

Any breaking change since a component's last release starts a new minor
prerelease line for that component: `x.(y+1).0-alpha.0` while the major is 0,
`(x+1).0.0-alpha.0` from 1.0. `qmochi` at `0.2.0-alpha.1` plus a `feat!:`
becomes `0.3.0-alpha.0`; `feat:`, `fix:` and `perf:` alone keep counting
(`0.2.0-alpha.2`). For a linked group's member the new line is a linked release
too, see [Linked releases](#linked-releases).

A commit is breaking when release-please's parser says so: a `!` before the
colon of a conventional header (`feat!:x` included), a body line that starts
with `BREAKING CHANGE:` and text, a `BREAKING CHANGE:` footer with text,
`BREAKING-CHANGE:` and text anywhere in the body, or a trailing footer whose
token ends in `!` (`wip!: x`, `Refs!: 12`), which release-please parses as a
breaking commit of its own. A paragraph that opens with a lowercase type, a
colon and a space counts as its own commit. `scripts/release-lib.sh` has the
full rules, corner cases included. A commit counts for a component when
release-please routes it there: its files, the component's path and
`exclude-paths`. Dependency bumps marked `!` (`build(deps)!:`) count.

release-please does not do this by itself: while the manifest carries a
prerelease suffix it bumps the counter whatever the commits say. Three pieces
close the gap.

**The check.** `.github/workflows/release-bump-check.yml` runs
`scripts/check-release-bump.sh` on every release PR
(`release-please--branches--*`) as the `breaking-bump` job. It checks each
package the PR releases (the manifest entries it moves, plus the component or
group its branch names), reading the current version and the config from the
merge base of the PR and its base branch, so a sibling release merged meanwhile
does not count, and the proposed version from the PR. It fails when:

- **stale**: the proposed version is already tagged, or the package's pin is
  not newer than its current version. A pin or footer left behind re-proposes
  a released version, which release-please only warns about
  (`DuplicateReleaseError`) after merging.
- **breaking**: a breaking commit since the package's tag of the current
  version (`qmochi/v0.2.0-alpha.1`) touches it, and the proposed version is
  not exactly `x.(y+1).0-alpha.0` (from 1.0, `(x+1).0.0-alpha.0`) or the stable
  form of either. `0.5.1-alpha.3 → 0.6.0-alpha.3` fails.
- **linked**: see [Linked releases](#linked-releases).
- **pins**: the PR's own config, at its head, still holds a pin its manifest
  has reached, or a linked-versions plugin entry whose members keep no pin.
  The PR is waiting for its `pins` job, see below.

Each failure prints the `scripts/release-as.sh` command that fixes it. The job
is skipped on every other PR. Run it locally the same way; `--config`,
`--manifest`, `--branch` and `--head-ref` take other paths and names:

```sh
scripts/check-release-bump.sh origin/next \
  origin/release-please--branches--next--groups--kit
```

**The helper.** `scripts/release-as.sh <component> <version>`
(`make release-as COMPONENT=qmochi VERSION=0.3.0-alpha.0`) pins one package:
it adds `"release-as": "<version>"` right after the package's `component`
line, checks with `jq` that nothing else in the config changed, and commits
the config alone as `chore(release): start <component> <version>` (`cut` for
a stable version). `--group <group> <version>` pins every member of a linked
group and wires the plugin, `--cleanup` drops the pins the manifest has
reached, and `--dry-run` prints the diff instead. It refuses, writing nothing,
a version not newer than the manifest's, a pin the package already carries
(so never an empty commit), a linked release for a single member, and a run
over staged changes. After a pin it prints the dry-run to run once the branch
is pushed.

**The release PR drops its pins.** The `pins` job of
`.github/workflows/changelog-rewrite.yml` runs `scripts/release-as.sh
--cleanup` on the release PR's branch. A pin that the PR's manifest has
reached is one the PR cuts, so it goes there, with a linked release's plugin
entry once no member keeps a pin. Merging the release PR leaves the config as
it was before the pins, and no later release PR re-proposes the version.

Until that commit lands, the check's pins rule fails the PR ("Waiting for the
pins job"), and the job's push runs the check again. release-please rewrites
its branch on every push to the base, pins included, and the job drops them
again. The job runs after the changelog rewrite and the lockfile sync whether
they passed or failed, but not in a cancelled run, which leaves it to the
newer one. If it fails, re-run it, or run `scripts/release-as.sh --cleanup` on
the PR branch and push. Once `breaking-bump` is a required check (see the
release brake above), this holds the merge until the pins are gone; a pin
that survives anyway fails the next release PR as stale.

To fix a failing release PR, run the command the check printed on a branch off
the PR's base, push, dry-run against that branch (the component's or group's
title shows the new version, every other title is unchanged) and merge.
release-please rebuilds the release PR at the new version, and the check
passes.

How the rule meets the rest of the flow:

- **Channels.** A breaking change during `beta` or `rc` also starts the new
  line at `alpha`: `0.6.0-beta.2` plus a breaking change becomes
  `0.7.0-alpha.0`. A new minor line is new work, and it earns `beta` again.
  Leave the config's `prerelease-type` alone: it only takes effect after a
  stable version, and the gate forbids moving it back. Pin the new line to
  `0.7.0-beta.0` when it is ready.
- **Stable cuts.** The first breaking change after a stable version needs no
  pin: from a stable version release-please bumps by the commits, so `0.6.0`
  plus a `feat!:` already proposes `0.7.0-alpha.0` (`2.0.0-alpha.0` from
  `1.2.0`). A stable cut (a pin of `0.6.0` from `0.6.0-rc.N`) passes unless a
  breaking commit touched the component since `rc.N`; `main` takes fixes only,
  so such a commit should not be there. If it is, the check asks for
  `0.7.0-alpha.0`.
- **LTS lines** take fixes only, so the check has nothing to catch there.

### Linked releases

The config's top-level `x-linked-groups` key lists linked groups;
release-please ignores it, and this repo's scripts read it. Today there is
one, `kit`: `kit`, `kit-php`, `kit-py`, `kit-rs` and `kit-ts`.

A release is linked when a member's proposed version starts a new major, in
any channel, or a new minor with a prerelease suffix (the pre-release phase:
`alpha`, `beta`, `rc` on `next`). A linked release releases every member at
that same version, the highest across the group, in one PR, members without
changes of their own included. Anything else (patches, prerelease counters,
stable minors) versions each member on its own, until the next linked release.

release-please's `linked-versions` plugin cannot link only sometimes: wired
permanently, it would align every release. So it is wired for one release at
a time. `scripts/release-as.sh --group kit 0.6.0-alpha.0` pins every member to
that version and adds the plugin to the config's `plugins`, on one line; its
commit carries `Release-As: 0.6.0-alpha.0`, which makes it count for `kit`.
release-please then opens one PR, `chore(next): release kit libraries` on
`release-please--branches--next--groups--kit`, moving every member: the plugin
gives a member without commits a synthetic one. The `pins` job drops the pins
and the plugin entry in that PR, so after the merge release-please is back to
one PR per component. `group-pull-request-title-pattern`
(`chore${scope}: release ${component} libraries`) stays in the config for
good: release-please needs it to recognise the merged group PR, and without it
the merge tags no member.

The check fails a member's release PR whose proposed version is linked unless
that PR moves every member to the same version, and prints the `--group`
command with the highest version the group can take. A member's own pin of a
linked version is refused for the same reason. release-please never closes
the per-component PRs a group PR supersedes: close them by hand.

### Pins and the main/next window

A pin lives only on the branch that cuts its version: the same pin on `main`
would have `main` propose that version too. So while a pin is pending, the
config differs between `main` and `next` by the pin lines, and the plugin line
of a linked release. The release PR's cleanup restores byte-identity when it
merges. Every other config change still lands on both branches alike.

The risk is a `next → main` promotion inside that window. It carries the pins
into `main`, and like every promotion it replays `next`'s history, including
the `Release-As:` footer of a root pin commit. `main`'s release-please then
proposes versions already tagged on `next`. The stale rule catches both: the
proposed version is already tagged, so the check fails, and the fix is
`scripts/release-as.sh --cleanup` for pins, or for a footer a pin of the
version `main` should cut, which wins over it. Promote after the pending
release PR merges and neither happens.

## Components

| Component | Path | Type | Prerelease channel (`next`) |
|-----------|------|------|------------------------------|
| kit | `.` | Go | alpha → beta → rc |
| kit-php | `sdk/experimental/php` | PHP | experimental |
| kit-py | `sdk/py` | Python | alpha → beta → rc |
| kit-rs | `sdk/experimental/rs` | Rust | experimental |
| kit-ts | `sdk/ts` | Node | alpha → beta → rc |
| qmochi | `incubator/qmochi` | Go | alpha → beta → rc |

`kit`, `kit-php`, `kit-py`, `kit-rs` and `kit-ts` form the linked group `kit`:
each versions on its own, except in a linked release, see
[Linked releases](#linked-releases).

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

## Bump policy

Both branches share one config, so a commit bumps the same way on either.

On a prerelease line (the manifest carries `-alpha.N`, `-beta.N` or `-rc.N`):

- `feat:` / `fix:` / `perf:` → the counter (`0.5.0-alpha.26 → 0.5.0-alpha.27`).
- `feat!:` / `BREAKING CHANGE` → a new minor line, `x.(y+1).0-alpha.0`
  (`(x+1).0.0-alpha.0` from 1.0), by `scripts/release-as.sh`; see
  [Breaking changes start a new minor line](#breaking-changes-start-a-new-minor-line).

After a stable version, release-please opens the next line from the commits,
at the config's `prerelease-type` (`-alpha.0`):

- `fix:` / `perf:` → patch (`0.6.0 → 0.6.1-alpha.0`).
- `feat:` → minor (`0.6.0 → 0.7.0-alpha.0`).
- `feat!:` / `BREAKING CHANGE` → minor before 1.0 (`bump-minor-pre-major`),
  major from 1.0 (`1.2.0 → 2.0.0-alpha.0`).

For a kit group member, a new minor with a prerelease suffix or a new major is
a linked release, whichever commit causes it: `0.6.0` plus a `feat:` proposes
`0.7.0-alpha.0`, and the whole group moves there with
`scripts/release-as.sh --group kit 0.7.0-alpha.0`; see
[Linked releases](#linked-releases).

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
