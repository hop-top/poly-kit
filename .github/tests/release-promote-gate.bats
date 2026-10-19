#!/usr/bin/env bats
# Tests for .github/workflows/release-promote-gate.yml.
#
# The "Validate promotion" step's `run:` script is pulled out of the
# workflow file and run in a throwaway repo: the base is the last commit,
# the PR is the working tree. The base's scripts/promote-release.sh is a
# stub (it writes the record the test asks for, or refuses, and logs what
# it was given), so the gate's own logic is covered offline.
#
# The "real promote.js" tests commit the actual wrapper as the base's and
# run it against hop-top/.github's extension when RELEASE_PLEASE_TARGET_DIR
# names an installed scripts/release-please-target (and node is on PATH);
# they are skipped otherwise.
#
# Run: bats .github/tests/release-promote-gate.bats
# Or:  make test-workflow

WORKFLOW="$BATS_TEST_DIRNAME/../workflows/release-promote-gate.yml"
WRAPPER="$BATS_TEST_DIRNAME/../../scripts/promote-release.sh"
CONFIG=.github/release-please-config.json
MANIFEST=.github/.release-please-manifest.json
BASE_PIN=1111111111111111111111111111111111111111
RELEASE_BOT='hop-top-release-bot[bot]'

# step_script <step name>: print the step's `run: |` block, dedented.
step_script() {
    awk -v name="$1" '
        $0 ~ "^ *- name: " name "$" { in_step = 1; next }
        in_step && /^ *- name: / { exit }
        in_step && !run && /^ *run: \|$/ { run = 1; ri = index($0, "r"); next }
        run {
            if ($0 ~ /^ *$/) { print ""; next }
            match($0, /^ */)
            if (RLENGTH < ri) exit
            if (!bi) bi = RLENGTH + 1
            print substr($0, bi)
        }
    ' "$WORKFLOW"
}

setup() {
    unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY
    export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    git init -q -b next "$REPO"
    git -C "$REPO" config user.email t@example.com
    git -C "$REPO" config user.name t
    mkdir -p "$REPO/.github"
    cat > "$REPO/$CONFIG" <<'EOF'
{
  "label": "status:release-pending",
  "plugins": [{"type": "target-context", "prereleaseBranches": ["next"]}],
  "packages": {
    ".": {"release-type": "go", "component": "kit", "prerelease": true,
          "prerelease-type": "alpha.0", "versioning": "target", "bump-minor-pre-major": true},
    "incubator/qmochi": {"release-type": "go", "component": "qmochi", "prerelease": true,
          "prerelease-type": "alpha.0", "versioning": "target", "bump-minor-pre-major": true}
  }
}
EOF
    printf '{".": "0.5.0-alpha.26", "incubator/qmochi": "0.2.0-alpha.1"}\n' > "$REPO/$MANIFEST"
    mkdir -p "$REPO/.github/workflows" "$REPO/scripts"
    printf '    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@%s\n' "$BASE_PIN" \
        > "$REPO/.github/workflows/release-please.yml"

    # Stub promote-release.sh: logs its arguments, the manifest it was
    # given and the pin next to it, one line per call; refuses with
    # $STUB_REFUSE, otherwise writes x-channel {line: $STUB_LINE, channel}
    # or x-open-line into --config.
    cat > "$REPO/scripts/promote-release.sh" <<'EOF'
#!/usr/bin/env bash
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
all="$*" cmd=$1 value=$2; shift 2
while [ $# -gt 0 ]; do
  case "$1" in --package) pkg=$2; shift ;; --config) cfg=$2; shift ;; --manifest) man=$2; shift ;; esac
  shift
done
pin=$(sed -n 's/.*@//p' "$root/.github/workflows/release-please.yml")
echo "$all | manifest=$(jq -c . "$man") | pin=$pin" >> "$STUB_CALLS"
if [ -n "${STUB_REFUSE:-}" ]; then echo "refused: $STUB_REFUSE" >&2; exit 1; fi
if [ "$cmd" = channel ]; then
  jq --arg p "$pkg" --arg l "$STUB_LINE" --arg c "$value" '.packages[$p]["x-channel"] = {line: $l, channel: $c}' "$cfg" > "$cfg.new"
