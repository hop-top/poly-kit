# Release branch wiring — superseded

This page recorded the first wiring of CI to the `next`/`main` branch model:
an in-CI `jq` strip of the prerelease keys on `main`, graduation of a
prerelease manifest to stable there, and per-package `Release-As` trailers
kept apart by merging release PRs with `--merge`. None of it describes the
repository any more.

Releases now run through hop-top/.github's target-versioning extension: the
branch decides the version, `main` cuts exactly what an rc on `next` previewed,
and `Release-As` footers are ignored. See [RELEASING.md](../RELEASING.md) for
the branch model, channel promotion, the promotion gate and the release brake.
