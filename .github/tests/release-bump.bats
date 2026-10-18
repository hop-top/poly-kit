#!/usr/bin/env bats
# Tests for scripts/check-release-bump.sh: the stale, breaking and linked
# rules, alone and after scripts/release-as.sh. qmochi versions alone; kit and
# kit-py form the linked group kit. The fixture is in release-fixture.bash.
#
# `[[ ]]` does not trip errexit under bash 3.2 (macOS /bin/bash), so a
# `[[ ]]` that is not a test's last command ends in `|| false`.
#
# Run: bats .github/tests/release-bump.bats
# Or:  make test-release

load release-fixture

QM=incubator/qmochi

# at <kit> <kit-py> [qmochi]: move next's manifest there and tag each version.
at() {
    set_manifest "$1" "$2" "${3:-0.2.0-alpha.1}"
    git -C "$REPO" commit -qam "chore(release): reseed"
    git -C "$REPO" tag -f "kit/v$1" >/dev/null
    git -C "$REPO" tag -f "kit-py/v$2" >/dev/null
    git -C "$REPO" tag -f "qmochi/v${3:-0.2.0-alpha.1}" >/dev/null
}

# --- breaking ---------------------------------------------------------------

@test "breaking: no new minor line fails, naming the commits and the pin" {
    land "fix(q)!: drop the v1 renderer" "$QM/q.go"
    sha="$(git -C "$REPO" rev-parse --short HEAD)"
    land "feat(q): add a chart" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 0.2.0-alpha.2)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"qmochi: 0.2.0-alpha.1 -> 0.2.0-alpha.2 keeps the line"* ]] || false
    [[ "$output" == *"$sha fix(q)!: drop the v1 renderer"* ]] || false
    [[ "$output" != *"add a chart"* ]] || false
    [[ "$output" == *"scripts/release-as.sh qmochi 0.3.0-alpha.0"* ]] || false
    [[ "$output" == *"off an up-to-date next"* ]]
}

@test "breaking: footers and headers count as release-please parses them" {
    land "$(printf 'feat(q): a\n\nBREAKING CHANGE: Stop takes a deadline')" "$QM/q.go"
    land "$(printf 'fix(q): b\n\nshutdown is capped, BREAKING-CHANGE: see the docs')" "$QM/q.go"
    land 'feat!:no space' "$QM/q.go"
    land "$(printf 'fix(q): c\n\nwip!: renderer API')" "$QM/q.go"
    land "$(printf 'docs(q): d\n\nno BREAKING CHANGE: here, mid-line')" "$QM/q.go"
    # A footer: its value is not body text.
    land "$(printf 'fix(q): e\n\nnote: BREAKING-CHANGE: in a footer')" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 0.2.0-alpha.2)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"4 breaking commit(s)"* ]] || false
    [[ "$output" == *"fix(q): c"* ]] || false
    [[ "$output" != *"docs(q): d"* ]] || false
    [[ "$output" != *"fix(q): e"* ]]
}

@test "breaking: x.(y+1).0-alpha.0 or its stable form passes" {
    land "fix(q)!: drop the v1 renderer" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 0.3.0-alpha.0)"
    check "$pr"
    [ "$status" -eq 0 ]
    [[ "$output" == *"qmochi: 0.2.0-alpha.1 -> 0.3.0-alpha.0 starts a new line"* ]] || false
    pr="$(release_pr qmochi "$QM" 0.3.0)"
    check "$pr"
    [ "$status" -eq 0 ]
}

@test "breaking: patch and prerelease counter must be 0, channel alpha" {
    at 0.5.0-alpha.26 0.5.0-alpha.5 0.2.1-alpha.3
    land "fix(q)!: drop the v1 renderer" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 0.3.0-alpha.3)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"scripts/release-as.sh qmochi 0.3.0-alpha.0"* ]] || false
    pr="$(release_pr qmochi "$QM" 0.3.0-beta.0)"
    check "$pr"
    [ "$status" -eq 1 ]
    pr="$(release_pr qmochi "$QM" 0.3.1-alpha.0)"
    check "$pr"
    [ "$status" -eq 1 ]
}

