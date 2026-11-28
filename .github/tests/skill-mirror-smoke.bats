#!/usr/bin/env bats
# Tests for scripts/skill-mirror-smoke.sh, the post-publish check that a
# skill reached its mirror at the new tag with the tag's version.
#
# The mirror is a directory tree read through a file:// base URL and
# skillsync is a stub that prints what the publish matrix expects, so the
# script's own logic runs offline.
#
# Run: bats .github/tests/skill-mirror-smoke.bats
# Or:  make test-bats

SCRIPT="$BATS_TEST_DIRNAME/../../scripts/skill-mirror-smoke.sh"

setup() {
    TMP="$(mktemp -d)"
    MIRRORS="$TMP/mirrors"
    mkdir -p "$MIRRORS"
    export SKILL_MIRROR_BASE="file://$MIRRORS"
    export SKILL_MIRROR_ATTEMPTS=3
    export SKILL_MIRROR_SLEEP=true
    # The stub answers per component from $TMP/expected.<component>; a
    # missing file means "unknown component", an empty one "no skill".
    cat >"$TMP/skillsync" <<'STUB'
#!/bin/sh
[ "$1" = expected ] && [ "$2" = --component ] || exit 64
f="$(dirname "$0")/expected.$3"
if [ ! -f "$f" ]; then
    echo "skillsync: unknown component \"$3\" (publish matrix has: kit, kit-rs)" >&2
    exit 2
fi
cat "$f"
STUB
    chmod +x "$TMP/skillsync"
    export SKILLSYNC="$TMP/skillsync"
}

teardown() {
    rm -rf "$TMP"
}

# expect <component> <mirror> <skill>...: what the matrix expects.
expect() {
    local c="$1" m="$2" s
    shift 2
    : >"$TMP/expected.$c"
    for s in "$@"; do
        printf '%s\tsdk/x/skills/%s\tskills/%s\n' "$m" "$s" "$s" >>"$TMP/expected.$c"
    done
}

# publish <mirror> <ref> <skill> <skill version> <plugin version>
publish() {
    local d="$MIRRORS/$1/$2/skills/$3"
    mkdir -p "$d/.claude-plugin"
    printf -- '---\nname: %s\nlicense: MIT\nmetadata:\n  version: "%s" # x-release-please-version\n  min_version: "0.1.0"\n---\n\n# %s\n' \
        "$3" "$4" "$3" >"$d/SKILL.md"
    printf '{\n  "name": "%s",\n  "version": "%s",\n  "author": {\n    "name": "hop-top"\n  }\n}\n' \
        "$3" "$5" >"$d/.claude-plugin/plugin.json"
}

@test "versions match: succeeds" {
    expect kit-rs hop-top/kit-rs hop-top-kit-rs
    publish hop-top/kit-rs v0.5.0 hop-top-kit-rs 0.5.0 0.5.0
    run "$SCRIPT" kit-rs/v0.5.0
    [ "$status" -eq 0 ]
    [[ "$output" == *"hop-top-kit-rs"*"0.5.0"* ]]
}

@test "prerelease versions compare as whole strings" {
    expect kit-rs hop-top/kit-rs hop-top-kit-rs
    publish hop-top/kit-rs v0.5.0-beta.16 hop-top-kit-rs 0.5.0-beta.16 0.5.0-beta.16
    run "$SCRIPT" kit-rs/v0.5.0-beta.16
    [ "$status" -eq 0 ]
}

@test "unquoted metadata.version is read" {
    expect kit-rs hop-top/kit-rs hop-top-kit-rs
    publish hop-top/kit-rs v0.5.0 hop-top-kit-rs 0.5.0 0.5.0
    sed -i.bak 's/version: "0.5.0" #/version: 0.5.0 #/' \
        "$MIRRORS/hop-top/kit-rs/v0.5.0/skills/hop-top-kit-rs/SKILL.md"
    run "$SCRIPT" kit-rs/v0.5.0
    [ "$status" -eq 0 ]
}

