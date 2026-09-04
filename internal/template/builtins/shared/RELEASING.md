# Releasing

## Version Lifecycle

```
0.1.0-alpha.0 -> .1 -> ... -> 0.1.0-beta.0 -> ... -> 0.1.0-rc.0 -> ... -> 0.1.0
```

| Stage   | Audience     | API              | Breaking changes  |
|---------|--------------|------------------|-------------------|
| alpha   | contributors | unstable         | expected          |
| beta    | testers      | feature-complete | only if critical  |
| rc      | everyone     | frozen           | showstoppers only |
| release | everyone     | stable           | next major only   |

## How releases work

1. Conventional commits on `main` trigger release-please
2. release-please creates/updates a release PR with version
   bumps + changelog
3. Merging the release PR creates GitHub Releases + tags
4. Per-language publish jobs fire automatically

## Promoting a release stage

`scripts/promote-release.sh` (behind the `make promote*` targets)
rewrites `prerelease-type` on every package to `<stage>.0`, or removes
it for `release`, and commits `chore(release): promote to <stage>`.
release-please reads that value only when the version it bumps has no
prerelease suffix; while the manifest carries one, every merged release
PR bumps that counter whatever the config says.

Interactive:

```bash
make promote
```

Explicit:

```bash
make promote-alpha    # seed the next line: x.y.z -> x.y+1.0-alpha.0
make promote-beta     # declare beta; jump with Release-As: x.y.z-beta.0
make promote-rc       # declare rc; jump with Release-As: x.y.z-rc.0
make promote-release  # reset the ladder; cuts nothing
```

Stable `x.y.z` is cut by a commit carrying `Release-As: x.y.z`, never by
`promote-release`. Run `promote-release` and `promote-alpha` back to
back while the manifest still carries the `rc` suffix: with no
`prerelease-type` and a stable manifest, the next merged release PR
would propose another stable version.

### Transition criteria

| Transition     | Criteria                         |
|----------------|----------------------------------|
| -> alpha       | new version cycle starts         |
| alpha -> beta  | all planned features merged      |
| beta -> rc     | no known bugs blocking release   |
| rc -> release  | 7-day bake, no regressions       |

## Nightly auto-release

A cron workflow runs nightly at 04:00 UTC. If a release-please PR
exists and CI is green, it auto-merges — producing a release without
manual intervention.

To disable: set the `NIGHTLY_RELEASE` repo variable to `false`, or
disable the workflow in GitHub Actions settings.

## Version synchronization

For polyglot projects, major.minor stays synchronized across
all language ports via release-please `linked-versions`. Patch
versions may differ.