else
  jq --arg p "$pkg" --arg o "$value" '.packages[$p]["x-open-line"] = $o' "$cfg" > "$cfg.new"
fi
mv "$cfg.new" "$cfg"
EOF
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm base
    export BASE_SHA="$(git -C "$REPO" rev-parse HEAD)"
    export BASE_REF=next HEAD_REF=feat/something PR_AUTHOR=someone
    export HEAD_REPO=hop-top/poly-kit BASE_REPO=hop-top/poly-kit

    step_script "Validate promotion" > "$TMP/gate.sh"
    [ -s "$TMP/gate.sh" ]

    export STUB_CALLS="$TMP/calls" STUB_LINE=0.5.0
    : > "$STUB_CALLS"
}

teardown() {
    rm -rf "$TMP"
}

# edit <jq filter>: apply it to the PR's config (the working tree).
edit() {
    jq "$1" "$REPO/$CONFIG" > "$TMP/c.json" && mv "$TMP/c.json" "$REPO/$CONFIG"
}

# edit_manifest <jq filter>: apply it to the PR's manifest.
edit_manifest() {
    jq "$1" "$REPO/$MANIFEST" > "$TMP/m.json" && mv "$TMP/m.json" "$REPO/$MANIFEST"
}

# release_pr: the PR is a release PR (release-please-- branch, release bot).
release_pr() {
    export HEAD_REF=release-please--branches--next--components--kit PR_AUTHOR="$RELEASE_BOT"
}

gate() {
    cd "$REPO" && bash "$TMP/gate.sh"
}

@test "no config on the base: nothing to validate" {
    git -C "$REPO" rm -q --cached "$CONFIG"
    git -C "$REPO" commit -qm "drop config"
    export BASE_SHA="$(git -C "$REPO" rev-parse HEAD)"
    edit '.packages["."]["x-channel"] = {line: "9.9.9", channel: "rc"}'
    run gate
    [ "$status" -eq 0 ]
    [[ "$output" == *"initial setup"* ]]
    [ ! -s "$STUB_CALLS" ]
}

@test "records on a branch that is not a prerelease branch are inert" {
    export BASE_REF=main
    edit '.packages["."]["x-channel"] = {line: "9.9.9", channel: "rc"} | .label = "x"'
    run gate
    [ "$status" -eq 0 ]
    [[ "$output" == *"main is not a prerelease branch"* ]]
    [ ! -s "$STUB_CALLS" ]
}

@test "a config change without a record change is not a promotion" {
    edit '.label = "other"'
    run gate
    [ "$status" -eq 0 ]
    [[ "$output" == *"not a promotion"* ]]
    [ ! -s "$STUB_CALLS" ]
}

@test "a record change next to another config change fails" {
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "beta"} | .label = "other"'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"::error"*"changes only x-channel / x-open-line records"* ]]
    [ ! -s "$STUB_CALLS" ]
}

@test "a record the way promote.js writes it passes" {
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "beta"}'
    run gate
    [ "$status" -eq 0 ]
    [[ "$output" == *'ok: .: x-channel {"channel":"beta","line":"0.5.0"}'* ]]
    grep -q '^channel beta --package \. --config [^ ]*/replay\.json --manifest [^ ]*/manifest\.json --no-commit | ' "$STUB_CALLS"
}

@test "a record for another line than promote.js records fails" {
    edit '.packages["."]["x-channel"] = {line: "0.6.0", channel: "beta"}'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *'::error'*'not what promote.js records from the base config: {"channel":"beta","line":"0.5.0"}'* ]]
}

@test "a record promote.js refuses fails with its reason" {
    export STUB_REFUSE="kit (.): alpha -> rc skips beta"
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "rc"}'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"::error"*"refused by promote.js: refused: kit (.): alpha -> rc skips beta"* ]]
}

@test "an opened line is replayed with open-line" {
    edit '.packages["."]["x-open-line"] = "1.0.0"'
    run gate
    [ "$status" -eq 0 ]
    grep -q "^open-line 1.0.0 --package \. " "$STUB_CALLS"
}

