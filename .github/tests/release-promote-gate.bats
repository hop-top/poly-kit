#!/usr/bin/env bats
# Tests for .github/workflows/release-promote-gate.yml.
#
# The "Validate promotion" step's `run:` script is pulled out of the
# workflow file and run in a throwaway repo: the base config is committed,
# the PR's config is the working tree. PROMOTE is a stub standing in for
# scripts/promote-release.sh (it writes the record the test asks for, or
# refuses), so the gate's own logic is covered offline.
#
# The "real promote.js" tests run the actual wrapper against
# hop-top/.github's extension when RELEASE_PLEASE_TARGET_DIR names an
# installed scripts/release-please-target (and node is on PATH); they are
# skipped otherwise.
#
# Run: bats .github/tests/release-promote-gate.bats
# Or:  make test-workflow

WORKFLOW="$BATS_TEST_DIRNAME/../workflows/release-promote-gate.yml"
WRAPPER="$BATS_TEST_DIRNAME/../../scripts/promote-release.sh"
CONFIG=.github/release-please-config.json
MANIFEST=.github/.release-please-manifest.json

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
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm base
    export BASE_SHA="$(git -C "$REPO" rev-parse HEAD)"
    export BASE_REF=next

    step_script "Validate promotion" > "$TMP/gate.sh"
    [ -s "$TMP/gate.sh" ]

    # Stub promote-release.sh: refuses with $STUB_REFUSE, otherwise writes
    # x-channel {line: $STUB_LINE, channel} or x-open-line into --config.
    cat > "$TMP/promote" <<'EOF'
#!/usr/bin/env bash
echo "$*" >> "$STUB_CALLS"
if [ -n "${STUB_REFUSE:-}" ]; then echo "refused: $STUB_REFUSE" >&2; exit 1; fi
cmd=$1 value=$2; shift 2
while [ $# -gt 0 ]; do
  case "$1" in --package) pkg=$2; shift ;; --config) cfg=$2; shift ;; esac
  shift
done
if [ "$cmd" = channel ]; then
  jq --arg p "$pkg" --arg l "$STUB_LINE" --arg c "$value" '.packages[$p]["x-channel"] = {line: $l, channel: $c}' "$cfg" > "$cfg.new"
else
  jq --arg p "$pkg" --arg o "$value" '.packages[$p]["x-open-line"] = $o' "$cfg" > "$cfg.new"
fi
mv "$cfg.new" "$cfg"
EOF
    chmod +x "$TMP/promote"
    export PROMOTE="$TMP/promote" STUB_CALLS="$TMP/calls" STUB_LINE=0.5.0
    : > "$STUB_CALLS"
}

teardown() {
    rm -rf "$TMP"
}

# edit <jq filter>: apply it to the PR's config (the working tree).
edit() {
    jq "$1" "$REPO/$CONFIG" > "$TMP/c.json" && mv "$TMP/c.json" "$REPO/$CONFIG"
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
    [[ "$output" == *'ok: .: x-channel {"line":"0.5.0","channel":"beta"}'* ]]
    grep -q "^channel beta --package \. --config [^ ]*/replay\.json --manifest $REPO/$MANIFEST\$" "$STUB_CALLS"
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

# --- real promote.js --------------------------------------------------

real_extension() {
    [ -f "${RELEASE_PLEASE_TARGET_DIR:-}/bin/promote.js" ] && command -v node >/dev/null ||
        skip "RELEASE_PLEASE_TARGET_DIR does not name an installed scripts/release-please-target"
    export PROMOTE="$WRAPPER"
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
