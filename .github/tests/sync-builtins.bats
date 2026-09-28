#!/usr/bin/env bats
# Tests for scripts/sync-builtins.sh.
#
# Each test builds a throwaway tree with a templates/ directory, runs the
# script into internal/template/builtins, and checks the mirrored bytes.
#
# Run: bats .github/tests/sync-builtins.bats
# Or:  make test-lint-scripts

SCRIPT="$BATS_TEST_DIRNAME/../../scripts/sync-builtins.sh"

setup() {
    ROOT="$(mktemp -d)"
    mkdir -p "$ROOT/templates/shared/ci" "$ROOT/templates/cli-go" "$ROOT/docs"
}

teardown() {
    rm -rf "$ROOT"
}

# write <path> <content>: write a file under the throwaway root.
write() {
    mkdir -p "$(dirname "$ROOT/$1")"
    printf '%s\n' "$2" > "$ROOT/$1"
}

sync() {
    run bash -c "cd '$ROOT' && '$SCRIPT' internal/template/builtins $*"
}

mirrored() {
    cat "$ROOT/internal/template/builtins/$1"
}

@test "a link that leaves templates/ is rebased to resolve from the mirror" {
    write templates/shared/README.md '- [Blueprints](../../docs/b.md#api): see'
    sync shared
    [ "$status" -eq 0 ]
    [ "$(mirrored shared/README.md)" = '- [Blueprints](../../../../docs/b.md#api): see' ]
}

@test "a link to a non-mirrored templates/ file is rebased into templates/" {
    write templates/cli-go/README.md 'See [conform](../CONFORM.md).'
    sync cli-go
    [ "$status" -eq 0 ]
    [ "$(mirrored cli-go/README.md)" = 'See [conform](../../../../templates/CONFORM.md).' ]
}

@test "reference-style definitions are rebased too" {
    write templates/shared/ci/README.md '[guide]: ../../../docs/g.md'
    sync shared
    [ "$(mirrored shared/ci/README.md)" = '[guide]: ../../../../../docs/g.md' ]
}

@test "links inside the template, URLs, anchors and actions are unchanged" {
    body='[a](ci/README.md) [b](../shared/x.md) [c](https://e.com/../x) [d](#top) [e]({{.Module}}/x) [f](/abs.md)'
    write templates/shared/README.md "$body"
    sync shared
    [ "$(mirrored shared/README.md)" = "$body" ]
}

@test "non-markdown files are copied byte for byte" {
    write templates/shared/emit.sh 'echo "[x](../../docs/b.md)"'
    sync shared
    [ "$(mirrored shared/emit.sh)" = 'echo "[x](../../docs/b.md)"' ]
}

@test "only the named templates are mirrored, and stale files are removed" {
    write templates/shared/a.txt a
    write templates/cli-go/b.txt b
    mkdir -p "$ROOT/internal/template/builtins/shared"
    printf stale > "$ROOT/internal/template/builtins/shared/stale.txt"
    sync shared
    [ -f "$ROOT/internal/template/builtins/shared/a.txt" ]
    [ ! -e "$ROOT/internal/template/builtins/shared/stale.txt" ]
    [ ! -e "$ROOT/internal/template/builtins/cli-go" ]
}

@test "a second run produces the same tree" {
    write templates/shared/README.md '[b](../../docs/b.md)'
    sync shared
    cp -R "$ROOT/internal/template/builtins" "$ROOT/first"
    sync shared
    run diff -r "$ROOT/first" "$ROOT/internal/template/builtins"
    [ "$status" -eq 0 ]
}

@test "missing arguments exit 2" {
    run bash -c "cd '$ROOT' && '$SCRIPT' internal/template/builtins"
    [ "$status" -eq 2 ]
}