@test "breaking: non-breaking changes keep the counter" {
    land "feat(q): add a chart" "$QM/q.go"
    land "fix(q): align the chart" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 0.2.0-alpha.2)"
    check "$pr"
    [ "$status" -eq 0 ]
    [[ "$output" == *"no breaking change since qmochi/v0.2.0-alpha.1"* ]]
}

@test "breaking: commits before the last tag do not count" {
    land "fix(q)!: drop the v1 renderer" "$QM/q.go"
    released qmochi "$QM" 0.3.0-alpha.0
    land "fix(q): align the chart" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 0.3.0-alpha.1)"
    check "$pr"
    [ "$status" -eq 0 ]
    [[ "$output" == *"no breaking change since qmochi/v0.3.0-alpha.0"* ]]
}

@test "breaking: a commit routed to other packages does not count" {
    land "feat(sdk-py)!: drop python 3.9" sdk/py/kit.py
    land "build(deps)!: bump the MCP SDK" extensions/mcp-tasks/t.go
    land "fix(q)!: wrong package" "$QM/q.go"
    pr="$(release_pr kit . 0.5.0-alpha.27)"
    check "$pr"
    [ "$status" -eq 0 ]
    [[ "$output" == *"kit: 0.5.0-alpha.26 -> 0.5.0-alpha.27, no breaking change"* ]]
}

@test "breaking: from 1.0 a breaking change needs a new major" {
    at 0.5.0-alpha.26 0.5.0-alpha.5 1.2.0
    land "feat(q)!: new config format" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 1.3.0-alpha.0)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"scripts/release-as.sh qmochi 2.0.0-alpha.0"* ]] || false
    pr="$(release_pr qmochi "$QM" 2.0.0-alpha.0)"
    check "$pr"
    [ "$status" -eq 0 ]
}

@test "merge base: a sibling release merged meanwhile is not compared" {
    land "fix(q): align the chart" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 0.2.0-alpha.2)"
    land "fix(sdk-py): retry" sdk/py/kit.py
    released kit-py sdk/py 0.5.0-alpha.6
    check "$pr"
    [ "$status" -eq 0 ]
    [[ "$output" == *"qmochi: 0.2.0-alpha.1 -> 0.2.0-alpha.2"* ]] || false
    [[ "$output" != *"kit-py"* ]]
}

# --- stale --------------------------------------------------------------------

@test "stale: a proposed version that is already tagged fails" {
    git -C "$REPO" tag qmochi/v0.2.0-alpha.2
    land "fix(q): align the chart" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 0.2.0-alpha.2)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"qmochi/v0.2.0-alpha.2 already exists"* ]] || false
    [[ "$output" == *"scripts/release-as.sh --cleanup"* ]]
}

@test "stale: a pin not newer than the current version fails" {
    perl -0pi -e 's/("component": "qmochi",\n)/$1      "release-as": "0.2.0-alpha.1",\n/' "$REPO/$CONFIG_FILE"
    git -C "$REPO" commit -qam "chore(release): left-behind pin"
    land "fix(q): align the chart" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 0.2.0-alpha.2)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"release-as pin 0.2.0-alpha.1 is not newer than 0.2.0-alpha.1"* ]]
}

@test "stale: a pin left after its release is caught twice over" {
    land "fix(q)!: drop the v1 renderer" "$QM/q.go"
    run bash -c "cd '$REPO' && '$RELEASE_AS' qmochi 0.3.0-alpha.0"
    [ "$status" -eq 0 ]
    released qmochi "$QM" 0.3.0-alpha.0
    land "fix(q): align the chart" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 0.3.0-alpha.0)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"qmochi/v0.3.0-alpha.0 already exists; its release-as pin 0.3.0-alpha.0 is not newer"* ]]
}

