#!/usr/bin/env bash
#
# bootstrap-scenarios-kit.sh — generate the local template for the
# private grader repo `hop-top/scenarios-kit`.
#
# Layer C content per umbrella 12fcc spec lines 83-93. See
# .tlc/tracks/12fcc-dog/design.md §2 for the layout this script
# materialises and ADR-0028 for the dual-repo decision.
#
# Usage:
#   scripts/bootstrap-scenarios-kit.sh [TARGET_DIR]
#
# TARGET_DIR defaults to ./scenarios-kit relative to cwd.
#
# Idempotency: re-running against an existing target re-writes the
# templated files in place. Any maintainer edits to those files are
# detected before overwrite via `git diff --exit-code`; if the target
# is a git repo and a templated file has uncommitted local changes,
# the script bails. Untracked maintainer-authored files (e.g. new
# scenarios under scenarios/) are never touched.
#
# This script DOES NOT call `gh repo create`. The maintainer runs that
# manually after reviewing the template (see "Next steps" output).
set -euo pipefail

TARGET="${1:-./scenarios-kit}"
SCRIPT_NAME="$(basename "$0")"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
note() { printf '%s: %s\n' "$SCRIPT_NAME" "$*"; }

# ── Sanity ────────────────────────────────────────────────────────
[[ -n "$TARGET" ]] || die "TARGET path is empty"

mkdir -p "$TARGET"
TARGET="$(cd "$TARGET" && pwd)"
note "target: $TARGET"

# Detect re-run vs initial bootstrap. A target with any prior content
# from this script (README.md or .verifynoleak.allow with our marker)
# is treated as a re-run; we re-template in place. A non-empty target
# without our marker is refused so we don't overwrite an unrelated
# repo by accident.
MARKER='# managed by scripts/bootstrap-scenarios-kit.sh'
INITIAL=1
if [[ -n "$(ls -A "$TARGET" 2>/dev/null)" ]]; then
  if [[ -f "$TARGET/.verifynoleak.allow" ]] && grep -qF "$MARKER" "$TARGET/.verifynoleak.allow"; then
    INITIAL=0
    note "re-run detected (marker found); re-templating in place"
  else
    die "refusing to bootstrap into non-empty $TARGET (no marker file). Move or empty the target."
  fi
fi

# ── Layout ────────────────────────────────────────────────────────
mkdir -p \
  "$TARGET/.github/workflows" \
  "$TARGET/scenarios/spaced" \
  "$TARGET/cassettes/spaced" \
  "$TARGET/prompts" \
  "$TARGET/contracts"

# ── Template helper ───────────────────────────────────────────────
# write_template path < heredoc
# If the file exists and is tracked in a git repo and dirty, bail.
# Else: write atomically and report whether content changed.
write_template() {
  local dst="$1"
  local tmp
  tmp="$(mktemp)"
  cat > "$tmp"
  if [[ -f "$dst" ]]; then
    if cmp -s "$tmp" "$dst"; then
      rm -f "$tmp"
      return 0
    fi
    # Different content. If under git and dirty, refuse.
    if (cd "$TARGET" && git rev-parse --is-inside-work-tree >/dev/null 2>&1); then
      local rel="${dst#"$TARGET/"}"
      if (cd "$TARGET" && git ls-files --error-unmatch -- "$rel" >/dev/null 2>&1); then
        if ! (cd "$TARGET" && git diff --quiet -- "$rel"); then
          rm -f "$tmp"
          die "templated file $rel has uncommitted local changes; commit or discard before re-running"
        fi
      fi
    fi
  fi
  mv "$tmp" "$dst"
  note "wrote $dst"
}

# ── README.md ─────────────────────────────────────────────────────
write_template "$TARGET/README.md" <<'EOF'
# scenarios-kit

Private scenario rubrics for `hop-top/kit` example apps. Layer C
content (12fcc umbrella spec lines 83-93).

## Access policy

Read+write: kit core maintainers only. Reviewed quarterly. Coding
agents have zero access — this repo's existence is the cornerstone
of the Layer C threat model.

## How this repo is consumed

