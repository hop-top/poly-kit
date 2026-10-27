#!/usr/bin/env bats
# Tests for the repo .gitignore: per-worktree task-tracker state must stay
# out of the ignored-file copy `git hop add` seeds a new worktree with.
#
# git hop copies every git-ignored entry of the source worktree into the
# new one, except entries whose deciding ignore rule sits under a comment
# block holding the `#-hop-#` marker. A copied .tlc/ or .hop/ shadows the
# hub's task-tracker config: the tracker sees two local configs that
# disagree and refuses to run, or reads an empty store.
#
# Each test copies the repo .gitignore into a throwaway repo with global
# excludes disabled, so the verdict depends on this file alone.
#
# Run: bats .github/tests/gitignore-hop-copy.bats
# Or:  make test-lint-scripts

GITIGNORE="$BATS_TEST_DIRNAME/../../.gitignore"
MARKER='#-hop-#'

setup() {
    REPO="$(mktemp -d)"
    git -C "$REPO" init -q
    git -C "$REPO" config core.excludesFile /dev/null
    cp "$GITIGNORE" "$REPO/.gitignore"
    mkdir -p "$REPO/.tlc" "$REPO/.hop/tlc"
    : > "$REPO/.tlc/db.sqlite"
    : > "$REPO/.hop/tlc/config.yaml"
}

teardown() {
    rm -rf "$REPO"
}

# marked <path>: succeed when <path> is ignored by .gitignore and the
# comment block directly above its deciding pattern carries the marker.
marked() {
    local verdict source line
    verdict="$(git -C "$REPO" check-ignore -v -- "$1")" || {
        echo "$1: not ignored"
        return 1
    }
    source="${verdict%%:*}"
    line="${verdict#*:}"
    line="${line%%:*}"
    [ "$source" = ".gitignore" ] || {
        echo "$1: decided by $source, not .gitignore"
        return 1
    }
    awk -v n="$line" -v m="$MARKER" '
        { l[NR] = $0 }
        END {
            for (i = n - 1; i > 0 && l[i] ~ /^[[:space:]]*#/; i--)
                if (index(l[i], m)) exit 0
            exit 1
        }' "$REPO/.gitignore" || {
        echo "$1: rule at .gitignore:$line lacks $MARKER above it"
        return 1
    }
}

@test ".tlc/ is ignored and excluded from the git hop copy" {
    run marked .tlc/db.sqlite
    [ "$status" -eq 0 ] || { echo "$output"; false; }
}

@test ".hop/ is ignored and excluded from the git hop copy" {
    run marked .hop/tlc/config.yaml
    [ "$status" -eq 0 ] || { echo "$output"; false; }
}
