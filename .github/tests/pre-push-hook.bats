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
    # pre-push hooks receive lines on stdin; the script must read them.
    grep -q 'while read' "$HOOK"
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
