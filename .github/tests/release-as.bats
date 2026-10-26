#!/usr/bin/env bats
# Tests for scripts/release-as.sh: pins, linked-group pins and the cleanup a
# release PR runs. The fixture is in release-fixture.bash.
#
# `[[ ]]` does not trip errexit under bash 3.2 (macOS /bin/bash), so a
# `[[ ]]` that is not a test's last command ends in `|| false`.
#
# Run: bats .github/tests/release-as.bats
# Or:  make test-release

load release-fixture

# changed_lines <rev-range>: the config's changed lines, +/- prefixed.
changed_lines() {
    git -C "$REPO" diff -U0 "$1" -- "$CONFIG_FILE" | grep '^[+-] ' || true
}

@test "pin: one release-as line after the component, config alone committed" {
    before="$(git -C "$REPO" rev-parse HEAD)"
    release_as qmochi 0.3.0-alpha.0
    [ "$status" -eq 0 ]
    [ "$(git -C "$REPO" rev-parse HEAD~1)" = "$before" ]
    [ "$(git -C "$REPO" show --format= --name-only HEAD)" = "$CONFIG_FILE" ]
    [ "$(git -C "$REPO" log -1 --format=%B)" = "chore(release): start qmochi 0.3.0-alpha.0" ]
    [ "$(changed_lines HEAD~1)" = '+      "release-as": "0.3.0-alpha.0",' ]
    grep -A1 '"component": "qmochi",' "$REPO/$CONFIG_FILE" | grep -q '"release-as": "0.3.0-alpha.0",'
    [ "$(jq -r '.packages["incubator/qmochi"]["release-as"]' "$REPO/$CONFIG_FILE")" = 0.3.0-alpha.0 ]
    [ -z "$(git -C "$REPO" status --porcelain)" ]
    [[ "$output" == *"release-pr --dry-run"* ]] || false
    [[ "$output" == *"--target-branch next"* ]] || false
    [[ "$output" == *'"chore(release): qmochi 0.3.0-alpha.0"'* ]]
}

@test "pin: a package without visible commits since its release is told it waits" {
    release_as qmochi 0.3.0-alpha.0
    [ "$status" -eq 0 ]
    [[ "$output" == *"no commit since qmochi/v0.2.0-alpha.1 puts a line in qmochi's changelog"* ]] || false
    # Hidden types, and commits release-please routes elsewhere, do not count.
    land "chore(q): tidy" incubator/qmochi/q.go
    land "feat(sdk-py): retry" sdk/py/kit.py
    release_as qmochi 0.4.0-alpha.0
    [ "$status" -eq 0 ]
    [[ "$output" == *"no commit since qmochi/v0.2.0-alpha.1"* ]] || false
    land "fix(q): align the chart" incubator/qmochi/q.go
    release_as qmochi 0.5.0-alpha.0
    [ "$status" -eq 0 ]
    [[ "$output" != *"no commit since"* ]]
}

@test "pin: a visible commit touching no file counts, as release-please routes it" {
    git -C "$REPO" commit -q --allow-empty -m "feat: everywhere"
    release_as qmochi 0.3.0-alpha.0
    [ "$status" -eq 0 ]
    [[ "$output" != *"no commit since"* ]]
}

@test "pin: the package the config file reaches gets a Release-As footer" {
    release_as kit 0.5.0-alpha.27
    [ "$status" -eq 0 ]
    [ "$(git -C "$REPO" log -1 --format=%B)" = "$(printf 'chore(release): start kit 0.5.0-alpha.27\n\nRelease-As: 0.5.0-alpha.27')" ]
    release_as kit-py 0.5.0-alpha.6
    [ "$status" -eq 0 ]
    [ "$(git -C "$REPO" log -1 --format=%B)" = "chore(release): start kit-py 0.5.0-alpha.6" ]
}

@test "pin: a stable version is a cut" {
    release_as mcp-tasks 0.2.0
    [ "$status" -eq 0 ]
    [ "$(git -C "$REPO" log -1 --format=%s)" = "chore(release): cut mcp-tasks 0.2.0" ]
}

@test "pin: a new pin replaces the old one in place" {
    release_as qmochi 0.3.0-alpha.0
    [ "$status" -eq 0 ]
    release_as qmochi 0.4.0-alpha.0
    [ "$status" -eq 0 ]
    [ "$(changed_lines HEAD~1)" = "$(printf '%s\n%s' '-      "release-as": "0.3.0-alpha.0",' '+      "release-as": "0.4.0-alpha.0",')" ]
}

@test "pin: refuses a version not newer than the manifest" {
    release_as qmochi 0.2.0-alpha.1
    [ "$status" -eq 1 ]
    [[ "$output" == *"not newer"* ]] || false
    release_as qmochi 0.1.9
    [ "$status" -eq 1 ]
    release_as qmochi 0.2.0-alpha.2
    [ "$status" -eq 0 ]
}

