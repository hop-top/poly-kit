#!/usr/bin/env bats
# Tests for scripts/test-scope.sh (with path-refs.awk and go-affected.awk):
# which tests a change needs, per suite.
#
# Each test builds a throwaway repo holding copies of the scope scripts
# and a small Go module with a dependency chain (low <- mid <- top, plus
# an unrelated leaf and a package outside the race scope), SDK package
# dirs and contract vectors; origin/next is a plain remote-tracking ref,
# so nothing touches the network. Needs go on PATH.
#
# Run: bats .github/tests/test-scope.bats

SRC="$BATS_TEST_DIRNAME/../../scripts"

setup() {
    REPO="$(mktemp -d)"
    git -C "$REPO" init -q -b next
    git -C "$REPO" config user.email t@example.com
    git -C "$REPO" config user.name t
    mkdir -p "$REPO/scripts"
    cp "$SRC/changed-files.sh" "$SRC/lint-changed.sh" "$SRC/test-scope.sh" \
        "$SRC/test-affected.sh" "$SRC/path-refs.awk" "$SRC/go-affected.awk" "$REPO/scripts/"

    w go.mod 'module example.com/fx

go 1.22
'
    w go/low/low.go 'package low

import _ "embed"

//go:embed banner.txt
var Banner string

func Low() int { return 1 }
'
    w go/low/banner.txt 'hi'
    w go/low/low_test.go 'package low

import "testing"

func TestLow(t *testing.T) { _ = Low() }
'
    w go/low/testdata/case.json '{}'
    w go/mid/mid.go 'package mid

import "example.com/fx/go/low"

func Mid() int { return low.Low() }
'
    w go/top/top.go 'package top

import "example.com/fx/go/mid"

func Top() int { return mid.Mid() }
'
    w go/leaf/leaf.go 'package leaf

func Leaf() int { return 3 }
'
    # Imports low from its test only: still a reverse dependency.
    w go/tested/tested.go 'package tested
'
    w go/tested/tested_test.go 'package tested

import (
	"testing"

	"example.com/fx/go/low"
)

func TestTested(t *testing.T) { _ = low.Low() }
'
    # Names a contract vector from its test.
    w go/core/id/id.go 'package id
'
    w go/core/id/id_test.go 'package id

import (
	"os"
	"testing"
)

func TestContract(t *testing.T) { _, _ = os.ReadFile("../../../contracts/x-v1/v.json") }
'
    # Outside the race scope (go/, cmd/, internal/, examples/).
    w engine/store/store.go 'package store

import "example.com/fx/go/leaf"

func Store() int { return leaf.Leaf() }
'
    w go/user/user.go 'package user

import "example.com/fx/engine/store"

func User() int { return store.Store() }
'
    w contracts/x-v1/v.json '[]'
    w contracts/y-v1/v.json '[]'
    w sdk/ts/src/a.ts 'export const a = 1;'
    w sdk/ts/src/contract.test.ts 'const p = "../../../contracts/x-v1/v.json";'
    w sdk/py/kit/a.py 'A = 1'
    w docs/guide.md '# guide'
    w .github/tests/a.bats '#!/usr/bin/env bats
@test "a" { true; }'
    w .github/tests/b.bats '#!/usr/bin/env bats
@test "b" { cat docs/guide.md; }'
    w .github/tests/c.bats '#!/usr/bin/env bats
@test "c" { go build ./...; }'
    w Makefile 'test-parity: test-parity-typeid test-parity-mcp test-parity-new ## parity
'
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm base
    git -C "$REPO" update-ref refs/remotes/origin/next HEAD
    git -C "$REPO" switch -qc work
    unset LINT_ALL LINT_BASE LINT_HEAD LINT_BASE_REF TEST_ALL
}

teardown() {
    rm -rf "$REPO"
}

# w <path> <content>: write a fixture file.
w() {
    mkdir -p "$REPO/$(dirname "$1")"
    printf '%s\n' "$2" >"$REPO/$1"
}

# touch_ <path>...: append a line to each (a modification).
touch_() {
    local f
    for f in "$@"; do printf '\n' >>"$REPO/$f"; done
}

