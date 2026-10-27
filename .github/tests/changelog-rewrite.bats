#!/usr/bin/env bats
# Tests for .github/workflows/changelog-rewrite.yml and the script it runs.
#
# Pathspec tests pull a step's `run:` script out of the workflow file itself,
# so the test follows the workflow, and run it in a throwaway repo holding a
# root CHANGELOG.md and a nested one. Rewrite tests run
# scripts/rewrite-changelog.sh on a raw release-please entry.
#
# `[[ ]]` does not trip errexit under bash 3.2 (macOS /bin/bash), so a
# `[[ ]]` that is not a test's last command ends in `|| false`.
#
# Run: bats .github/tests/changelog-rewrite.bats
# Or:  make test-workflow

WORKFLOW="$BATS_TEST_DIRNAME/../workflows/changelog-rewrite.yml"

# step_script <step name>: print the step's `run: |` block, dedented.
step_script() {
    awk -v name="$1" '
        $0 ~ "^ *- name: " name "$" { in_step = 1; next }
        in_step && /^ *- name: / { exit }
        in_step && !run && /^ *run: \|$/ { run = 1; ri = index($0, "r"); next }
        run {
            if ($0 ~ /^ *$/) { print ""; next }
            match($0, /^ */)
            if (RLENGTH < ri) exit
            if (!bi) bi = RLENGTH + 1
            print substr($0, bi)
        }
    ' "$WORKFLOW"
}

setup() {
    # Keep the caller's git environment and config (hooks, identity) out.
    unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY
    export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

    TMP="$(mktemp -d)"
    git init -q --bare -b main "$TMP/origin.git"
    git init -q -b main "$TMP/repo"
    REPO="$TMP/repo"
    git -C "$REPO" config user.email t@example.com
    git -C "$REPO" config user.name t
    mkdir -p "$REPO/sdk/ts"
    printf '# Changelog\n' > "$REPO/CHANGELOG.md"
    printf '# Changelog\n' > "$REPO/sdk/ts/CHANGELOG.md"
    printf 'readme\n' > "$REPO/README.md"
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm base
    git -C "$REPO" remote add origin "$TMP/origin.git"
    git -C "$REPO" push -q -u origin main
    BASE_SHA="$(git -C "$REPO" rev-parse HEAD)"
    export GITHUB_OUTPUT="$TMP/output"
    : > "$GITHUB_OUTPUT"
}

teardown() {
    rm -rf "$TMP"
}

# commit_edit <path>...: append a line to each path and commit.
commit_edit() {
    local f
    for f in "$@"; do printf 'edit\n' >> "$REPO/$f"; done
    git -C "$REPO" commit -qam edit
}

run_step() {
    local script
    script="$(step_script "$1")"
    [ -n "$script" ]
    run env BASE_SHA="$BASE_SHA" bash --noprofile --norc -eo pipefail -c "cd '$REPO' && $script"
}

@test "step scripts are found in the workflow" {
    [ -n "$(step_script 'Detect modified changelogs')" ]
    [ -n "$(step_script 'Commit rewritten changelogs')" ]
}

# job_block <job id>: print the job's lines, up to the next top-level job.
job_block() {
    awk -v id="$1" '
        $0 ~ "^  " id ":$" { in_job = 1; print; next }
        in_job && /^  [A-Za-z0-9_-]+:$/ { exit }
        in_job { print }
    ' "$WORKFLOW"
}

# A GITHUB_TOKEN push starts runs as github-actions[bot] that wait for
# approval, so the release PR head would get no CI.
@test "rewrite job checks out and pushes with the release-bot app token" {
    local job
    job="$(job_block rewrite)"
    [ -n "$job" ]
    grep -qF 'uses: actions/create-github-app-token@' <<<"$job"
    grep -qF 'token: ${{ steps.app-token.outputs.token }}' <<<"$job"
    ! grep -qF 'token: ${{ secrets.GITHUB_TOKEN }}' <<<"$job" || false
}

@test "a newer run cancels the older one on the same branch" {
    grep -qxF '  group: changelog-rewrite-${{ github.head_ref || github.run_id }}' "$WORKFLOW"
    grep -qxF '  cancel-in-progress: true' "$WORKFLOW"
}

@test "detect: root CHANGELOG.md alone is found" {
    commit_edit CHANGELOG.md
    run_step 'Detect modified changelogs'
    [ "$status" -eq 0 ]
    grep -qx 'found=true' "$GITHUB_OUTPUT"
    grep -qx 'CHANGELOG.md' "$GITHUB_OUTPUT"
}

