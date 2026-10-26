# shellcheck shell=bash
# Fixture shared by the release-*.bats suites: a throwaway repo on `next`
# holding a cut-down copy of the real release-please config, formatted like
# it (one key per line). The root package kit excludes the other packages'
# paths; kit-py (sdk/py) is in kit's linked group; qmochi (incubator/qmochi)
# and mcp-tasks (extensions/mcp-tasks) version alone. Release PRs are
# branches named like release-please's, whose commit moves manifest entries.
# The scripts run from this checkout with the throwaway repo as the working
# directory.

SCRIPTS="$(cd "$BATS_TEST_DIRNAME/../../scripts" && pwd)"
CHECK="$SCRIPTS/check-release-bump.sh"
RELEASE_AS="$SCRIPTS/release-as.sh"

setup() {
    unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY GITHUB_ACTIONS
    export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

    REPO="$(mktemp -d)"
    git -C "$REPO" init -q -b next
    git -C "$REPO" config user.email t@example.com
    git -C "$REPO" config user.name t
    mkdir -p "$REPO/.github" "$REPO/cli" "$REPO/sdk/py" \
        "$REPO/incubator/qmochi" "$REPO/extensions/mcp-tasks"
    cat > "$REPO/.github/release-please-config.json" <<'EOF'
{
  "separate-pull-requests": true,
  "group-pull-request-title-pattern": "chore${scope}: release ${component} libraries",
  "include-component-in-tag": true,
  "tag-separator": "/",
  "x-linked-groups": [
    {
      "group": "kit",
      "components": ["kit", "kit-py"]
    }
  ],
  "packages": {
    ".": {
      "release-type": "go",
      "component": "kit",
      "exclude-paths": [
        "extensions/mcp-tasks",
        "incubator",
        "sdk/py"
      ],
      "prerelease": true,
      "prerelease-type": "alpha.0",
      "versioning": "prerelease"
    },
    "extensions/mcp-tasks": {
      "release-type": "go",
      "component": "mcp-tasks",
      "prerelease": true,
      "prerelease-type": "alpha.0",
      "versioning": "prerelease"
    },
    "incubator/qmochi": {
      "release-type": "go",
      "component": "qmochi",
      "prerelease": true,
      "prerelease-type": "alpha.0",
      "versioning": "prerelease"
    },
    "sdk/py": {
      "release-type": "python",
      "component": "kit-py",
      "prerelease": true,
      "prerelease-type": "alpha.0",
      "versioning": "prerelease"
    }
  }
}
EOF
    set_manifest 0.5.0-alpha.26 0.5.0-alpha.5
    printf 'package cli\n' > "$REPO/cli/cli.go"
    printf 'x = 1\n' > "$REPO/sdk/py/kit.py"
    printf 'package qmochi\n' > "$REPO/incubator/qmochi/q.go"
    printf 'package tasks\n' > "$REPO/extensions/mcp-tasks/t.go"
    git -C "$REPO" add -A
    git -C "$REPO" commit -qm "chore: initial layout"
    git -C "$REPO" tag kit/v0.5.0-alpha.26
    git -C "$REPO" tag kit-py/v0.5.0-alpha.5
    git -C "$REPO" tag qmochi/v0.2.0-alpha.1
    git -C "$REPO" tag mcp-tasks/v0.1.0-alpha.1
}

teardown() {
    rm -rf "$REPO"
}

# shellcheck disable=SC2034 # used by the suites that load this file
CONFIG_FILE=.github/release-please-config.json
MANIFEST_FILE=.github/.release-please-manifest.json

# set_manifest <kit> <kit-py> [qmochi] [mcp-tasks]: write the manifest.
set_manifest() {
    cat > "$REPO/$MANIFEST_FILE" <<EOF
{
  ".": "$1",
  "extensions/mcp-tasks": "${4:-0.1.0-alpha.1}",
  "incubator/qmochi": "${3:-0.2.0-alpha.1}",
  "sdk/py": "$2"
}
EOF
}

# land <message> <file>...: commit on the current branch touching each file.
land() {
    local msg="$1" f
    shift
    for f in "$@"; do
        mkdir -p "$(dirname "$REPO/$f")"
        printf '%s\n' "$RANDOM$RANDOM" >> "$REPO/$f"
        git -C "$REPO" add -- "$f"
    done
    git -C "$REPO" commit -qm "$msg"
}

# bump <path> <version>...: set manifest entries in the working tree.
bump() {
    local m="$REPO/$MANIFEST_FILE"
    while [ $# -ge 2 ]; do
        jq --arg p "$1" --arg v "$2" '.[$p] = $v' "$m" > "$m.tmp" && mv "$m.tmp" "$m"
        shift 2
    done
}

# release_pr <component> <path> <version>: a release-please branch off next
# whose commit moves that package's manifest entry; prints its name.
release_pr() {
    local branch="release-please--branches--next--components--$1"
    git -C "$REPO" checkout -q -B "$branch" next
    bump "$2" "$3"
    # --allow-empty: a re-proposed version leaves the manifest as it was.
    git -C "$REPO" commit -q --allow-empty -am "chore(release): $1 $3"
    git -C "$REPO" checkout -q next
    echo "$branch"
}

# group_pr <group> <path> <version> [<path> <version>...]: the linked-versions
# plugin's branch off next moving several entries; prints its name.
group_pr() {
    local branch="release-please--branches--next--groups--$1"
    shift
    git -C "$REPO" checkout -q -B "$branch" next
    bump "$@"
    git -C "$REPO" commit -q --allow-empty -am "chore(next): release libraries"
    git -C "$REPO" checkout -q next
    echo "$branch"
}

# released <component> <path> <version>: a release merged on next, tagged.
released() {
    bump "$2" "$3"
    git -C "$REPO" commit -qam "chore(release): $1 $3"
    git -C "$REPO" tag "$1/v$3"
}

check() {
    run bash -c "cd '$REPO' && '$CHECK' next '$1'"
}

# pins_job <branch>: what the pins job does on a release PR branch.
pins_job() {
    git -C "$REPO" checkout -q "$1"
    (cd "$REPO" && "$RELEASE_AS" --cleanup >/dev/null)
    git -C "$REPO" checkout -q next
}

release_as() {
    run bash -c "cd '$REPO' && '$RELEASE_AS' $*"
}