@test "every changed record is checked; one bad one fails the gate" {
    export STUB_LINE=0.2.0
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "beta"}
          | .packages["incubator/qmochi"]["x-channel"] = {line: "0.2.0", channel: "beta"}'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *'ok: incubator/qmochi: x-channel'* ]]
    [[ "$output" == *'::error'*'.: x-channel'*'not what promote.js records'* ]]
    [ "$(wc -l < "$STUB_CALLS")" -eq 2 ]
}

@test "removing a record passes without a replay" {
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "beta"}'
    git -C "$REPO" commit -qam promote
    export BASE_SHA="$(git -C "$REPO" rev-parse HEAD)"
    edit 'del(.packages["."]["x-channel"])'
    run gate
    [ "$status" -eq 0 ]
    [[ "$output" == *"ok: .: x-channel removed"* ]]
    [ ! -s "$STUB_CALLS" ]
}

# --- the manifest: release PRs only -------------------------------------

@test "manifest: a hand edit fails, with a record on top" {
    edit_manifest '.["."] = "0.5.0-beta.3"'
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "rc"}'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"::error file=$MANIFEST::the manifest is written by release PRs only"* ]]
    [ ! -s "$STUB_CALLS" ]
}

@test "manifest: a hand edit fails on a branch where records are inert too" {
    export BASE_REF=main
    edit_manifest '.["."] = "1.0.0"'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"written by release PRs only"* ]]
}

@test "manifest: a release PR changes it" {
    release_pr
    edit_manifest '.["."] = "0.5.0-alpha.27"'
    run gate
    [ "$status" -eq 0 ]
    [[ "$output" == *"ok: $MANIFEST changed by a release PR"* ]]
}

@test "manifest: a release-please-- branch someone else opened fails" {
    release_pr
    export PR_AUTHOR=someone
    edit_manifest '.["."] = "0.5.0-alpha.27"'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"written by release PRs only"* ]]
}

@test "manifest: the release bot on another branch fails" {
    release_pr
    export HEAD_REF=feat/something
    edit_manifest '.["."] = "0.5.0-alpha.27"'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"written by release PRs only"* ]]
}

@test "manifest: next merged into main carries next's manifest" {
    export BASE_REF=main HEAD_REF=next
    edit_manifest '.["."] = "0.5.0-rc.2"'
    run gate
    [ "$status" -eq 0 ]
    [[ "$output" == *"ok: $MANIFEST changed by a merge from next"* ]]
}

@test "manifest: main forward-ported into next carries main's manifest" {
    export HEAD_REF=main
    edit_manifest '.["incubator/qmochi"] = "0.1.4"'
    run gate
    [ "$status" -eq 0 ]
    [[ "$output" == *"ok: $MANIFEST changed by a merge from main"* ]]
}

@test "manifest: a fork's branch named next fails" {
    export BASE_REF=main HEAD_REF=next HEAD_REPO=someone/poly-kit
    edit_manifest '.["."] = "0.5.0-rc.2"'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"written by release PRs only"* ]]
}

@test "manifest: a deleted fork's branch named next fails" {
    export BASE_REF=main HEAD_REF=next HEAD_REPO=
    edit_manifest '.["."] = "0.5.0-rc.2"'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"written by release PRs only"* ]]
}

@test "manifest: a PR that changes neither file passes" {
    echo change > "$REPO/README.md"
    run gate
    [ "$status" -eq 0 ]
    [[ "$output" != *"::error"* ]]
    [ ! -s "$STUB_CALLS" ]
}

# --- the verdict rests on the base -------------------------------------

@test "base: the roles come from the base config, a PR cannot switch the gate off" {
    edit '.plugins[0].prereleaseBranches = ["main"]
          | .packages["."]["x-channel"] = {line: "9.9.9", channel: "rc"}'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"::error"*"changes only x-channel / x-open-line records"* ]]
}

@test "base: records replay on the base manifest, not the PR's" {
    release_pr
    edit_manifest '.["."] = "0.5.0-beta.3"'
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "rc"}'
    run gate
    [ "$status" -eq 0 ]
    grep -q '| manifest={"\.":"0\.5\.0-alpha\.26","incubator/qmochi":"0\.2\.0-alpha\.1"} |' "$STUB_CALLS"
    if grep -q 'beta\.3' "$STUB_CALLS"; then false; fi
}

