# Release branch wiring — review notes

Wires CI to the `next`/`main` branch model documented in `RELEASING.md`.
Staged on branch `release-branch-wiring` (off `main`); nothing lands on `main`
until reviewed.

## What changed

| File | Change |
|------|--------|
| `.github/workflows/release-please.yml` | Trigger on `main` **and** `next`; pass `target-branch: ${{ github.ref_name }}`; add a "Derive stable config on main" step that strips `prerelease`/`prerelease-type`/`versioning` from all packages **in-CI only** when the branch is `main`. |
| `.github/workflows/changelog-rewrite.yml` | Run on PRs into `next` as well as `main`. |
| `.github/workflows/nightly-release.yml` | Matrix over `[main, next]`; find each branch's `release-please--branches--<branch>` PR and auto-merge it. Also switched `--squash` → `--merge` (see below). |

## Design: branch-driven prerelease

release-please-action@v4 has **no runtime `prerelease` input** — the flag is
read from the config file. To keep one committed config across both branches
(no drift, conflict-free `next → main` merges), the `main` leg patches the
config ephemerally with `jq` before release-please runs:

- **`next`**: config used as-is → `prerelease: true` → cuts `*-alpha|beta|rc.N`.
- **`main`**: `jq` strips the prerelease keys → cuts stable `x.y.z`.

The edit is never committed; `release-please-config.json` is byte-identical on
both branches.

### Verified

- All three workflows parse as valid YAML.
- The `jq` patch strips `prerelease`/`prerelease-type`/`versioning` from all 6
  packages (kit, kit-ts, kit-py, kit-rs, kit-php, qmochi).
- Manifest currently holds `0.5.0-alpha.N`; with flags stripped on `main`,
  release-please graduates the prerelease to stable `0.5.0` on the next bump —
  the intended first-stable behavior. `bump-minor-pre-major: true` is retained
  (still pre-1.0), so bump size is identical on both branches.

## Operational caveats (not blocking)

- **Shared manifest.** `.release-please-manifest.json` is one file on both
  branches; each branch's release-please writes that branch's versions into it.
  While `next` (prerelease) and `main` (stable) advance independently, the
  manifest will differ between branches and must reconcile at `next → main`
  promotion. Inherent to the two-branch model.
- **`--merge` not `--squash`.** release-please PRs carry one commit per package
  with its own `Release-As` trailer; squashing collapses them and loses
  per-package attribution. nightly-release now plain-merges.

## Pre-existing bugs found (out of scope — flagging, not fixing here)

1. **`release-promote-gate.yml` watches the wrong path.** Its `paths:` filter
   and `git show BASE:release-please-config.json` reference
   `release-please-config.json` at repo root, but the real config is at
   `.github/release-please-config.json`. The gate never fires and always takes
   the "initial setup — skip" branch. Unrelated to this wiring; the in-CI patch
   does not interact with it (the patch is never committed, so no PR diff shows
   it).
2. **`changelog-rewrite.yml` uses `github.head_ref` in `ref:`.** Pre-existing;
   gated to bot-created `release-please--*` branches, so not attacker-reachable,
   but worth hardening separately.

## Not done (needs a human decision before merge)

- **Branch protection / CODEOWNERS for `next`.** `next` should get the same
  protection rules as `main` (required checks, no direct pushes) once this
  lands. Not configurable from the repo tree.
- **First stable cutover.** Actually cutting `0.5.0` stable from `main` is a
  deliberate release action, not part of this wiring.
