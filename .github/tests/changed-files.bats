#!/usr/bin/env bats
# Tests for scripts/changed-files.sh and scripts/lint-changed.sh, the
# changed-set definition the lint targets, the pre-push hook and CI share.
#
# Each test builds a throwaway repo holding copies of both scripts, with
# origin/next (and origin/main) as plain remote-tracking refs: no network.
#
# Run: bats .github/tests/changed-files.bats
# Or:  make test-lint-scripts

SRC="$BATS_TEST_DIRNAME/../../scripts"

setup() {
    REPO="$(mktemp -d)"
    git -C "$REPO" init -q -b next
    git -C "$REPO" config user.email t@example.com
    git -C "$REPO" config user.name t
    mkdir -p "$REPO/scripts" "$REPO/pkg/a" "$REPO/pkg/b" "$REPO/docs"
    cp "$SRC/changed-files.sh" "$SRC/lint-changed.sh" "$REPO/scripts/"
    printf 'package a\n' > "$REPO/pkg/a/a.go"
    printf 'package b\n' > "$REPO/pkg/b/b.go"
    printf '# doc\n' > "$REPO/docs/x.md"
    printf 'cfg: 1\n' > "$REPO/.lintrc"
    printf 'ignored.txt\n' > "$REPO/.gitignore"
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm base
    git -C "$REPO" update-ref refs/remotes/origin/next HEAD
    git -C "$REPO" update-ref refs/remotes/origin/main HEAD
    git -C "$REPO" switch -qc work
    unset LINT_ALL LINT_BASE LINT_HEAD LINT_BASE_REF
}

teardown() {
    rm -rf "$REPO"
}

commit_all() {
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm "$1"
}

cf() {
    run bash -c "cd '$REPO' && scripts/changed-files.sh $*"
}

lc() {
    run bash -c "cd '$REPO' && scripts/lint-changed.sh $*"
}

# --- changed set -----------------------------------------------------------

@test "no change: empty list, exit 0" {
    cf
    [ "$status" -eq 0 ]
    [ -z "$output" ]
}

