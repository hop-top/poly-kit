#!/usr/bin/env bats
# Tests for scripts/release-lib.sh: commit routing, the breaking-change parser
# (each expectation is what release-please 17.6.0's own parser returns for the
# message), semver precedence and the release-line rules. The fixture is in
# release-fixture.bash.
#
# Run: bats .github/tests/release-lib.bats
# Or:  make test-release

load release-fixture

lib() {
    run bash -c ". '$SCRIPTS/release-lib.sh' && $1"
}

route() {
    run bash -c "cd '$REPO' && . '$SCRIPTS/release-lib.sh' && rp_route \"\$(cat $CONFIG_FILE)\" '$1' | paste -sd' ' -"
}

# breaking <message>: prints 1 when rp_is_breaking accepts it, else 0.
breaking() {
    bash -c '. "$1" && if rp_is_breaking "$2"; then echo 1; else echo 0; fi' _ \
        "$SCRIPTS/release-lib.sh" "$1"
}

@test "routing: a top-level file reaches the root package alone" {
    route '["RELEASING.md"]'
    [ "$output" = "." ]
}

@test "routing: an excluded package path drops the root package" {
    route '["sdk/py/kit.py"]'
    [ "$output" = "sdk/py" ]
    route '["sdk/py/kit.py", "cli/cli.go"]'
    [ "$output" = ". sdk/py" ]
    route '["extensions/mcp-tasks/t.go"]'
    [ "$output" = "extensions/mcp-tasks" ]
}

@test "routing: a path the root does not exclude reaches both" {
    lib "rp_route '{\"packages\": {\".\": {}, \"ext/a\": {}}}' '[\"ext/a/x.go\"]' | paste -sd' ' -"
    [ "$output" = ". ext/a" ]
}

@test "routing: the longest package path takes a file" {
    lib "rp_route '{\"packages\": {\"sdk\": {}, \"sdk/py\": {}}}' '[\"sdk/py/kit.py\"]'"
    [ "$output" = "sdk/py" ]
}

@test "routing: an empty commit skips every package with exclude-paths" {
    route '[]'
    [ "$output" = "extensions/mcp-tasks incubator/qmochi sdk/py" ]
}

@test "parser: headers" {
    [ "$(breaking 'feat!: x')" = 1 ]
    [ "$(breaking 'feat!:x')" = 1 ]
    [ "$(breaking 'feat(sc ope)!: x')" = 1 ]
    [ "$(breaking 'Feat!: x')" = 1 ]
    [ "$(breaking '1feat!: x')" = 1 ]
    [ "$(breaking 'feat_x!: y')" = 1 ]
    [ "$(breaking 'build(deps)!: bump')" = 1 ]
    [ "$(breaking 'feat: x')" = 0 ]
    [ "$(breaking 'feat !: x')" = 0 ]
    [ "$(breaking 'feat()!: x')" = 0 ]
    [ "$(breaking 'feat(a)b)!: x')" = 0 ]
    [ "$(breaking 'feat!: ')" = 0 ]
    [ "$(breaking 'fixup! feat!: x')" = 0 ]
    [ "$(breaking 'Revert "feat!: x"')" = 0 ]
}

@test "parser: BREAKING CHANGE starts a body line, with text" {
    [ "$(breaking "$(printf 'fix: x\n\nBREAKING CHANGE: gone')")" = 1 ]
    [ "$(breaking "$(printf 'fix: x\n\nBREAKING CHANGE:gone')")" = 1 ]
    [ "$(breaking "$(printf 'fix: x\n\nsome text\nBREAKING CHANGE: y')")" = 1 ]
    [ "$(breaking "$(printf 'fix: x\r\n\r\nBREAKING CHANGE: y')")" = 1 ]
    [ "$(breaking "$(printf 'fix: x\n\nthis mentions BREAKING CHANGE: inline')")" = 0 ]
    [ "$(breaking "$(printf 'fix: x\n\n  BREAKING CHANGE: gone')")" = 0 ]
    [ "$(breaking "$(printf 'fix: x\n\nbreaking change: gone')")" = 0 ]
    [ "$(breaking "$(printf 'fix: x\n\nBREAKING CHANGE:')")" = 0 ]
    [ "$(breaking 'fix: BREAKING CHANGE: x')" = 0 ]
}

