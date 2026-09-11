#!/usr/bin/env bats
# Tests for .githooks/pre-push scoped lint/test logic.
#
# Validates that the hook correctly detects changed languages and
# constructs the right make/go targets. Does NOT run actual builds.
#
# Run: bats .github/tests/pre-push-hook.bats
# Or:  make test-hook

HOOK=".githooks/pre-push"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Extract the language-detection block and evaluate it against a fake
# CHANGED variable, printing which HAS_* vars are set.
detect_languages() {
    local changed="$1"
    CHANGED="$changed"
    HAS_GO=$(echo "$CHANGED" | grep -E '\.go$' || true)
    HAS_TS=$(echo "$CHANGED" | grep -E '^sdk/ts/' || true)
    HAS_PY=$(echo "$CHANGED" | grep -E '^sdk/py/' || true)
    HAS_DOCS=$(echo "$CHANGED" | grep -E '\.(md)$' || true)
    HAS_CLI=$(echo "$CHANGED" | grep -E '^go/console/cli/' || true)
}

# Extract Go packages from changed files (mirrors hook logic).
go_pkgs_from() {
    local changed="$1"
    echo "$changed" | grep -E '\.go$' \
        | xargs -I{} dirname {} \
        | sort -u \
        | sed 's|^|./|' \
        | tr '\n' ' '
}

