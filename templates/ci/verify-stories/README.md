# verify-stories CI templates

Drop one of these into your CI provider's workflow directory to wire
`kit conformance verify-stories` (and the belt-and-suspenders
`verify-no-leak` pass) into your pipeline.

| File | Provider |
|------|----------|
| `github-actions.yml` | GitHub Actions |

The workflow scans `e2e/stories/` on PRs that touch that path. Every
story must:

1. Pass the closed-key schema validator (no `scenario_id`,
   `assertions`, `judge`, or `cassette_must_*` keys; well-formed
   `story_id`, `intent`, `steps`, etc.).
2. Pass the leak detector on the same bytes (the structural
   distinctness guarantee).

A failing run uploads `/tmp/verify-stories.json` as an artifact so
adopters can grep findings without re-running CI.