ts() {
    run bash -c "cd '$REPO' && scripts/test-scope.sh $*"
}

pkgs() {
    printf '%s\n' "$output" | cut -f2 | sed 's|^example.com/fx/||' | sort | tr '\n' ' ' | sed 's/ $//'
}

# --- no change, whole-suite paths -----------------------------------------

@test "no change: every suite empty, exit 0" {
    for s in go go-race rs ts py php bats parity; do
        ts "$s"
        [ "$status" -eq 0 ]
        [ -z "$output" ]
    done
}

@test "TEST_ALL=1: whole suite (exit 3)" {
    TEST_ALL=1 ts go
    [ "$status" -eq 3 ]
    [[ "$output" == *"TEST_ALL=1"* ]]
}

@test "a change to the scope script or Makefile runs every suite whole" {
    touch_ scripts/test-scope.sh
    for s in go ts bats parity; do
        ts "$s"
        [ "$status" -eq 3 ]
    done
    git -C "$REPO" checkout -q -- scripts/test-scope.sh
    touch_ Makefile
    ts py
    [ "$status" -eq 3 ]
}

@test "unknown base: whole suite, never a silent skip" {
    LINT_BASE=0123456789abcdef0123456789abcdef01234567 ts go
    [ "$status" -eq 3 ]
}

@test "root go.mod change: whole Go suite" {
    touch_ go.mod
    ts go
    [ "$status" -eq 3 ]
    ts go-race
    [ "$status" -eq 3 ]
}

@test "unknown suite or option: usage error" {
    ts nope
    [ "$status" -eq 2 ]
    ts --nope go
    [ "$status" -eq 2 ]
}

# --- Go: reverse dependencies ----------------------------------------------

@test "go: a leaf change runs the leaf and its importers only" {
    touch_ go/top/top.go
    ts go
    [ "$status" -eq 0 ]
    [ "$(pkgs)" = "go/top" ]
}

@test "go: a low-level change runs every reverse dependency, test imports included" {
    touch_ go/low/low.go
    ts go
    [ "$status" -eq 0 ]
    [ "$(pkgs)" = "go/low go/mid go/tested go/top" ]
}

@test "go: a _test.go change runs that package alone" {
    touch_ go/low/low_test.go
    ts go
    [ "$status" -eq 0 ]
    [ "$(pkgs)" = "go/low" ]
}

@test "go: testdata runs its package alone" {
    touch_ go/low/testdata/case.json
    ts go
    [ "$status" -eq 0 ]
    [ "$(pkgs)" = "go/low" ]
}

@test "go: an embedded file is part of the build: reverse deps run" {
    touch_ go/low/banner.txt
    ts go
    [ "$status" -eq 0 ]
    [ "$(pkgs)" = "go/low go/mid go/tested go/top" ]
}

@test "go: a new package runs; a deleted one runs the whole module" {
    w go/fresh/fresh.go 'package fresh'
    ts go
    [ "$status" -eq 0 ]
    [ "$(pkgs)" = "go/fresh" ]
    rm -rf "$REPO/go/fresh"
    git -C "$REPO" rm -rq go/leaf
    ts go
    [ "$status" -eq 0 ]
    [ "$output" = ".	ALL" ]
}

@test "go: only real packages are units, never a parent of subpackages" {
    # `go test` on a directory with no Go files of its own, or whose only
    # file was deleted, fails with "[setup failed]".
    w go/README.md '# go'
    touch_ go/low/low_test.go
    ts go
    [ "$status" -eq 0 ]
    [ "$(pkgs)" = "go/low" ]
    git -C "$REPO" rm -qf go/low/low_test.go
    ts go
    [ "$(pkgs)" = "go/low" ]
}

@test "go: a contract vector runs the package whose test names it" {
    touch_ contracts/x-v1/v.json
    ts go
    [ "$status" -eq 0 ]
    [ "$(pkgs)" = "go/core/id" ]
    git -C "$REPO" checkout -q -- contracts
    touch_ contracts/y-v1/v.json
    ts go
    [ "$status" -eq 0 ]
    [ -z "$output" ]
}

