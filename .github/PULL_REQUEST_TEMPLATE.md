<!-- Adapted from the hop-top/.github template; maintained in this repo. -->
<!-- Title = Conventional Commit. No (#N) suffix, no tracker refs. -->

## What

<!-- Root cause / gap, then what this change does about it. -->

## Verification

<!-- Evidence, not claims: commands run + relevant output/numbers. -->

## Checks

- [ ] Conventional Commit title; no tracker/internal refs anywhere in the diff
- [ ] Tests cover the change; required checks green (`make check`)
- [ ] Touched `templates/` or `internal/template/builtins/`? Mirror kept in sync (`make check-mirror-sync`)
- [ ] Cross-language contract change? Every shipping SDK (Go / TS / Python / Rust / PHP) updated or explicitly out of scope
- [ ] Docs updated (README / RELEASING.md / docs/) if behavior changed
- [ ] Breaking change → `!` in title + migration note above
