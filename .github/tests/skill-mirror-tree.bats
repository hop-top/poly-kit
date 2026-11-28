#!/usr/bin/env bats
# What each mirror receives carries a working skill.
#
# The publish workflow hands every component to the shared mirror workflow
# (hop-top/.github), which pushes `git subtree split --prefix=<dir>` of the
# component directory, or for the repository root (dir ".") the whole tree
# minus .github/workflows. This suite repeats that split for every entry of
# the publish workflow's ecosystems map in a throwaway repository built from
# the working tree, then checks the result the way a mirror's consumer sees
# it: skills/<name>/ with SKILL.md, the plugin manifest and the generated
# references, no marketplace catalog, and `skillsync lint --standalone`
# clean on the split tree alone. A component with no skill (ports.yaml
# no_skill) must carry no skills/ directory. Offline.
#
# Needs git with `git subtree`, go (to build skillsync once) and tar.
# Set SKILLSYNC to a built skillsync binary to skip the build.
#
# Run: bats .github/tests/skill-mirror-tree.bats
# Or:  make test-bats

ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
PUBLISH="$ROOT/.github/workflows/publish.yml"

# components: "<name> <dir>" per entry of the ecosystems map.
components() {
    awk '
        /^ {8}[A-Za-z0-9_-]+:$/ { name = $1; sub(/:$/, "", name) }
        /^ {10}dir: / && name != "" { print name, $2; name = "" }
    ' "$PUBLISH"
}

setup_file() {
    unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY
    export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
    SCRATCH="$(mktemp -d)"
    export SCRATCH
    git subtree 2>&1 | grep -q '^usage: git subtree' || {
        echo "git subtree is not installed; the mirror workflow needs it" >&2
        return 1
    }

    if [ -z "${SKILLSYNC:-}" ]; then
        (cd "$ROOT" && go build -buildvcs=false -o "$SCRATCH/skillsync" ./internal/tools/skillsync) >&2
        SKILLSYNC="$SCRATCH/skillsync"
    fi
    export SKILLSYNC

    # The working tree as a one-commit repository, so uncommitted edits are
    # what gets split. Dependency and build directories are left out.
    SRC="$SCRATCH/src"
    export SRC
    mkdir -p "$SRC"
    tar -C "$ROOT" --exclude=.git --exclude=node_modules --exclude=.venv \
        --exclude=target --exclude=vendor -cf - . | tar -C "$SRC" -xf -
    git -C "$SRC" init -q -b main
    git -C "$SRC" add -A
    git -C "$SRC" -c user.email=t@example.com -c user.name=t commit -q -m tree
}

teardown_file() {
    rm -rf "$SCRATCH"
}

# split <component> <dir>: extract what the mirror receives into
# $SCRATCH/out/<component> (kept for later tests of the same component).
split() {
    local c="$1" dir="$2" out="$SCRATCH/out/$1" ref
    [ -d "$out" ] && return 0
    mkdir -p "$out"
    if [ "$dir" = . ]; then
        # The shared workflow: HEAD's tree minus .github/workflows.
        local idx="$SCRATCH/idx.$c"
        GIT_INDEX_FILE="$idx" git -C "$SRC" read-tree HEAD
        if GIT_INDEX_FILE="$idx" git -C "$SRC" ls-files --cached --error-unmatch .github/workflows/ >/dev/null 2>&1; then
            GIT_INDEX_FILE="$idx" git -C "$SRC" rm -rf --cached --quiet .github/workflows/
        fi
        ref=$(GIT_INDEX_FILE="$idx" git -C "$SRC" write-tree)
    else
        ref=$(git -C "$SRC" subtree split --prefix="$dir" HEAD)
    fi
    git -C "$SRC" archive "$ref" | tar -C "$out" -xf -
}

# json_top <key> <file>: a top-level (two-space indented) string key.
json_top() {
    sed -n 's/^  "'"$1"'": *"\([^"]*\)".*/\1/p' "$2" | head -n 1
}

# check_mirror <component>: the assertions every mirror tree must meet.
check_mirror() {
    local c="$1" dir out skills name mdir srcdir
    dir=$(components | awk -v c="$c" '$1 == c { print $2 }')
    [ -n "$dir" ] || { echo "component $c is not in $PUBLISH" >&2; return 1; }
    split "$c" "$dir"
    out="$SCRATCH/out/$c"

    # A mirror is a plugin source, never a marketplace.
    run find "$out" -path '*/.claude-plugin/marketplace.json'
    [ "$status" -eq 0 ]
    [ -z "$output" ]
    # Mirror repos never receive the source repository's workflows.
    [ ! -e "$out/.github/workflows" ]

    skills=$("$SKILLSYNC" expected --root "$ROOT" --component "$c")
    if [ -z "$skills" ]; then
        [ ! -e "$out/skills" ]
        return 0
    fi
    while IFS="$(printf '\t')" read -r _ srcdir mdir; do
        name=$(basename "$mdir")
        [ -f "$out/$mdir/SKILL.md" ]
        [ -f "$out/$mdir/.claude-plugin/plugin.json" ]
        [ "$(json_top name "$out/$mdir/.claude-plugin/plugin.json")" = "$name" ]
        [ -n "$(json_top version "$out/$mdir/.claude-plugin/plugin.json")" ]
        # The generated references arrive, and the skill arrives whole.
        [ -n "$(ls "$out/$mdir/references")" ]
        diff -r "$ROOT/$srcdir" "$out/$mdir"
        # Lint the skill from the split tree alone.
        (cd "$out" && "$SKILLSYNC" lint --standalone "$mdir")
    done <<<"$skills"
}

@test "the suite covers every publish.yml component" {
    run components
    [ "$status" -eq 0 ]
    [ "$output" = "$(printf '%s\n' \
        'kit .' \
        'mcp-tasks extensions/mcp-tasks' \
        'kit-php sdk/experimental/php' \
        'kit-py sdk/py' \
        'kit-rs sdk/rs' \
        'kit-ts sdk/ts' \
        'qmochi incubator/qmochi')" ]
}

@test "the components together expect at least the six skills" {
    total=0
    while read -r c _; do
        n=$("$SKILLSYNC" expected --root "$ROOT" --component "$c" | wc -l)
        total=$((total + n))
    done < <(components)
    # Six skills today (router plus five ports); never none.
    [ "$total" -ge 6 ]
}

@test "kit mirror (root split): router and Go skill" {
    check_mirror kit
    [ -f "$SCRATCH/out/kit/skills/hop-top-kit/SKILL.md" ]
    [ -f "$SCRATCH/out/kit/skills/hop-top-kit-go/SKILL.md" ]
}

@test "kit-ts mirror carries hop-top-kit-ts" {
    check_mirror kit-ts
}

@test "kit-py mirror carries hop-top-kit-py" {
    check_mirror kit-py
}

@test "kit-rs mirror carries hop-top-kit-rs" {
    check_mirror kit-rs
}

@test "kit-php mirror carries hop-top-kit-php" {
    check_mirror kit-php
}

@test "mcp-tasks mirror has no skills directory" {
    check_mirror mcp-tasks
}

@test "qmochi mirror has no skills directory" {
    check_mirror qmochi
}