@test "detect: root and nested changelogs are both found" {
    commit_edit CHANGELOG.md sdk/ts/CHANGELOG.md
    run_step 'Detect modified changelogs'
    [ "$status" -eq 0 ]
    grep -qx 'CHANGELOG.md' "$GITHUB_OUTPUT"
    grep -qx 'sdk/ts/CHANGELOG.md' "$GITHUB_OUTPUT"
}

@test "detect: no changelog changed reports found=false" {
    commit_edit README.md
    run_step 'Detect modified changelogs'
    [ "$status" -eq 0 ]
    grep -qx 'found=false' "$GITHUB_OUTPUT"
    [[ "$output" == *"No changelogs modified"* ]]
}

@test "commit: rewritten root CHANGELOG.md is committed and pushed" {
    printf 'rewritten\n' >> "$REPO/CHANGELOG.md"
    run_step 'Commit rewritten changelogs'
    [ "$status" -eq 0 ]
    [ "$(git -C "$TMP/origin.git" log -1 --format=%s main)" = "chore: rewrite changelog" ]
    [ "$(git -C "$TMP/origin.git" diff-tree --no-commit-id --name-only -r main)" = "CHANGELOG.md" ]
}

@test "commit: clean tree commits nothing" {
    run_step 'Commit rewritten changelogs'
    [ "$status" -eq 0 ]
    [[ "$output" == *"already processed"* ]] || false
    [ "$(git -C "$TMP/origin.git" rev-parse main)" = "$BASE_SHA" ]
}

# --- scripts/rewrite-changelog.sh ------------------------------------------

REWRITE="$BATS_TEST_DIRNAME/../../scripts/rewrite-changelog.sh"

# rewrite_fixture: write a raw release-please entry to $REPO/CHANGELOG.md, with
# a stub `gh` first on PATH so the Contributors lookup never reaches GitHub.
rewrite_fixture() {
    mkdir -p "$TMP/bin"
    printf '#!/bin/sh\nexit 1\n' > "$TMP/bin/gh"
    chmod +x "$TMP/bin/gh"
    cat > "$REPO/CHANGELOG.md" <<'MD'
# Changelog

## [0.6.0](https://github.com/hop-top/kit/compare/kit-v0.5.0...kit-v0.6.0) (2026-04-18)

### Features

* **bus:** pluggable adapter ([abc1234](https://github.com/hop-top/kit/commit/abc1234))
* **cli:** scaffolder ([#46](https://github.com/hop-top/kit/pull/46)) ([def5678](https://github.com/hop-top/kit/commit/def5678))

### Bug Fixes

* **core:** raw sha (aaa1111)
MD
}

run_rewrite() {
    run env PATH="$TMP/bin:$PATH" bash "$REWRITE" \
        --file "$REPO/CHANGELOG.md" --component Kit --repo hop-top/kit "$@"
}

@test "rewrite: commit and PR links on bullets survive" {
    rewrite_fixture
    run_rewrite
    [ "$status" -eq 0 ]
    local f="$REPO/CHANGELOG.md"
    grep -qxF '* **bus:** pluggable adapter ([abc1234](https://github.com/hop-top/kit/commit/abc1234))' "$f"
    grep -qxF '* **cli:** scaffolder ([#46](https://github.com/hop-top/kit/pull/46)) ([def5678](https://github.com/hop-top/kit/commit/def5678))' "$f"
    grep -qxF '* **core:** raw sha (aaa1111)' "$f"
}

@test "rewrite: intro, full diff link and marker still added; second run is a no-op" {
    rewrite_fixture
    run_rewrite
    [ "$status" -eq 0 ]
    local f="$REPO/CHANGELOG.md"
    grep -qxF 'The hop-top team is happy to announce Kit 0.6.0. This release includes new features and bug fixes.' "$f"
    grep -qxF 'Full diff: [kit-v0.5.0...kit-v0.6.0](https://github.com/hop-top/kit/compare/kit-v0.5.0...kit-v0.6.0)' "$f"
    cp "$f" "$TMP/first.md"
    run_rewrite
    [ "$status" -eq 0 ]
    [[ "$output" == *"already rewritten"* ]] || false
    cmp -s "$TMP/first.md" "$f"
}

@test "rewrite: --dry-run leaves the file alone and reports no stripping" {
    rewrite_fixture
    cp "$REPO/CHANGELOG.md" "$TMP/before.md"
    run_rewrite --dry-run
    [ "$status" -eq 0 ]
    [[ "$output" == *"[dry-run] No changes made."* ]] || false
    [[ "$output" != *"strip"* ]] || false
    cmp -s "$TMP/before.md" "$REPO/CHANGELOG.md"
}
