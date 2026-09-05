# ci

Drop-in CI workflow templates that wire `kit conformance` leaves into common CI providers; copy, rename to the provider's path, commit.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`grade/`](grade/README.md) | `kit conformance grade` on GitHub Actions, GitLab CI, Buildkite, or a generic shell step; needs secrets | you want a conformance verdict on every PR |
| [`verify-no-leak/`](verify-no-leak/README.md) | `kit conformance verify-no-leak` on the same four providers | you gate merges on leak checks |
| [`verify-stories/`](verify-stories/README.md) | `kit conformance verify-stories` plus a `verify-no-leak` pass, GitHub Actions only | your repo carries user stories to verify |