A workflow in `hop-top/kit`
(`.github/workflows/dogfood-grade.yml`) builds the spaced binary,
captures cassettes via xrr, uploads them as a CI artifact, and
emits a `repository_dispatch` event of type `grade-pr` to this
repo. The local `grade-on-dispatch.yml` workflow downloads the
cassettes, runs the kit grader binary, and posts a Tier 1 status
check back to the kit PR.

## What this repo MUST NOT do

- Post any scenario content (assertions, prompts, diffs) in a
  status-check body or comment on the public kit repo. Tier 1
  output only.
- Accept dispatch events from any repository other than
  `hop-top/kit`. Filter on `github.event.client_payload.source`.

## Layout

```
.
├── README.md                        # this file
├── LICENSE                          # MIT (matches kit)
├── .gitignore
├── .verifynoleak.allow              # this repo's allowlist
├── .github/workflows/
│   ├── grade-on-dispatch.yml        # cross-repo dispatch handler
│   ├── grade-on-pr.yml              # smoke-test on private-repo PRs
│   └── sync-rules.yml               # drift check against public rules
├── scenarios/<app>/<name>.yaml      # scenario rubrics (the secret)
├── cassettes/<app>/<name>.cassette  # baseline cassettes
├── prompts/                         # judge prompts (none in v1)
└── contracts/
    └── scenario-rules.json          # mirror of public rules
```

## Adding a scenario

1. Capture cassette: `xrr capture --out cassettes/<app>/<name>.cassette
   -- <binary invocation>` against the kit's main.
2. Author scenario YAML under `scenarios/<app>/<name>.yaml`.
3. Validate locally: `kit conformance grade
   cassettes/<app>/<name>.cassette scenarios/<app>/<name>.yaml`.
4. Open a PR. The `grade-on-pr.yml` workflow validates the
   scenario parses and grades the in-repo cassette cleanly.
5. Merge once green.

## Drift policy

`contracts/scenario-rules.json` is a mirror of the file in
`hop-top/kit/contracts/scenario-rules.json`. The `sync-rules.yml`
workflow runs nightly and fails if the mirror has drifted from
upstream. Maintainers update the mirror by re-copying from the
latest public release tag, never by hand-edit.

## Provenance

This template was generated by
`scripts/bootstrap-scenarios-kit.sh` in `hop-top/kit`. Re-run that
script (against this directory) to refresh the templated files;
maintainer-authored content under `scenarios/`, `cassettes/`, and
`prompts/` is preserved.
EOF

# ── LICENSE ───────────────────────────────────────────────────────
write_template "$TARGET/LICENSE" <<'EOF'
MIT License

Copyright (c) hop.top kit maintainers

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
EOF

# ── .gitignore ────────────────────────────────────────────────────
write_template "$TARGET/.gitignore" <<'EOF'
*.test
*.out
/bin/
/dist/
.DS_Store
EOF