@test "base: the base's promote-release.sh and pin replay, the PR's are ignored" {
    export STUB_REFUSE="kit (.): alpha -> rc skips beta"
    # The PR's own wrapper would write any record; its pin differs.
    cat > "$REPO/scripts/promote-release.sh" <<'EOF'
#!/usr/bin/env bash
echo "pr wrapper" >> "$STUB_CALLS"
while [ $# -gt 0 ]; do case "$1" in --config) cfg=$2; shift ;; esac; shift; done
jq '.packages["."]["x-channel"] = {line: "0.5.0", channel: "rc"}' "$cfg" > "$cfg.new" && mv "$cfg.new" "$cfg"
EOF
    sed -i.bak "s/$BASE_PIN/2222222222222222222222222222222222222222/" "$REPO/.github/workflows/release-please.yml"
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "rc"}'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"refused by promote.js: refused: kit (.): alpha -> rc skips beta"* ]]
    if grep -q "pr wrapper" "$STUB_CALLS"; then false; fi
    grep -q "| pin=$BASE_PIN\$" "$STUB_CALLS"
}

@test "base: a release PR leaving the line passes without replaying anything" {
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "beta"}'
    git -C "$REPO" rm -q scripts/promote-release.sh
    git -C "$REPO" commit -qam "promote; no wrapper"
    export BASE_SHA="$(git -C "$REPO" rev-parse HEAD)"
    release_pr
    edit_manifest '.["."] = "0.6.0-alpha.0"'
    edit 'del(.packages["."]["x-channel"])'
    run gate
    [ "$status" -eq 0 ]
    [[ "$output" == *"ok: $MANIFEST changed by a release PR"* ]]
    [[ "$output" == *"ok: .: x-channel removed"* ]]
}

@test "base: a record with no manifest on the base fails" {
    git -C "$REPO" rm -q "$MANIFEST"
    git -C "$REPO" commit -qm "drop manifest"
    export BASE_SHA="$(git -C "$REPO" rev-parse HEAD)"
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "beta"}'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"no $MANIFEST on the base"* ]]
    [ ! -s "$STUB_CALLS" ]
}

# --- real promote.js --------------------------------------------------

# real_extension: commit the actual wrapper as the base's.
real_extension() {
    [ -f "${RELEASE_PLEASE_TARGET_DIR:-}/bin/promote.js" ] && command -v node >/dev/null ||
        skip "RELEASE_PLEASE_TARGET_DIR does not name an installed scripts/release-please-target"
    cp "$WRAPPER" "$REPO/scripts/promote-release.sh"
    git -C "$REPO" commit -qam "real wrapper"
    export BASE_SHA="$(git -C "$REPO" rev-parse HEAD)"
}

@test "real promote.js: a release PR's manifest does not lift the channel" {
    real_extension
    release_pr
    edit_manifest '.["."] = "0.5.0-beta.0"'
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "rc"}'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"skips beta"* ]]
}

@test "real promote.js: kit and qmochi to beta on their lines pass" {
    real_extension
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "beta"}
          | .packages["incubator/qmochi"]["x-channel"] = {line: "0.2.0", channel: "beta"}'
    run gate
    [ "$status" -eq 0 ]
    [[ "$output" == *"ok: .: x-channel"* ]]
    [[ "$output" == *"ok: incubator/qmochi: x-channel"* ]]
}

@test "real promote.js: alpha straight to rc is refused" {
    real_extension
    edit '.packages["."]["x-channel"] = {line: "0.5.0", channel: "rc"}'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"skips beta"* ]]
}

@test "real promote.js: a record for a line the package is not on fails" {
    real_extension
    edit '.packages["."]["x-channel"] = {line: "0.6.0", channel: "beta"}'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *'not what promote.js records from the base config: {"channel":"beta","line":"0.5.0"}'* ]]
}

@test "real promote.js: opening 1.0.0 passes, opening a reached line fails" {
    real_extension
    edit '.packages["."]["x-open-line"] = "1.0.0"'
    run gate
    [ "$status" -eq 0 ]
    edit '.packages["."]["x-open-line"] = "0.5.0"'
    run gate
    [ "$status" -eq 1 ]
    [[ "$output" == *"already reached"* ]]
}