@test "go: TS-only and docs-only changes run no Go" {
    touch_ sdk/ts/src/a.ts docs/guide.md
    ts go
    [ "$status" -eq 0 ]
    [ -z "$output" ]
    ts --gate go
    [ "$status" -eq 0 ]
    [ -z "$output" ]
}

@test "go-race: keeps to the race scope, through packages outside it" {
    touch_ go/leaf/leaf.go
    ts go
    [ "$(pkgs)" = "engine/store go/leaf go/user" ]
    ts go-race
    [ "$status" -eq 0 ]
    [ "$(pkgs)" = "go/leaf go/user" ]
}

@test "go: committed range only with LINT_HEAD" {
    touch_ go/top/top.go
    git -C "$REPO" commit -qam c1
    touch_ go/low/low.go
    LINT_HEAD=HEAD ts go
    [ "$(pkgs)" = "go/top" ]
}

# --- SDK packages ----------------------------------------------------------

@test "ts: a change under the package; py untouched" {
    touch_ sdk/ts/src/a.ts
    ts ts
    [ "$status" -eq 0 ]
    [ "$output" = "sdk/ts" ]
    ts py
    [ -z "$output" ]
    ts --gate ts
    [ "$output" = "sdk/ts/src/a.ts" ]
}

@test "ts: a contract vector its sources name; not an unrelated one" {
    touch_ contracts/x-v1/v.json
    ts ts
    [ "$output" = "sdk/ts" ]
    git -C "$REPO" checkout -q -- contracts
    touch_ contracts/y-v1/v.json
    ts ts
    [ -z "$output" ]
}

@test "ts: the pnpm lockfile runs the whole suite" {
    w pnpm-lock.yaml 'lockfileVersion: 9'
    ts ts
    [ "$status" -eq 3 ]
}

# --- bats --------------------------------------------------------------------

@test "bats: a changed suite; a suite naming a changed file; Go builders on Go" {
    touch_ .github/tests/a.bats
    ts bats
    [ "$output" = ".github/tests/a.bats" ]
    git -C "$REPO" checkout -q -- .github
    touch_ docs/guide.md
    ts bats
    [ "$output" = ".github/tests/b.bats" ]
    git -C "$REPO" checkout -q -- docs
    touch_ go/leaf/leaf.go
    ts bats
    [ "$output" = ".github/tests/c.bats" ]
    git -C "$REPO" checkout -q -- go
    touch_ go/low/low_test.go
    ts bats
    [ -z "$output" ]
}

@test "bats: a shell script change runs every suite" {
    w scripts/x.sh 'echo x'
    ts bats
    [ "$status" -eq 3 ]
}

# --- parity ------------------------------------------------------------------

@test "parity: an SDK change runs the targets with that SDK; unknown targets always" {
    touch_ sdk/py/kit/a.py
    ts parity
    [ "$status" -eq 0 ]
    [ "$output" = "test-parity-typeid
test-parity-mcp
test-parity-new" ]
}

@test "parity: a Go change runs the targets whose Go half it reaches" {
    touch_ go/core/id/id.go
    ts parity
    [ "$output" = "test-parity-typeid
test-parity-new" ]
}

@test "parity: any contracts change runs every target" {
    touch_ contracts/y-v1/v.json
    ts parity
    [ "$status" -eq 3 ]
}

# --- test-affected.sh --gate -------------------------------------------------

@test "test-affected --gate: run=false with a skip summary, run=true otherwise" {
    touch_ docs/guide.md
    out="$REPO/out" sum="$REPO/sum"
    run bash -c "cd '$REPO' && GITHUB_OUTPUT='$out' GITHUB_STEP_SUMMARY='$sum' scripts/test-affected.sh --gate go"
    [ "$status" -eq 0 ]
    [ "$(cat "$out")" = "run=false" ]
    grep -q 'go: skipped' "$sum"
    touch_ go/leaf/leaf.go
    run bash -c "cd '$REPO' && GITHUB_OUTPUT='$out.2' scripts/test-affected.sh --gate go"
    [ "$(cat "$out.2")" = "run=true" ]
}
