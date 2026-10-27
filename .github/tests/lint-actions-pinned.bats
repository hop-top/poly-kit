#!/usr/bin/env bats
# Tests for scripts/lint-actions-pinned.sh.
#
# Each test builds a throwaway git repo holding a copy of the script and
# one tracked workflow or composite action, then asserts the exit code
# and the file:line reported.
#
# Run: bats .github/tests/lint-actions-pinned.bats
# Or:  make test-lint-scripts

SCRIPT="$BATS_TEST_DIRNAME/../../scripts/lint-actions-pinned.sh"
SHA=0123456789abcdef0123456789abcdef01234567
DIGEST=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

setup() {
    REPO="$(mktemp -d)"
    git -C "$REPO" init -q
    mkdir -p "$REPO/scripts"
    cp "$SCRIPT" "$REPO/scripts/"
}

teardown() {
    rm -rf "$REPO"
}

# write <path> <content>: write a file and stage it.
write() {
    mkdir -p "$(dirname "$REPO/$1")"
    printf '%s\n' "$2" > "$REPO/$1"
    git -C "$REPO" add -A
}

run_lint() {
    run bash -c "cd '$REPO' && scripts/lint-actions-pinned.sh"
}

@test "no workflows passes" {
    run_lint
    [ "$status" -eq 0 ]
}

@test "SHA pins, local actions, digest-pinned docker and comments pass" {
    write .github/workflows/ci.yml "# Example for callers:
#   uses: hop-top/kit/.github/workflows/lint.yml@main
jobs:
  call:
    uses: org/repo/.github/workflows/x.yml@$SHA # v0.15.0
  a:
    steps:
      - uses: actions/checkout@$SHA # v7.0.1
      - uses: 'owner/repo@$SHA'
      - name: local
        uses: ./.github/actions/dev-env
      - uses: docker://alpine@sha256:$DIGEST
      - uses: owner/repo/sub/dir@$SHA  # v1"
    run_lint
    [ "$status" -eq 0 ]
}

@test "major tag fails with file:line" {
    write .github/workflows/ci.yml "jobs:
  a:
    steps:
      - uses: actions/checkout@$SHA # v7.0.1
      - uses: bufbuild/buf-action@v1"
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *".github/workflows/ci.yml:5: unpinned uses: bufbuild/buf-action@v1"* ]]
}

@test "branch ref on a reusable workflow fails" {
    write .github/workflows/publish.yml "jobs:
  publish:
    uses: org/repo/.github/workflows/x.yml@main"
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *"publish.yml:3: unpinned uses: org/repo/.github/workflows/x.yml@main"* ]]
}

@test "short SHA fails" {
    write .github/workflows/ci.yml "jobs:
  a:
    steps:
      - uses: owner/repo@${SHA:0:7}"
    run_lint
    [ "$status" -eq 1 ]
}

@test "version comment does not satisfy the pin" {
    write .github/workflows/ci.yml "jobs:
  a:
    steps:
      - uses: owner/repo@v2 # $SHA"
    run_lint
    [ "$status" -eq 1 ]
}

@test "docker ref without a digest fails" {
    write .github/workflows/ci.yml "jobs:
  a:
    steps:
      - uses: docker://alpine:3.20"
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *"unpinned uses: docker://alpine:3.20"* ]]
}

@test "composite actions are scanned" {
    write .github/actions/dev-env/action.yml "runs:
  using: composite
  steps:
    - uses: actions/cache@v4"
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *".github/actions/dev-env/action.yml:4: unpinned uses: actions/cache@v4"* ]]
}

@test "untracked workflow is out of scope" {
    mkdir -p "$REPO/.github/workflows"
    printf 'jobs:\n  a:\n    steps:\n      - uses: owner/repo@v1\n' > "$REPO/.github/workflows/scratch.yml"
    run_lint
    [ "$status" -eq 0 ]
}

@test "files given as arguments are checked" {
    write templates/ci.yml "jobs:
  a:
    steps:
      - uses: owner/repo@v1"
    run bash -c "cd '$REPO' && scripts/lint-actions-pinned.sh templates/ci.yml"
    [ "$status" -eq 1 ]
    [[ "$output" == *"templates/ci.yml:4:"* ]]
}

@test "missing argument file is a scan error" {
    run bash -c "cd '$REPO' && scripts/lint-actions-pinned.sh nope.yml"
    [ "$status" -eq 2 ]
}