@test "pin: refuses the pin the package already carries, never an empty commit" {
    release_as qmochi 0.3.0-alpha.0
    [ "$status" -eq 0 ]
    head="$(git -C "$REPO" rev-parse HEAD)"
    release_as qmochi 0.3.0-alpha.0
    [ "$status" -eq 1 ]
    [[ "$output" == *"already pinned to 0.3.0-alpha.0"* ]] || false
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$head" ]
}

@test "pin: a linked member's linked release needs --group" {
    before="$(git -C "$REPO" rev-parse HEAD)"
    release_as kit 0.6.0-alpha.0
    [ "$status" -eq 1 ]
    [[ "$output" == *"linked release for group kit"* ]] || false
    [[ "$output" == *"scripts/release-as.sh --group kit 0.6.0-alpha.0"* ]] || false
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before" ]
    release_as kit 1.0.0
    [ "$status" -eq 1 ]
}

@test "pin: a linked member's counter or patch pins alone" {
    release_as kit 0.5.0-alpha.27
    [ "$status" -eq 0 ]
    release_as kit-py 0.5.1-alpha.0
    [ "$status" -eq 0 ]
    [ "$(jq -c '.plugins' "$REPO/$CONFIG_FILE")" = null ]
}

@test "group: pins every member and wires linked-versions on one line" {
    release_as --group kit 0.6.0-alpha.0
    [ "$status" -eq 0 ]
    [ "$(git -C "$REPO" show --format= --name-only HEAD)" = "$CONFIG_FILE" ]
    [ "$(git -C "$REPO" log -1 --format=%s)" = "chore(release): start group kit 0.6.0-alpha.0" ]
    [ "$(changed_lines HEAD~1)" = "$(printf '%s\n%s\n%s' \
        '+  "plugins": [{"type": "linked-versions", "groupName": "kit", "components": ["kit", "kit-py"]}],' \
        '+      "release-as": "0.6.0-alpha.0",' \
        '+      "release-as": "0.6.0-alpha.0",')" ]
    [ "$(jq -c '[.packages["."]["release-as"], .packages["sdk/py"]["release-as"]]' "$REPO/$CONFIG_FILE")" = '["0.6.0-alpha.0","0.6.0-alpha.0"]' ]
    [ "$(jq -r '.packages["incubator/qmochi"] | has("release-as")' "$REPO/$CONFIG_FILE")" = false ]
    [ "$(git -C "$REPO" log -1 --format=%B | sed "/^$/d" | tail -n1)" = "Release-As: 0.6.0-alpha.0" ]
    [ "$(git -C "$REPO" log -1 --format=%B | grep -c '^Release-As:')" -eq 1 ]
    [[ "$output" == *'"chore(<target-branch>): release kit libraries"'* ]]
}

@test "group: refuses a version not newer than any member, and unknown groups" {
    released kit-py sdk/py 0.6.0-alpha.0
    release_as --group kit 0.6.0-alpha.0
    [ "$status" -eq 1 ]
    [[ "$output" == *"kit-py is at 0.6.0-alpha.0"* ]] || false
    release_as --group nope 1.0.0
    [ "$status" -eq 1 ]
    [[ "$output" == *"has: kit"* ]]
}

@test "group: refuses a group already pinned" {
    release_as --group kit 0.6.0-alpha.0
    [ "$status" -eq 0 ]
    head="$(git -C "$REPO" rev-parse HEAD)"
    release_as --group kit 0.6.0-alpha.0
    [ "$status" -eq 1 ]
    [[ "$output" == *"already pinned"* ]] || false
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$head" ]
}

@test "cleanup: a release PR that cuts the pins restores the config byte for byte" {
    cp "$REPO/$CONFIG_FILE" "$REPO/config.orig"
    release_as --group kit 0.6.0-alpha.0
    release_as qmochi 0.3.0-alpha.0
    bump . 0.6.0-alpha.0 sdk/py 0.6.0-alpha.0 incubator/qmochi 0.3.0-alpha.0
    git -C "$REPO" commit -qam "chore(release): the release PR"
    release_as --cleanup
    [ "$status" -eq 0 ]
    [ "$(git -C "$REPO" log -1 --format=%s)" = "chore(release): drop release-as pins this release cuts" ]
    [[ "$(git -C "$REPO" log -1 --format=%b)" == *"kit 0.6.0-alpha.0, qmochi 0.3.0-alpha.0, kit-py 0.6.0-alpha.0, linked-versions plugin for kit"* ]] || false
    cmp "$REPO/$CONFIG_FILE" "$REPO/config.orig"
}

@test "cleanup: keeps pins not reached and the plugin while its group has one" {
    release_as --group kit 0.6.0-alpha.0
    release_as qmochi 0.3.0-alpha.0
    bump incubator/qmochi 0.3.0-alpha.0
    git -C "$REPO" commit -qam "chore(release): qmochi 0.3.0-alpha.0"
    release_as --cleanup
    [ "$status" -eq 0 ]
    [ "$(jq -r '.packages["incubator/qmochi"] | has("release-as")' "$REPO/$CONFIG_FILE")" = false ]
    [ "$(jq -r '.packages["."]["release-as"]' "$REPO/$CONFIG_FILE")" = 0.6.0-alpha.0 ]
    [ "$(jq -r '.plugins[0].groupName' "$REPO/$CONFIG_FILE")" = kit ]
}

