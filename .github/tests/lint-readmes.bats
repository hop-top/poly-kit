#!/usr/bin/env bats
# Tests for scripts/lint-readmes.
#
# Each test builds a throwaway tree and runs the script against it with
# --root, then asserts the exit code.
#
# Run: bats .github/tests/lint-readmes.bats
# Or:  make test-lint-scripts

SCRIPT="$BATS_TEST_DIRNAME/../../scripts/lint-readmes"

setup() {
    TREE="$(mktemp -d)"
}

teardown() {
    rm -rf "$TREE"
}

# A skill directory is entered through its SKILL.md; its snippet files are
# example code, not a package, so they owe no directory README.
@test "snippet source files under skills/ need no README" {
    mkdir -p "$TREE/skills/x/assets/snippets" "$TREE/lib"
    printf '# root\n' > "$TREE/README.md"
    # one documented package keeps the script's empty-walk self-test satisfied
    printf '# lib\n' > "$TREE/lib/README.md"
    printf 'package lib\n' > "$TREE/lib/lib.go"
    printf 'print("hi")\n' > "$TREE/skills/x/assets/snippets/a.py"
    run "$SCRIPT" --root "$TREE"
    [ "$status" -eq 0 ]
}