# ── .verifynoleak.allow ───────────────────────────────────────────
write_template "$TARGET/.verifynoleak.allow" <<'EOF'
# managed by scripts/bootstrap-scenarios-kit.sh
# scenarios/, cassettes/, prompts/ ARE rubric. The leak gate exempts
# them; the gate runs on README.md and other prose to catch
# accidental rubric inclusion in commentary.
scenarios/**
cassettes/**
prompts/**
EOF

# ── Sample seed scenario ──────────────────────────────────────────
write_template "$TARGET/scenarios/spaced/launch-dry-run-no-mutation.yaml" <<'EOF'
# Seed scenario shipped by the bootstrap template. Refine against
# real spaced behaviour before relying on it in CI.
scenario_id: spaced.launch.dry-run-no-mutation
schema_version: "1"
binary: spaced
factor_coverage: [3, 5, 6]
tier: 1
story_ref:
  story_id: spaced.launch.dry-run-walkthrough
  schema_min: "1"
  content_sha256: "REPLACE_WITH_ACTUAL_STORY_HASH"
description: "spaced launch --dry-run must not mutate and must emit structured output"

steps:
  - id: dry_run
    invoke: ["./spaced", "launch", "--payload", "alpha", "--dry-run", "--format", "json"]
    capture: [exit_code, stdout, stderr, cassette_diff, duration_ms]

assertions:
  - id: exit-ok
    on: dry_run
    kind: exit_code_equals
    value: 0
    factor: 5
  - id: structured
    on: dry_run
    kind: output_schema_matches
    schema_ref: spaced.launch.v1
    factor: 3
  - id: no-mutation
    on: dry_run
    kind: dry_run_no_mutation
    factor: 6
EOF

# ── contracts/scenario-rules.json placeholder ─────────────────────
write_template "$TARGET/contracts/scenario-rules.json" <<'EOF'
{
  "_comment": "Mirror of hop-top/kit/contracts/scenario-rules.json. Replace this stub by running scripts/sync-scenario-rules.sh (planned helper) or by copying from the latest kit release tag. The sync-rules.yml workflow fails if this file drifts from upstream.",
  "schema_version": "1",
  "rules": []
}
EOF

# ── Workflow: grade-on-dispatch.yml ───────────────────────────────
write_template "$TARGET/.github/workflows/grade-on-dispatch.yml" <<'EOF'
# Cross-repo dispatch handler. Fires when hop-top/kit emits a
# repository_dispatch event of type `grade-pr`. Downloads the
# cassettes artifact, grades against the matching scenarios, and
# posts a Tier 1 status check back to the kit PR.
#
# SHAs below are placeholders. Resolve to commit SHAs before merging.
name: grade-on-dispatch
on:
  repository_dispatch:
    types: [grade-pr]

permissions:
  contents: read
  actions: read       # to fetch cross-repo artifact
  statuses: write     # to post status check back to public repo

jobs:
  grade:
    runs-on: ubuntu-latest
    if: github.event.client_payload.source == 'hop-top/kit'
    steps:
      - uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5  # v4.3.1

      - uses: actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff  # v5.6.0
        with:
          go-version: stable

      - name: download cassettes from kit
        env:
          GH_TOKEN: ${{ secrets.KIT_RO_TOKEN }}
        run: |
          set -euo pipefail
          artifact='${{ github.event.client_payload.artifact_name }}'
          url=$(gh api repos/hop-top/kit/actions/artifacts \
            --jq ".artifacts[] | select(.name == \"${artifact}\") | .archive_download_url" \
            | head -1)
          [ -n "$url" ] || { echo "artifact ${artifact} not found"; exit 1; }
          gh api "$url" > cassettes.zip
          mkdir -p /tmp/cassettes
          unzip -o cassettes.zip -d /tmp/cassettes

      - name: install kit grader
        run: go install hop.top/kit/cmd/kit@latest

      - name: grade each scenario (tier 1 hard-coded)
        id: grade
        run: |
          set -euo pipefail
          # Tier 1 hard-coded per dog design §6.4 + ADR-0028. The
          # dispatch payload MUST NOT carry a tier field; this
          # workflow ignores any such field if present.
          out='[]'
          for s in scenarios/spaced/*.yaml; do
            name=$(basename "$s" .yaml)
            cassette="/tmp/cassettes/${name}.cassette"
            if [ ! -f "$cassette" ]; then
              continue
            fi
            verdict=$(kit conformance grade "$cassette" "$s" --tier=1 --format=json)
            out=$(jq --argjson v "$verdict" '. + [$v]' <<<"$out")
          done
          echo "results=$out" >> "$GITHUB_OUTPUT"

      - name: post status check to kit PR
        env:
          GH_TOKEN: ${{ secrets.KIT_STATUS_TOKEN }}
        run: |
          set -euo pipefail
          results='${{ steps.grade.outputs.results }}'
          state=$(jq -r 'if length == 0 then "success" elif all(.verdict == "pass") then "success" else "failure" end' <<<"$results")
          summary=$(jq -r '.[] | "- \(.scenario_id): \(.verdict) (factors: \((.factor_coverage // []) | join(", ")))"' <<<"$results" | head -c 140)
          gh api -X POST \
            "repos/hop-top/kit/statuses/${{ github.event.client_payload.commit_sha }}" \
            -f state="$state" \
            -f context="dogfood/scenarios" \
            -f description="$summary"
EOF

# ── Workflow: grade-on-pr.yml ─────────────────────────────────────
write_template "$TARGET/.github/workflows/grade-on-pr.yml" <<'EOF'
# Smoke-test scenarios on PRs to the private repo. Confirms the
# scenario YAML parses and grades cleanly against its companion
# cassette in the same PR. Does NOT call back to the public kit
# repo — this is private-repo-internal CI only.
name: grade-on-pr
on:
  pull_request:
    paths:
      - 'scenarios/**'
      - 'cassettes/**'
      - '.github/workflows/grade-on-pr.yml'

permissions:
  contents: read

jobs:
  smoke:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5  # v4.3.1
      - uses: actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff  # v5.6.0
        with:
          go-version: stable
      - name: install kit grader
        run: go install hop.top/kit/cmd/kit@latest
      - name: smoke-grade in-repo scenarios
        run: |
          set -euo pipefail
          # For each scenario with a sibling cassette, grade tier 1.
          for s in scenarios/*/*.yaml; do
            app=$(basename "$(dirname "$s")")
            name=$(basename "$s" .yaml)
            cassette="cassettes/${app}/${name}.cassette"
            [ -f "$cassette" ] || { echo "skip ${name}: no cassette"; continue; }
            kit conformance grade "$cassette" "$s" --tier=1
          done