@test "cleanup: other plugins stay, a string entry included" {
    m="$REPO/$CONFIG_FILE"
    perl -0pi -e 's/  "packages": \{/  "plugins": ["node-workspace"],\n  "packages": {/' "$m"
    git -C "$REPO" commit -qam "chore: a string plugin"
    cp "$m" "$REPO/config.orig"
    release_as --group kit 0.6.0-alpha.0
    [ "$status" -eq 0 ]
    [ "$(jq -c '.plugins | map(type)' "$m")" = '["string","object"]' ]
    bump . 0.6.0-alpha.0 sdk/py 0.6.0-alpha.0
    git -C "$REPO" commit -qam "chore(release): the release PR"
    release_as --cleanup
    [ "$status" -eq 0 ]
    cmp "$m" "$REPO/config.orig"
}

@test "cleanup: drops a stale pin the manifest passed" {
    release_as qmochi 0.3.0-alpha.0
    bump incubator/qmochi 0.3.0-alpha.2
    git -C "$REPO" commit -qam "chore(release): qmochi 0.3.0-alpha.2"
    release_as --cleanup
    [ "$status" -eq 0 ]
    [ "$(jq -r '.packages["incubator/qmochi"] | has("release-as")' "$REPO/$CONFIG_FILE")" = false ]
}

@test "cleanup: nothing to drop is a no-op" {
    release_as qmochi 0.3.0-alpha.0
    head="$(git -C "$REPO" rev-parse HEAD)"
    release_as --cleanup
    [ "$status" -eq 0 ]
    [[ "$output" == *"nothing to drop"* ]] || false
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$head" ]
}

@test "cleanup: a hand-written last-key pin leaves valid JSON" {
    m="$REPO/$CONFIG_FILE"
    cp "$m" "$REPO/config.orig"
    # The first "versioning" line is the root package's last key.
    perl -0pi -e 's/"versioning": "prerelease"\n/"versioning": "prerelease",\n      "release-as": "0.5.0-alpha.26"\n/' "$m"
    [ "$(jq -r '.packages["."]["release-as"]' "$m")" = 0.5.0-alpha.26 ]
    git -C "$REPO" commit -qam "chore: hand pin"
    release_as --cleanup
    [ "$status" -eq 0 ]
    jq -e '.packages["."] | has("release-as") | not' "$m"
    cmp "$m" "$REPO/config.orig"
}

@test "edits that would touch more than the pin are refused" {
    m="$REPO/$CONFIG_FILE"
    perl -0pi -e 's/("component": "qmochi",\n)/$1      "release-as": "0.2.0-alpha.1", "draft": true,\n/' "$m"
    git -C "$REPO" commit -qam "chore: two keys on one line"
    cp "$m" "$REPO/config.before"
    release_as --cleanup
    [ "$status" -eq 1 ]
    [[ "$output" == *"changed more than intended"* ]] || false
    cmp "$m" "$REPO/config.before"
}

@test "edits refuse a plugins key that spans lines" {
    m="$REPO/$CONFIG_FILE"
    perl -0pi -e 's/  "packages": \{/  "plugins": [\n    "sentence-case"\n  ],\n  "packages": {/' "$m"
    git -C "$REPO" commit -qam "chore: plugins by hand"
    release_as --group kit 0.6.0-alpha.0
    [ "$status" -eq 1 ]
    [[ "$output" == *"spans several lines"* ]]
}

@test "refuses to run over staged changes" {
    printf 'y\n' >> "$REPO/cli/cli.go"
    git -C "$REPO" add cli/cli.go
    release_as qmochi 0.3.0-alpha.0
    [ "$status" -eq 1 ]
    [[ "$output" == *"staged changes"* ]] || false
    [ "$(jq -r '.packages["incubator/qmochi"] | has("release-as")' "$REPO/$CONFIG_FILE")" = false ]
}

@test "--dry-run prints the diff and writes nothing" {
    before="$(git -C "$REPO" rev-parse HEAD)"
    release_as --dry-run --group kit 0.6.0-alpha.0
    [ "$status" -eq 0 ]
    [[ "$output" == *'+      "release-as": "0.6.0-alpha.0",'* ]] || false
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before" ]
    [ -z "$(git -C "$REPO" status --porcelain)" ]
}

@test "rejects unknown components, malformed versions and bad usage" {
    release_as nope 0.6.0
    [ "$status" -eq 1 ]
    [[ "$output" == *"kit mcp-tasks qmochi kit-py"* ]] || false
    release_as qmochi v0.3.0
    [ "$status" -eq 1 ]
    release_as qmochi 0.3
    [ "$status" -eq 1 ]
    release_as qmochi
    [ "$status" -eq 2 ]
    release_as --cleanup extra
    [ "$status" -eq 2 ]
}