@test "parser: BREAKING-CHANGE anywhere in a body line, with text after" {
    [ "$(breaking "$(printf 'fix: x\n\nthis mentions BREAKING-CHANGE: inline')")" = 1 ]
    [ "$(breaking "$(printf 'fix: x\n\nBREAKING-CHANGE:\nnext line')")" = 1 ]
    [ "$(breaking "$(printf 'fix: x\n\nBREAKING-CHANGE:   ')")" = 0 ]
}

@test "parser: a paragraph opening with a type is its own commit" {
    [ "$(breaking "$(printf 'Some title\n\nfeat(x): y\n\nBREAKING CHANGE: z')")" = 1 ]
    [ "$(breaking "$(printf 'Some title\n\nfeat(x)!: y')")" = 0 ]
    [ "$(breaking "$(printf 'Merge branch x\n\nBREAKING CHANGE: y')")" = 0 ]
    [ "$(breaking "$(printf 'fix: x\n\n\nfeat: y\n\nBREAKING CHANGE: z')")" = 1 ]
}

@test "semver: precedence follows the spec" {
    lib "for p in '0.5.0-alpha.26 0.5.0-alpha.3' '0.6.0-alpha.0 0.5.0-alpha.26' \
                 '0.6.0 0.6.0-rc.1' '0.6.0-beta.0 0.6.0-alpha.9' \
                 '1.0.0-alpha.1 1.0.0-alpha' '1.0.0-alpha.beta 1.0.0-alpha.1'; do
            set -- \$p; printf '%s ' \"\$(rp_semver_cmp \$1 \$2)\" \"\$(rp_semver_cmp \$2 \$1)\"
        done; rp_semver_cmp 0.6.0-alpha.0 0.6.0-alpha.0"
    [ "$output" = "1 -1 1 -1 1 -1 1 -1 1 -1 1 -1 0" ]
    lib "rp_semver_max 0.5.0-alpha.27 0.6.0-alpha.0 0.6.0-alpha.0 0.5.1"
    [ "$output" = "0.6.0-alpha.0" ]
}

@test "new line: x.(y+1).0-alpha.0, a new major, or their stable form" {
    lib "for p in '0.5.0-alpha.26 0.6.0-alpha.0' '0.5.0-alpha.26 0.6.0' \
                 '0.5.0-alpha.26 1.0.0' '1.2.0 2.0.0-alpha.0' \
                 '0.5.1-alpha.3 0.6.0-alpha.3' '0.5.0-alpha.26 0.6.0-beta.0' \
                 '0.5.0-alpha.26 0.6.1-alpha.0' '0.5.0-alpha.26 0.5.0-alpha.27' \
                 '1.2.0 1.3.0-alpha.0' '1.2.0 2.1.0-alpha.0'; do
            set -- \$p; if rp_is_new_line \$1 \$2; then printf 1; else printf 0; fi
        done"
    [ "$output" = "1111000000" ]
    lib "rp_new_line 0.5.1-alpha.3; rp_new_line 1.2.0"
    [ "$output" = "$(printf '0.6.0-alpha.0\n2.0.0-alpha.0')" ]
}

@test "linked: a new major in any channel, or a new minor in pre-release" {
    lib "for p in '1.2.0 2.0.0-alpha.0' '1.2.0 2.0.0' '0.6.0 1.0.0' \
                 '0.5.0-alpha.26 0.6.0-alpha.0' '0.6.0 0.7.0-alpha.0' \
                 '0.6.3 0.7.0' '0.6.0 0.6.1-alpha.0' '0.5.0-alpha.26 0.5.0-alpha.27' \
                 '0.6.0-rc.3 0.6.0'; do
            set -- \$p; if rp_linked_applicable \$1 \$2; then printf 1; else printf 0; fi
        done"
    [ "$output" = "111110000" ]
}

@test "groups: members and membership come from x-linked-groups" {
    run bash -c "cd '$REPO' && . '$SCRIPTS/release-lib.sh' && c=\$(cat $CONFIG_FILE) &&
        rp_group_members \"\$c\" kit | paste -sd' ' - && rp_group_of \"\$c\" kit-py &&
        echo \"[\$(rp_group_of \"\$c\" qmochi)]\""
    [ "$output" = "$(printf 'kit kit-py\nkit\n[]')" ]
}