@test "SKILL.md metadata.version differs from the tag: fails" {
    expect kit-rs hop-top/kit-rs hop-top-kit-rs
    publish hop-top/kit-rs v0.5.1 hop-top-kit-rs 0.5.0 0.5.1
    run "$SCRIPT" kit-rs/v0.5.1
    [ "$status" -eq 1 ]
    [[ "$output" == *"metadata.version"*"0.5.0"*"0.5.1"* ]]
}

@test "plugin.json version differs from the tag: fails" {
    expect kit-rs hop-top/kit-rs hop-top-kit-rs
    publish hop-top/kit-rs v0.5.1 hop-top-kit-rs 0.5.1 0.5.0
    run "$SCRIPT" kit-rs/v0.5.1
    [ "$status" -eq 1 ]
    [[ "$output" == *"plugin.json"*"0.5.0"*"0.5.1"* ]]
}

@test "version only in a nested key is not the plugin version" {
    expect kit-rs hop-top/kit-rs hop-top-kit-rs
    publish hop-top/kit-rs v0.5.1 hop-top-kit-rs 0.5.1 0.5.1
    printf '{\n  "name": "hop-top-kit-rs",\n  "author": {\n    "version": "0.5.1"\n  }\n}\n' \
        >"$MIRRORS/hop-top/kit-rs/v0.5.1/skills/hop-top-kit-rs/.claude-plugin/plugin.json"
    run "$SCRIPT" kit-rs/v0.5.1
    [ "$status" -eq 1 ]
    [[ "$output" == *"plugin.json"* ]]
}

@test "skill absent from the mirror at the tag: fails after every attempt" {
    expect kit-rs hop-top/kit-rs hop-top-kit-rs
    run "$SCRIPT" kit-rs/v0.5.0
    [ "$status" -eq 1 ]
    [[ "$output" == *"attempt 3"* ]]
    [[ "$output" == *"hop-top-kit-rs"* ]]
}

@test "mirror trailing the tag: retries until the skill arrives" {
    expect kit-rs hop-top/kit-rs hop-top-kit-rs
    publish hop-top/kit-rs v0.5.0 hop-top-kit-rs 0.5.0 0.5.0
    d="$MIRRORS/hop-top/kit-rs/v0.5.0"
    mv "$d" "$TMP/late"
    # The wait between attempts is when the mirror push lands.
    printf '#!/bin/sh\nmv "%s" "%s"\n' "$TMP/late" "$d" >"$TMP/arrive"
    chmod +x "$TMP/arrive"
    export SKILL_MIRROR_SLEEP="$TMP/arrive"
    run "$SCRIPT" kit-rs/v0.5.0
    [ "$status" -eq 0 ]
    [[ "$output" == *"attempt 1"* ]]
}

@test "every skill of a shared mirror is checked" {
    expect kit hop-top/kit hop-top-kit hop-top-kit-go
    publish hop-top/kit v0.5.0 hop-top-kit 0.5.0 0.5.0
    publish hop-top/kit v0.5.0 hop-top-kit-go 0.4.9 0.5.0
    run "$SCRIPT" kit/v0.5.0
    [ "$status" -eq 1 ]
    [[ "$output" == *"hop-top-kit-go"*"0.4.9"* ]]
}

@test "component without a skill: succeeds without fetching" {
    : >"$TMP/expected.qmochi"
    run "$SCRIPT" qmochi/v1.2.3
    [ "$status" -eq 0 ]
    [[ "$output" == *"no skill"* ]]
}

@test "tag of a component outside the publish matrix: skipped" {
    run "$SCRIPT" umbrella/v1.0.0
    [ "$status" -eq 0 ]
    [[ "$output" == *"not in the publish matrix"* ]]
}

@test "skillsync failing for another reason: fails" {
    printf '#!/bin/sh\necho "skillsync: boom" >&2\nexit 2\n' >"$TMP/skillsync"
    run "$SCRIPT" kit-rs/v0.5.0
    [ "$status" -eq 1 ]
    [[ "$output" == *"boom"* ]]
}

@test "tag without a component and version: usage error" {
    run "$SCRIPT" v0.5.0
    [ "$status" -eq 2 ]
    run "$SCRIPT"
    [ "$status" -eq 2 ]
}
