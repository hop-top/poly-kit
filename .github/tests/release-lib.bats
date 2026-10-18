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
    [ "$(breaking "$(printf 'chore: squash\n\nBEGIN_NESTED_COMMIT\nfeat!: x\nEND_NESTED_COMMIT')")" = 1 ]
}

@test "parser: a trailing footer whose token ends in ! is a breaking commit" {
    [ "$(breaking "$(printf 'fix: a\n\nwip!: b')")" = 1 ]
    [ "$(breaking "$(printf 'fix: a\nfeat!: b')")" = 1 ]
    [ "$(breaking "$(printf 'fix: a\n\nRefs!: 12')")" = 1 ]
    [ "$(breaking "$(printf 'fix: a\n\nbody\n\nfeat(x)!: b')")" = 1 ]
    [ "$(breaking "$(printf 'fix: a\n\nRefs: 1\nwip!: b')")" = 1 ]
    [ "$(breaking "$(printf 'fix: a\n\nfeat!: b\n  continued\nRefs: 1')")" = 1 ]
    [ "$(breaking "$(printf 'fix: a\n\nBREAKING-CHANGES: y\nwip!: b')")" = 1 ]
    [ "$(breaking 'fix! #12')" = 1 ]
    # Not trailing footers, or nothing after the colon.
    [ "$(breaking "$(printf 'fix: a\n\nfeat!: b\n\nmore body')")" = 0 ]
    [ "$(breaking "$(printf 'fix: a\n\nsome body\nwip!: b\nmore')")" = 0 ]
    [ "$(breaking "$(printf 'fix: a\n\nwip!:')")" = 0 ]
}

@test "parser: BREAKING CHANGE right after a closed scope is the ! marker" {
    [ "$(breaking 'feat(api)BREAKING CHANGE: drop v1')" = 1 ]
    [ "$(breaking 'feat(api)BREAKING-CHANGE: drop v1')" = 1 ]
    [ "$(breaking 'feat(api)BREAKING CHANGE #3')" = 1 ]
    [ "$(breaking 'feat(api)BREAKING CHANGE:')" = 0 ]
    [ "$(breaking "$(printf 'fix: a\n\nRefs(x)BREAKING CHANGE: z')")" = 1 ]
    [ "$(breaking "$(printf 'fix: a\n\nRefs(x) #BREAKING CHANGE: z')")" = 1 ]
    # A trailing footer marks both commits; behind " #" only the footer's.
    lib "rp_parse_commits \"\$(printf 'fix: a\n\nRefs(x)BREAKING CHANGE: z')\" | LC_ALL=C sort"
    [ "$output" = "$(printf 'Refs\tx\t1\t1\t0\nfix\t\t1\t0\t0')" ]
    lib "rp_parse_commits \"\$(printf 'fix: a\n\nRefs(x) #BREAKING CHANGE: z')\" | LC_ALL=C sort"
    [ "$output" = "$(printf 'Refs\tx\t1\t1\t0\nfix\t\t0\t0\t0')" ]
    # Without a scope it is no marker: the header does not parse, or the
    # type takes it.
    [ "$(breaking 'fixBREAKING CHANGE: x')" = 0 ]
    [ "$(breaking 'fix BREAKING CHANGE: x')" = 0 ]
    lib "rp_parse_commits 'fixBREAKING CHANGE: x'; rp_parse_commits 'fix BREAKING CHANGE: x'"
    [ "$output" = "" ]
    lib "rp_parse_commits 'fixBREAKING-CHANGE: x'"
    [ "$output" = "$(printf 'fixBREAKING-CHANGE\t\t0\t0\t0')" ]
}

@test "parser: the last breaking marker needs text; footers are not body" {
    [ "$(breaking "$(printf 'fix: x\n\nBREAKING CHANGE:\nmore text')")" = 1 ]
    [ "$(breaking "$(printf 'fix: x\n\nBREAKING CHANGE #3')")" = 1 ]
    [ "$(breaking "$(printf 'fix: a\n\nfoo: bar\n  BREAKING CHANGE: x')")" = 1 ]
    [ "$(breaking "$(printf 'fix: x\n\nBREAKING CHANGE:\nRefs: 12')")" = 0 ]
    [ "$(breaking "$(printf 'feat!: x\n\nBREAKING CHANGE:')")" = 0 ]
    [ "$(breaking "$(printf 'fix: x\n\nBREAKING CHANGE: y\n   \nRefs: 1')")" = 0 ]
    [ "$(breaking "$(printf 'fix: a\n\nnote: BREAKING-CHANGE: capped')")" = 0 ]
    [ "$(breaking "$(printf 'fix: a\n\nBREAKING CHANGES: y')")" = 0 ]
}

@test "parser: a scope left open drops the message; leading blanks are trimmed" {
    [ "$(breaking "$(printf 'feat!: a\n\nfoo(bar')")" = 0 ]
    [ "$(breaking "$(printf 'feat!: a\n\nsee foo(bar')")" = 1 ]
    [ "$(breaking '  feat!: lead')" = 1 ]
}

@test "parser: one record per commit, footers included" {
    lib "rp_parse_commits \"\$(printf 'feat(x)!: a\n\nRefs!: 12\nRelease-As: 1.2.3')\" | LC_ALL=C sort"
    [ "$output" = "$(printf 'Refs\t\t1\t1\t0\nRelease-As\t\t0\t0\t0\nfeat\tx\t1\t1\t1')" ]
}

# visible <message> [config]: prints 1 when rp_visible accepts it for package p.
visible() {
    local cfg='{"packages": {"p": {}}}'
    [ $# -lt 2 ] || cfg="$2"
    bash -c '. "$1" && if rp_visible "$3" p "$2"; then echo 1; else echo 0; fi' _ \
        "$SCRIPTS/release-lib.sh" "$1" "$cfg"
}

@test "visible: what puts a line in the changelog, as release-please writes it" {
    [ "$(visible 'fix(q): x')" = 1 ]
    [ "$(visible 'Feat: x')" = 1 ]
    [ "$(visible 'chore(q)!: x')" = 1 ]
    [ "$(visible 'ci(q)BREAKING CHANGE:')" = 1 ]
    [ "$(visible "$(printf 'ci: a\n\nRefs(x) #BREAKING CHANGE: z')")" = 1 ]
    [ "$(visible "$(printf 'ci: a\n\nBREAKING CHANGE: b')")" = 1 ]
    [ "$(visible "$(printf 'chore: x\n\nRelease-As: 1.2.3')")" = 1 ]
    [ "$(visible "$(printf 'test(x): y\n\nfix: real')")" = 1 ]
    [ "$(visible 'chore(q): tidy')" = 0 ]
    [ "$(visible 'docs: x')" = 0 ]
    [ "$(visible 'wip: x')" = 0 ]
    [ "$(visible "$(printf 'chore: x\n\nRelease-As: nope')")" = 0 ]
}

@test "visible: changelog-sections decide, the first entry for a type winning" {
    cfg='{"changelog-sections": [{"type": "docs", "section": "Docs"},
        {"type": "chore", "section": "Misc", "hidden": true},
        {"type": "chore", "scope": "deps", "section": "Deps"}], "packages": {"p": {}}}'
    [ "$(visible 'docs: x' "$cfg")" = 1 ]
    [ "$(visible 'fix: x' "$cfg")" = 0 ]
    [ "$(visible 'chore(deps): y' "$cfg")" = 0 ]
    cfg='{"packages": {"p": {"changelog-sections": [{"type": "ci", "section": "CI"}]}}}'
    [ "$(visible 'ci: x' "$cfg")" = 1 ]
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