# --- linked -------------------------------------------------------------------

@test "linked: a pre-release minor for one member fails and names --group" {
    land "fix(cli)!: reserve the peer tree" cli/cli.go
    pr="$(release_pr kit . 0.6.0-alpha.0)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"linked release of group kit"* ]] || false
    [[ "$output" == *"kit-py does not here"* ]] || false
    [[ "$output" == *"scripts/release-as.sh --group kit 0.6.0-alpha.0"* ]]
}

@test "linked: a member's breaking change without a new line asks for --group" {
    land "fix(cli)!: reserve the peer tree" cli/cli.go
    pr="$(release_pr kit . 0.5.0-alpha.27)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"keeps the line"* ]] || false
    [[ "$output" == *"scripts/release-as.sh --group kit 0.6.0-alpha.0"* ]]
}

@test "linked: the group PR passes, a member without commits included" {
    land "fix(cli)!: reserve the peer tree" cli/cli.go
    pr="$(group_pr kit . 0.6.0-alpha.0 sdk/py 0.6.0-alpha.0)"
    check "$pr"
    [ "$status" -eq 0 ]
    [[ "$output" == *"kit: 0.5.0-alpha.26 -> 0.6.0-alpha.0 starts a new line"* ]] || false
    [[ "$output" == *"kit-py: 0.5.0-alpha.5 -> 0.6.0-alpha.0, no breaking change"* ]] || false
    [[ "$output" == *"linked release of group kit; every member at 0.6.0-alpha.0"* ]]
}

@test "linked: a group PR with a member on another version fails" {
    land "fix(cli)!: reserve the peer tree" cli/cli.go
    pr="$(group_pr kit . 0.6.0-alpha.0 sdk/py 0.5.0-alpha.6)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"kit-py does not here"* ]]
}

@test "linked: a new major in alpha needs the whole group" {
    at 1.2.0 1.2.0
    land "feat(cli)!: new config format" cli/cli.go
    pr="$(release_pr kit . 2.0.0-alpha.0)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"scripts/release-as.sh --group kit 2.0.0-alpha.0"* ]] || false
    pr="$(group_pr kit . 2.0.0-alpha.0 sdk/py 2.0.0-alpha.0)"
    check "$pr"
    [ "$status" -eq 0 ]
}

@test "linked: a new stable major needs the whole group" {
    at 1.2.0 1.2.0
    land "feat(cli)!: new config format" cli/cli.go
    pr="$(release_pr kit . 2.0.0)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"scripts/release-as.sh --group kit 2.0.0"* ]] || false
    pr="$(group_pr kit . 2.0.0 sdk/py 2.0.0)"
    check "$pr"
    [ "$status" -eq 0 ]
}

@test "linked: a pre-release minor without a breaking change needs the group" {
    at 0.6.0 0.6.0
    land "feat(cli): add a flag" cli/cli.go
    pr="$(release_pr kit . 0.7.0-alpha.0)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"scripts/release-as.sh --group kit 0.7.0-alpha.0"* ]]
}

@test "linked: counters, patches and stable minors version each member alone" {
    land "feat(cli): add a flag" cli/cli.go
    pr="$(release_pr kit . 0.5.0-alpha.27)"
    check "$pr"
    [ "$status" -eq 0 ]
    at 0.6.0 0.6.3
    land "fix(sdk-py): retry" sdk/py/kit.py
    pr="$(release_pr kit-py sdk/py 0.6.4-alpha.0)"
    check "$pr"
    [ "$status" -eq 0 ]
    pr="$(release_pr kit-py sdk/py 0.7.0)"
    check "$pr"
    [ "$status" -eq 0 ]
    [[ "$output" != *"linked release"* ]]
}