EOF

# ── Workflow: sync-rules.yml ──────────────────────────────────────
write_template "$TARGET/.github/workflows/sync-rules.yml" <<'EOF'
# Nightly drift check: compare contracts/scenario-rules.json against
# the canonical copy in hop-top/kit at the latest release tag. Fails
# if drift is detected so a maintainer can re-mirror.
name: sync-rules
on:
  schedule:
    - cron: '0 7 * * *'
  workflow_dispatch:

permissions:
  contents: read

jobs:
  diff:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5  # v4.3.1
      - name: fetch upstream rules
        env:
          GH_TOKEN: ${{ secrets.KIT_RO_TOKEN }}
        run: |
          set -euo pipefail
          tag=$(gh api repos/hop-top/kit/releases/latest --jq .tag_name)
          gh api "repos/hop-top/kit/contents/contracts/scenario-rules.json?ref=${tag}" \
            --jq .content | base64 -d > /tmp/upstream-rules.json
      - name: diff
        run: |
          if ! diff -u /tmp/upstream-rules.json contracts/scenario-rules.json; then
            echo "::error::contracts/scenario-rules.json has drifted from hop-top/kit upstream; re-mirror from the latest release tag"
            exit 1
          fi
EOF

# ── Optional: initial git repo ────────────────────────────────────
if [[ $INITIAL -eq 1 ]]; then
  if ! (cd "$TARGET" && git rev-parse --is-inside-work-tree >/dev/null 2>&1); then
    (cd "$TARGET" && git init -q -b main && git add -A && git commit -q -m "chore: bootstrap from hop-top/kit scripts/bootstrap-scenarios-kit.sh")
    note "initialised git repo and created bootstrap commit"
  fi
fi

# ── Next-steps message ───────────────────────────────────────────
cat <<EOF

Bootstrap complete: $TARGET

Next steps (run on a kit maintainer's laptop, NOT in CI):

  cd "$TARGET"
  # review the templated files, customise as needed
  gh repo create hop-top/scenarios-kit --private --source=. --remote=origin --push

After the repo exists:

  - Configure secrets per design §3:
      * KIT_RO_TOKEN     (actions:read on hop-top/kit)
      * KIT_STATUS_TOKEN (statuses:write on hop-top/kit)
  - Add DOGFOOD_DISPATCH_TOKEN to hop-top/kit's repo secrets
    (repo:dispatch on hop-top/scenarios-kit).
  - Mirror contracts/scenario-rules.json from the latest kit
    release tag.
  - Replace REPLACE_WITH_ACTUAL_STORY_HASH in the seed scenario
    with the output of:
      kit conformance verify-stories --paths examples/spaced/e2e/stories \\
        --print-content-sha256
EOF