@test "committed, staged, unstaged and untracked changes are all listed" {
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    commit_all c1
    printf 'package b // s\n' > "$REPO/pkg/b/b.go"
    git -C "$REPO" add pkg/b/b.go
    printf '# doc u\n' > "$REPO/docs/x.md"
    printf 'new\n' > "$REPO/docs/new.md"
    cf
    [ "$status" -eq 0 ]
    [ "$output" = "docs/new.md
docs/x.md
pkg/a/a.go
pkg/b/b.go" ]
}

@test "ignored files are not listed" {
    printf 'x\n' > "$REPO/ignored.txt"
    cf
    [ "$status" -eq 0 ]
    [ -z "$output" ]
}

@test "pathspecs narrow the list" {
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    printf '# doc u\n' > "$REPO/docs/x.md"
    cf "'*.go'"
    [ "$status" -eq 0 ]
    [ "$output" = "pkg/a/a.go" ]
}

@test "deleted files are dropped unless --include-deleted" {
    git -C "$REPO" rm -q pkg/b/b.go
    cf
    [ "$status" -eq 0 ]
    [ -z "$output" ]
    cf --include-deleted
    [ "$status" -eq 0 ]
    [ "$output" = "pkg/b/b.go" ]
}

@test "--filter AD lists added (untracked too) and deleted, not modified" {
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    git -C "$REPO" rm -q pkg/b/b.go
    printf 'new\n' > "$REPO/docs/new.md"
    cf --filter AD --include-deleted
    [ "$status" -eq 0 ]
    [ "$output" = "docs/new.md
pkg/b/b.go" ]
}

# --- base resolution -------------------------------------------------------

@test "base: merge-base with origin/next, not its moving tip" {
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    commit_all mine
    # next moves on after the branch was cut; its change is not ours.
    git -C "$REPO" switch -q next
    printf '# next\n' > "$REPO/docs/x.md"
    commit_all theirs
    git -C "$REPO" update-ref refs/remotes/origin/next HEAD
    git -C "$REPO" switch -q work
    cf
    [ "$status" -eq 0 ]
    [ "$output" = "pkg/a/a.go" ]
}

@test "base: nearest merge-base wins over an older main" {
    base="$(git -C "$REPO" rev-parse HEAD)"
    printf 'package b // next\n' > "$REPO/pkg/b/b.go"
    commit_all on-next
    git -C "$REPO" update-ref refs/remotes/origin/next HEAD
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    commit_all mine
    # origin/main still sits at the old base.
    [ "$(git -C "$REPO" rev-parse origin/main)" = "$base" ]
    cf
    [ "$status" -eq 0 ]
    [ "$output" = "pkg/a/a.go" ]
}

@test "base: a branch cut from main is measured against main" {
    # main is ahead of next here (a hotfix line); the branch forks from
    # main, so next's older merge-base would claim main's commit too.
    printf 'package b // main\n' > "$REPO/pkg/b/b.go"
    commit_all on-main
    git -C "$REPO" update-ref refs/remotes/origin/main HEAD
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    commit_all mine
    cf
    [ "$status" -eq 0 ]
    [ "$output" = "pkg/a/a.go" ]
}

@test "base: LINT_BASE_REF picks the ref to measure against" {
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    commit_all mine
    run bash -c "cd '$REPO' && LINT_BASE_REF=HEAD scripts/changed-files.sh"
    [ "$status" -eq 0 ]
    [ -z "$output" ]
}

@test "base: --base prints the resolved commit" {
    want="$(git -C "$REPO" rev-parse HEAD)"
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    commit_all mine
    cf --base
    [ "$status" -eq 0 ]
    [ "$output" = "$want" ]
}

@test "LINT_BASE + LINT_HEAD=HEAD: committed range only, no working tree" {
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    commit_all c1
    first="$(git -C "$REPO" rev-parse HEAD)"
    printf 'package b // c\n' > "$REPO/pkg/b/b.go"
    commit_all c2
    printf '# wip\n' > "$REPO/docs/x.md"
    printf 'new\n' > "$REPO/docs/new.md"
    run bash -c "cd '$REPO' && LINT_BASE=$first LINT_HEAD=HEAD scripts/changed-files.sh"
    [ "$status" -eq 0 ]
    [ "$output" = "pkg/b/b.go" ]
}

@test "LINT_BASE zero SHA (new branch push): lint everything" {
    run bash -c "cd '$REPO' && LINT_BASE=0000000000000000000000000000000000000000 scripts/changed-files.sh"
    [ "$status" -eq 3 ]
    [[ "$output" == *"no base"* ]]
}

@test "LINT_BASE unknown commit: lint everything, never skip" {
    run bash -c "cd '$REPO' && LINT_BASE=1234567890abcdef1234567890abcdef12345678 scripts/changed-files.sh"
    [ "$status" -eq 3 ]
}

@test "no default-branch ref: lint everything" {
    git -C "$REPO" update-ref -d refs/remotes/origin/next
    git -C "$REPO" update-ref -d refs/remotes/origin/main
    cf
    [ "$status" -eq 3 ]
}

# --- lint everything -------------------------------------------------------

@test "LINT_ALL=1: lint everything; LINT_ALL=0 does not" {
    run bash -c "cd '$REPO' && LINT_ALL=1 scripts/changed-files.sh"
    [ "$status" -eq 3 ]
    run bash -c "cd '$REPO' && LINT_ALL=0 scripts/changed-files.sh"
    [ "$status" -eq 0 ]
}

@test "--trigger: a changed config lints everything" {
    printf 'cfg: 2\n' > "$REPO/.lintrc"
    cf --trigger .lintrc "'*.go'"
    [ "$status" -eq 3 ]
    [[ "$output" == *".lintrc changed"* ]]
}

@test "--trigger: an unchanged config does not" {
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    cf --trigger .lintrc "'*.go'"
    [ "$status" -eq 0 ]
    [ "$output" = "pkg/a/a.go" ]
}

@test "a change to the scope scripts lints everything" {
    printf '# edit\n' >> "$REPO/scripts/lint-changed.sh"
    cf
    [ "$status" -eq 3 ]
    [[ "$output" == *"scripts/lint-changed.sh changed"* ]]
}

@test "--full-on-delete: a deletion lints everything" {
    git -C "$REPO" rm -q docs/x.md
    cf --full-on-delete "'*.md'"
    [ "$status" -eq 3 ]
    [[ "$output" == *"docs/x.md deleted"* ]]
}

# More changed paths than one pipe buffer (64 KiB) holds, so the producer is
# still writing when the first-hit reader has its answer: a reader that stops
# early (head -n 1) kills it with SIGPIPE, and pipefail turns that into exit
# 141 instead of the full-lint verdict. A directory move deletes this many.
many_paths() {
    mkdir -p "$REPO/$1"
    (cd "$REPO/$1" && for i in $(seq 3000); do : > "file-with-a-long-enough-name-$i.txt"; done)
}

@test "--full-on-delete: a mass deletion (directory move) still lints everything" {
    many_paths old
    commit_all many
    git -C "$REPO" update-ref refs/remotes/origin/next HEAD
    git -C "$REPO" mv old new
    commit_all move
    cf --full-on-delete "'*.md'"
    [ "$status" -eq 3 ]
    [[ "$output" == *" deleted"* ]]
}

@test "unknown option: usage error" {
    cf --nope
    [ "$status" -eq 2 ]
}

# --- lint-changed.sh -------------------------------------------------------

@test "lint-changed: nothing relevant changed prints a skip line, runs nothing" {
    printf '# doc u\n' > "$REPO/docs/x.md"
    lc t "'*.go'" -- touch ran
    [ "$status" -eq 0 ]
    [[ "$output" == *"t: no relevant changes, skipped"* ]]
    [ ! -e "$REPO/ran" ]
}

@test "lint-changed: changed files appended, relative to --cd" {
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    lc t --cd pkg --full ./... "'pkg/*.go'" -- echo LINT
    [ "$status" -eq 0 ]
    [[ "$output" == *"LINT a/a.go"* ]]
}

@test "lint-changed: full run gets the --full arguments" {
    printf 'cfg: 2\n' > "$REPO/.lintrc"
    lc t --cd pkg --full ./... --trigger .lintrc "'pkg/*.go'" -- echo LINT
    [ "$status" -eq 0 ]
    [[ "$output" == *"t: full run"* ]]
    [[ "$output" == *"LINT ./..."* ]]
}

@test "lint-changed: --gate runs the command bare once" {
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    printf 'package b // c\n' > "$REPO/pkg/b/b.go"
    lc t --gate pkg -- echo LINT
    [ "$status" -eq 0 ]
    [[ "$output" == *"2 relevant file(s) changed"* ]]
    [ "$(printf '%s\n' "$output" | grep -c '^LINT$')" -eq 1 ]
}

@test "lint-changed: the linter's failure is the exit status" {
    printf 'package a // c\n' > "$REPO/pkg/a/a.go"
    lc t "'*.go'" -- false
    [ "$status" -eq 1 ]
}