@test "linked: the fix takes the highest line across the group" {
    at 0.5.0-alpha.26 0.6.0-alpha.1
    land "fix(cli)!: reserve the peer tree" cli/cli.go
    pr="$(release_pr kit . 0.5.0-alpha.27)"
    check "$pr"
    [ "$status" -eq 1 ]
    [[ "$output" == *"scripts/release-as.sh --group kit 0.7.0-alpha.0"* ]]
}

# --- after release-as ---------------------------------------------------------

@test "after release-as: a pinned component and a pinned group pass" {
    land "fix(q)!: drop the v1 renderer" "$QM/q.go"
    land "fix(cli)!: reserve the peer tree" cli/cli.go
    run bash -c "cd '$REPO' && '$RELEASE_AS' qmochi 0.3.0-alpha.0 && '$RELEASE_AS' --group kit 0.6.0-alpha.0"
    [ "$status" -eq 0 ]
    pr="$(release_pr qmochi "$QM" 0.3.0-alpha.0)"
    check "$pr"
    [ "$status" -eq 0 ]
    pr="$(group_pr kit . 0.6.0-alpha.0 sdk/py 0.6.0-alpha.0)"
    check "$pr"
    [ "$status" -eq 0 ]
}

# --- plumbing -----------------------------------------------------------------

@test "plumbing: a missing tag is an error, not a pass" {
    land "fix(q)!: drop the v1 renderer" "$QM/q.go"
    git -C "$REPO" tag -d qmochi/v0.2.0-alpha.1 >/dev/null
    pr="$(release_pr qmochi "$QM" 0.2.0-alpha.2)"
    check "$pr"
    [ "$status" -eq 2 ]
    [[ "$output" == *"tag qmochi/v0.2.0-alpha.1"* ]]
}

@test "plumbing: no manifest change, nothing to check" {
    land "fix(q)!: drop the v1 renderer" "$QM/q.go"
    check next
    [ "$status" -eq 0 ]
    [[ "$output" == *"nothing to check"* ]]
}

@test "plumbing: annotates failures in GitHub Actions" {
    land "fix(q)!: drop the v1 renderer" "$QM/q.go"
    pr="$(release_pr qmochi "$QM" 0.2.0-alpha.2)"
    run bash -c "cd '$REPO' && GITHUB_ACTIONS=true '$CHECK' next '$pr'"
    [ "$status" -eq 1 ]
    [[ "$output" == *"::error title=Breaking change needs a new minor line::qmochi 0.2.0-alpha.2"* ]]
}

@test "plumbing: config and manifest paths come from --config and --manifest" {
    mkdir -p "$REPO/rp"
    git -C "$REPO" mv "$CONFIG_FILE" rp/config.json
    git -C "$REPO" mv "$MANIFEST_FILE" rp/versions.json
    git -C "$REPO" commit -qm "chore: move release-please files"
    land "fix(q)!: drop the v1 renderer" "$QM/q.go"
    git -C "$REPO" checkout -q -b release-please--branches--next--components--qmochi
    jq '.["incubator/qmochi"] = "0.2.0-alpha.2"' "$REPO/rp/versions.json" > "$REPO/rp/v.tmp"
    mv "$REPO/rp/v.tmp" "$REPO/rp/versions.json"
    git -C "$REPO" commit -qam "chore(release): qmochi 0.2.0-alpha.2"
    git -C "$REPO" checkout -q next
    run bash -c "cd '$REPO' && '$CHECK' --config rp/config.json --manifest rp/versions.json next release-please--branches--next--components--qmochi"
    [ "$status" -eq 1 ]
    [[ "$output" == *"scripts/release-as.sh --config rp/config.json --manifest rp/versions.json qmochi 0.3.0-alpha.0"* ]] || false
    run bash -c "cd '$REPO' && '$RELEASE_AS' --config rp/config.json --manifest rp/versions.json qmochi 0.3.0-alpha.0"
    [ "$status" -eq 0 ]
    [ "$(jq -r '.packages["incubator/qmochi"]["release-as"]' "$REPO/rp/config.json")" = 0.3.0-alpha.0 ]
}
