#!/usr/bin/env bats
# Tests for scripts/lint-internal-refs.sh.
#
# Each test builds a throwaway git repo holding a copy of the script and
# one tracked file, then asserts the exit code. Forbidden strings are
# assembled at runtime so this file never trips the lint itself.
#
# Run: bats .github/tests/lint-internal-refs.bats
# Or:  make test-lint-scripts

SCRIPT="$BATS_TEST_DIRNAME/../../scripts/lint-internal-refs.sh"

setup() {
    REPO="$(mktemp -d)"
    git -C "$REPO" init -q
    git -C "$REPO" config user.email t@example.com
    git -C "$REPO" config user.name t
    mkdir -p "$REPO/scripts"
    cp "$SCRIPT" "$REPO/scripts/"
    printf 'clean\n' > "$REPO/README.md"
    git -C "$REPO" add -A
}

teardown() {
    rm -rf "$REPO"
}

# write_tracked <path> <content>: write a file and stage it.
write_tracked() {
    mkdir -p "$(dirname "$REPO/$1")"
    printf '%s\n' "$2" > "$REPO/$1"
    git -C "$REPO" add -A
}

run_lint() {
    run bash -c "cd '$REPO' && scripts/lint-internal-refs.sh"
}

@test "clean tree passes, including the script itself" {
    run_lint
    [ "$status" -eq 0 ]
    [[ "$output" == *"No references"* ]]
}

@test "out-of-repo conventions doc fails" {
    write_tracked go/x.go "// see cli-""conventions §3.5"
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *"go/x.go:1:"* ]]
}

@test "maintainer workspace path fails" {
    write_tracked docs/a.md "see ~/.o""ps/docs/glossary.md"
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *"docs/a.md:1:"* ]]
}

@test "design-note section fails" {
    write_tracked go/y.go "// sizing per design-""note §4"
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *"go/y.go:1:"* ]]
}

@test "decision record number split across lines fails" {
    write_tracked go/z.go "$(printf '// distinct (A''DR\n// 0004): expired')"
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *"go/z.go:1:"* ]]
}

@test "real account in a home path fails" {
    write_tracked go/p_test.go "const cwd = \"/Users/""jdoe/src/tool\""
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *"go/p_test.go:1:"* ]]
    [[ "$output" == *"placeholder user"* ]]
}

@test "placeholder accounts in home paths pass" {
    write_tracked go/p_test.go "$(printf '%s\n' \
        '"/Users/alice/src/tool"' '"/home/testuser/.config"' 'HOME=/home/me')"
    run_lint
    [ "$status" -eq 0 ]
}

@test "nested /x/home/y is not a home directory" {
    write_tracked sdk/fixture.json '{"p": "/test/home/project/data.json"}'
    run_lint
    [ "$status" -eq 0 ]
}

@test "CHANGELOG.md is out of scope" {
    write_tracked CHANGELOG.md "- fix: cite cli-""conventions §8"
    run_lint
    [ "$status" -eq 0 ]
}

@test "untracked files are out of scope" {
    printf '%s\n' "cli-""conventions" > "$REPO/scratch.txt"
    run_lint
    [ "$status" -eq 0 ]
}

@test "bare spec section fails" {
    write_tracked go/s.go "// retain floor (spe""c §3 #2)"
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *"go/s.go:1:"* ]]
    [[ "$output" == *"name no document"* ]]
}

@test "bare contract section fails" {
    write_tracked go/c.go "// second signal aborts the drain (contrac""t §\"Signals\")"
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *"go/c.go:1:"* ]]
}

@test "bare decision numbers fail, with or without #" {
    write_tracked go/d.go "$(printf '%s\n' "// live heads retained (decisio""n #10)" \
        "// anonymous branches (spec model, decisio""n 1)")"
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *"go/d.go:1:"* ]]
    [[ "$output" == *"go/d.go:2:"* ]]
}

@test "a file name in the path does not excuse a bare citation" {
    write_tracked docs/guide.md "Heads are retained (spe""c §3)."
    run_lint
    [ "$status" -eq 1 ]
    [[ "$output" == *"docs/guide.md:1:"* ]]
}

@test "citations that name an in-repo file pass" {
    write_tracked go/ok.go "$(printf '%s\n' \
        '// wired per serve-lifecycle.md §"Middleware"' \
        "// drain rule (serve-lifecycle.md contrac""t §\"Signals\")" \
        "// payload bound (kit-init-pr-wiring.md spe""c §2)" \
        "// retained per decisio""n #4 in docs/decisions.md" \
        '// token list per RFC 7519 §4.1.3')"
    write_tracked docs/x.md \
        "- [serve lifecycle contrac""t §\"The rpc service\"](contracts/serve-lifecycle.md#the-rpc-service)"
    run_lint
    [ "$status" -eq 0 ]
}