# Extract testable Go packages, dropping directories that do not exist or
# hold no .go files of their own (mirrors hook logic).
go_dirs_testable() {
    local changed="$1"
    echo "$changed" | grep -E '\.go$' \
        | xargs -I{} dirname {} \
        | sort -u \
        | while IFS= read -r d; do
            [ -d "$d" ] || continue
            for f in "$d"/*.go; do
                [ -e "$f" ] || continue
                printf '%s\n' "$d"
                break
            done
          done | tr '\n' ' '
}

# ---------------------------------------------------------------------------
# Structure
# ---------------------------------------------------------------------------

@test "hook file exists and is executable" {
    [ -f "$HOOK" ]
    [ -x "$HOOK" ]
}

@test "hook is valid sh syntax" {
    bash -n "$HOOK"
}

@test "hook reads stdin (remote ref protocol)" {
    # pre-push hooks receive lines on stdin; the script captures them once.
    grep -q 'PUSH_INPUT=' "$HOOK"
}

@test "hook has SHA cache skip logic" {
    grep -q 'pre-push-last-sha' "$HOOK"
}

# ---------------------------------------------------------------------------
# Language detection
# ---------------------------------------------------------------------------

@test "detect: Go only" {
    detect_languages "go/runtime/bus/bus.go
go/runtime/bus/bus_test.go"
    [ -n "$HAS_GO" ]
    [ -z "$HAS_TS" ]
    [ -z "$HAS_PY" ]
    [ -z "$HAS_DOCS" ]
}

@test "detect: TypeScript only" {
    detect_languages "sdk/ts/src/cli.ts
sdk/ts/src/cli.test.ts"
    [ -z "$HAS_GO" ]
    [ -n "$HAS_TS" ]
    [ -z "$HAS_PY" ]
    [ -z "$HAS_DOCS" ]
}

@test "detect: Python only" {
    detect_languages "sdk/py/hop_top_kit/cli.py
sdk/py/tests/test_cli.py"
    [ -z "$HAS_GO" ]
    [ -z "$HAS_TS" ]
    [ -n "$HAS_PY" ]
    [ -z "$HAS_DOCS" ]
}

@test "detect: docs only" {
    detect_languages "README.md
docs/plans/foo.md"
    [ -z "$HAS_GO" ]
    [ -z "$HAS_TS" ]
    [ -z "$HAS_PY" ]
    [ -n "$HAS_DOCS" ]
}

@test "detect: mixed Go + TS + docs" {
    detect_languages "go/runtime/bus/bus.go
sdk/ts/src/bus.ts
CHANGELOG.md"
    [ -n "$HAS_GO" ]
    [ -n "$HAS_TS" ]
    [ -z "$HAS_PY" ]
    [ -n "$HAS_DOCS" ]
}

@test "detect: CLI triggers parity" {
    detect_languages "go/console/cli/root.go
go/console/cli/completion/complete.go"
    [ -n "$HAS_GO" ]
    [ -n "$HAS_CLI" ]
}

@test "detect: non-CLI Go does not trigger parity" {
    detect_languages "go/runtime/bus/bus.go
go/storage/kv/tidb/tidb.go"
    [ -n "$HAS_GO" ]
    [ -z "$HAS_CLI" ]
}

# ---------------------------------------------------------------------------
# Go package extraction
# ---------------------------------------------------------------------------

@test "go_pkgs: single file" {
    result=$(go_pkgs_from "go/runtime/bus/bus.go")
    [[ "$result" == *"./go/runtime/bus"* ]]
}

@test "go_pkgs: multiple files same package" {
    result=$(go_pkgs_from "go/runtime/bus/bus.go
go/runtime/bus/bus_test.go")
    # Should deduplicate to single ./go/runtime/bus
    [ "$(echo "$result" | xargs -n1 | wc -l | tr -d ' ')" = "1" ]
    [[ "$result" == *"./go/runtime/bus"* ]]
}

@test "go_pkgs: multiple packages" {
    result=$(go_pkgs_from "go/runtime/bus/bus.go
go/storage/kv/tidb/tidb.go
go/core/identity/jwt.go")
    [[ "$result" == *"./go/runtime/bus"* ]]
    [[ "$result" == *"./go/storage/kv/tidb"* ]]
    [[ "$result" == *"./go/core/identity"* ]]
}

@test "go_pkgs: nested paths" {
    result=$(go_pkgs_from "go/ai/llm/router/controller.go
go/storage/secret/file/file.go")
    [[ "$result" == *"./go/ai/llm/router"* ]]
    [[ "$result" == *"./go/storage/secret/file"* ]]
}

@test "go_pkgs: ignores non-go files" {
    result=$(go_pkgs_from "README.md
sdk/ts/src/cli.ts
go/runtime/bus/bus.go")
    [[ "$result" == *"./go/runtime/bus"* ]]
    [[ "$result" != *"./ts"* ]]
    [[ "$result" != *"README"* ]]
}

# ---------------------------------------------------------------------------
# Base resolution
# ---------------------------------------------------------------------------

@test "base: no hardcoded merge-base against main" {
    # The trunk for PRs is not necessarily main; a hardcoded main inflates
    # the changed set by every commit the real trunk gained meanwhile.
    ! grep -qE 'merge-base HEAD main' "$HOOK"
}

@test "base: considers next before main" {
    grep -qE 'for b in next main' "$HOOK"
}

@test "base: uses the push destination ref from stdin" {
    grep -q 'DEST_REF' "$HOOK"
    grep -q 'resolve_remote_ref' "$HOOK"
}

@test "base: destination candidate never falls back to a local branch" {
    # resolve_ref would resolve a brand-new branch to HEAD itself, whose
    # merge-base is HEAD, silently emptying the diff.
    ! grep -qE 'DEST_SHA=\$\(resolve_ref ' "$HOOK"
}

@test "base: falls back to HEAD~1 when nothing resolves" {
    grep -q 'echo HEAD~1' "$HOOK"
}

@test "base: never selects a base equal to HEAD" {
    grep -q 'base" = "$head_sha' "$HOOK"
}

# ---------------------------------------------------------------------------
# Empty-package filtering
# ---------------------------------------------------------------------------

@test "hook filters directories with no direct .go files" {
    # Guards against `go test` on a parent that only holds subpackages, or
    # on a directory whose only .go file was deleted in this push: both
    # abort the push with "[setup failed]".
    grep -q 'for f in "$d"/\*.go' "$HOOK"
}

@test "go_dirs: skips directory with no direct .go files" {
    tmp="$BATS_TEST_TMPDIR/nogo"
    mkdir -p "$tmp/parent/child"
    : > "$tmp/parent/child/x.go"
    cd "$tmp"
    result=$(go_dirs_testable "parent/only_subpackages.go")
    [ -z "$(echo "$result" | tr -d ' ')" ]
}

@test "go_dirs: keeps directory that has direct .go files" {
    tmp="$BATS_TEST_TMPDIR/hasgo"
    mkdir -p "$tmp/pkg"
    : > "$tmp/pkg/a.go"
    cd "$tmp"
    result=$(go_dirs_testable "pkg/a.go")
    [[ "$result" == *"pkg"* ]]
}

@test "go_dirs: skips directory removed in this push" {
    tmp="$BATS_TEST_TMPDIR/gone"
    mkdir -p "$tmp"
    cd "$tmp"
    result=$(go_dirs_testable "deleted/pkg/a.go")
    [ -z "$(echo "$result" | tr -d ' ')" ]
}

# ---------------------------------------------------------------------------
# Branch deletion / empty stdin
#
# `git push origin --delete <branch>` invokes the hook with either no ref
# lines at all or a line whose LOCAL sha ($2) is the zero SHA. Neither case
# has content to lint, and an invoker that leaves stdin open with no data
# must not wedge the hook on a blocking read.
# ---------------------------------------------------------------------------

ZERO="0000000000000000000000000000000000000000"

# A base whose diff against HEAD contains at least one .go file, so the
# affected-area gating actually has something to fire on. Walking history
# rather than assuming HEAD~1 keeps these tests independent of whatever the
# most recent commit happened to touch.
go_touching_base() {
    local c
    for c in $(git rev-list -40 HEAD); do
        if git diff --name-only "$c" HEAD 2>/dev/null | grep -q '\.go$'; then
            printf '%s\n' "$c"
            return 0
        fi
    done
    return 1
}

# Run the hook with stubbed `make`/`go` so no real build is triggered, and
# with a hard wall-clock cap so a blocking read FAILS the test instead of
# hanging the whole suite.
#
# Usage: run_hook_capped <seconds> <mode> [payload]
#   mode `closed` : stdin redirected from /dev/null (EOF immediately)
#   mode `open`   : stdin is a pipe held open by a writer that sends nothing
#   mode `lines`  : payload written to stdin, then EOF
run_hook_capped() {
    local cap="$1" mode="$2" payload="${3:-}"
    local stub="$BATS_TEST_TMPDIR/stub"
    mkdir -p "$stub"
    # Stubs record their invocation so tests can assert nothing ran.
    for c in make go; do
        cat > "$stub/$c" <<STUB
#!/bin/sh
echo "\$0 \$*" >> "$BATS_TEST_TMPDIR/invoked"
exit 0
STUB
        chmod +x "$stub/$c"
    done
    : > "$BATS_TEST_TMPDIR/invoked"

    # Never let the hook poison the real repo's SHA cache.
    local gitdir; gitdir="$(git rev-parse --git-dir)"
    local cache="$gitdir/pre-push-last-sha"
    local saved=""
    if [ -f "$cache" ]; then saved="$(cat "$cache")"; fi
    rm -f "$cache"

    HOOK_STATUS=""
    case "$mode" in
        closed)
            PATH="$stub:$PATH" "$HOOK" origin file:///dev/null </dev/null \
                >"$BATS_TEST_TMPDIR/out" 2>&1 &
            ;;
        open)
            local fifo="$BATS_TEST_TMPDIR/fifo"
            rm -f "$fifo"; mkfifo "$fifo"
            # Holds the write end open for longer than the cap without
            # ever writing a byte: exactly the hang condition.
            sh -c "sleep $((cap + 5))" > "$fifo" &
            HOLDER=$!
            PATH="$stub:$PATH" "$HOOK" origin file:///dev/null <"$fifo" \
                >"$BATS_TEST_TMPDIR/out" 2>&1 &
            ;;
        lines)
            printf '%s\n' "$payload" | PATH="$stub:$PATH" \
                "$HOOK" origin file:///dev/null \
                >"$BATS_TEST_TMPDIR/out" 2>&1 &
            ;;
    esac
    local hookpid=$!

    # Poll for completion up to the cap; kill and mark TIMEOUT past it.
    local waited=0
    while kill -0 "$hookpid" 2>/dev/null; do
        if [ "$waited" -ge "$cap" ]; then
            kill -9 "$hookpid" 2>/dev/null || true
            wait "$hookpid" 2>/dev/null || true
            HOOK_STATUS="TIMEOUT"
            break
        fi
        sleep 1
        waited=$((waited + 1))
    done
    if [ "$HOOK_STATUS" != "TIMEOUT" ]; then
        # A nonzero hook exit is a result to assert on, not a test error.
        HOOK_STATUS=0
        wait "$hookpid" || HOOK_STATUS=$?
    fi
    [ -n "${HOLDER:-}" ] && { kill "$HOLDER" 2>/dev/null || true; HOLDER=""; }

    HOOK_OUT="$(cat "$BATS_TEST_TMPDIR/out" 2>/dev/null || true)"
    HOOK_INVOKED="$(cat "$BATS_TEST_TMPDIR/invoked" 2>/dev/null || true)"

    # Restore the cache exactly as found.
    rm -f "$cache"
    if [ -n "$saved" ]; then printf '%s\n' "$saved" > "$cache"; fi
}

@test "deletion: stdin closed exits 0 without running linters" {
    run_hook_capped 20 closed
    [ "$HOOK_STATUS" = "0" ]
    [ -z "$HOOK_INVOKED" ]
}

@test "deletion: stdin open with no data exits 0 and does not hang" {
    # THE HANG. Without a cap this wedges the suite rather than failing.
    run_hook_capped 20 open
    [ "$HOOK_STATUS" != "TIMEOUT" ]
    [ "$HOOK_STATUS" = "0" ]
    [ -z "$HOOK_INVOKED" ]
}

@test "deletion: a single deletion line exits 0 without running linters" {
    # $4 on a deletion is the LIVE remote sha of the branch being removed,
    # not the zero SHA. Use a sha that differs from HEAD so an unfixed hook
    # produces a non-empty diff and actually invokes the linters — pinning
    # $4 to HEAD would mask the defect behind an empty `git diff`.
    run_hook_capped 60 lines "(delete) $ZERO refs/heads/gone $(go_touching_base)"
    [ "$HOOK_STATUS" != "TIMEOUT" ]
    [ "$HOOK_STATUS" = "0" ]
    [ -z "$HOOK_INVOKED" ]
}

@test "deletion: mixed deletion + real push still lints the real ref" {
    # An over-broad "any deletion present -> skip everything" fix would
    # silently drop linting for the legitimate ref in the same push.
    base="$(go_touching_base)"
    [ -n "$base" ]
    run_hook_capped 60 lines "(delete) $ZERO refs/heads/gone $(git rev-parse HEAD)
refs/heads/work $(git rev-parse HEAD) refs/heads/work $base"
    [ "$HOOK_STATUS" != "TIMEOUT" ]
    [ -n "$HOOK_INVOKED" ]
}

@test "deletion: a normal push line still runs affected linters" {
    base="$(go_touching_base)"
    [ -n "$base" ]
    run_hook_capped 60 lines "refs/heads/work $(git rev-parse HEAD) refs/heads/work $base"
    [ "$HOOK_STATUS" != "TIMEOUT" ]
    [ -n "$HOOK_INVOKED" ]
}

@test "deletion: protected-branch refusal still fires for a real push" {
    run_hook_capped 30 lines "refs/heads/main $(git rev-parse HEAD) refs/heads/main $(git rev-parse HEAD~1)"
    [ "$HOOK_STATUS" = "1" ]
    [[ "$HOOK_OUT" == *"refusing direct push"* ]]
}

@test "deletion: deleting main is still allowed past the protected check" {
    run_hook_capped 60 lines "(delete) $ZERO refs/heads/main $(go_touching_base)"
    [ "$HOOK_STATUS" = "0" ]
    [[ "$HOOK_OUT" != *"refusing direct push"* ]]
    [ -z "$HOOK_INVOKED" ]
}

@test "hook does not use a bare blocking cat for stdin" {
    # An unindented top-level `PUSH_INPUT=$(cat)` blocks forever when the
    # invoker leaves stdin open with no ref lines to send. The same call
    # indented inside the no-`read -t` fallback branch is fine.
    ! grep -qE '^PUSH_INPUT=\$\(cat\)[[:space:]]*$' "$HOOK"
    # And the guarded read must actually be present.
    grep -qE 'read -r -t' "$HOOK"
}

@test "hook short-circuits before computing a diff base" {
    # The deletion exit must happen before REMOTE_SHA / CHANGED work.
    early=$(grep -n 'ALL_DELETIONS\|NON_DELETIONS' "$HOOK" | head -1 | cut -d: -f1)
    base=$(grep -n '^CHANGED=' "$HOOK" | head -1 | cut -d: -f1)
    [ -n "$early" ]
    [ -n "$base" ]
    [ "$early" -lt "$base" ]
}
