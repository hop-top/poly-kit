#!/usr/bin/env bats
# Tests for the changelog pathspecs in .github/workflows/changelog-rewrite.yml.
#
# Each test pulls a step's `run:` script out of the workflow file itself, so
# the test follows the workflow, and runs it in a throwaway repo holding a root
# CHANGELOG.md and a nested one.
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
    [[ "$output" == *"already processed"* ]]
    [ "$(git -C "$TMP/origin.git" rev-parse main)" = "$BASE_SHA" ]
}
