#!/usr/bin/env bash
#
# skill-mirror-smoke.sh — after a component publishes, check that its
# skills reached the mirror at the new tag with the tag's version.
#
# The tag is `<component>/v<version>`, the publish workflow's own shape.
# `skillsync expected --component <component>` names the mirror and the
# skills' paths inside it; nothing expected means nothing to check. For
# each skill the script reads, from the mirror at tag v<version>:
#
#   <path>/SKILL.md                    metadata.version
#   <path>/.claude-plugin/plugin.json  top-level version (installed
#                                      plugins update when it changes)
#
# and fails unless both equal <version>. The mirror push can trail the
# publish run, so a missing or stale file is retried before it fails.
# Read-only: it only fetches public files.
#
# Usage: scripts/skill-mirror-smoke.sh <component>/v<version>
# Exit:  0 every skill present at the tag version (or none expected),
#        1 a skill missing or at the wrong version, 2 usage error.
#
# Environment (all optional):
#   SKILLSYNC             command that runs skillsync
#                         (default: go run ./internal/tools/skillsync)
#   SKILL_MIRROR_BASE     raw file base URL
#                         (default: https://raw.githubusercontent.com);
#                         files live at <base>/<mirror>/<ref>/<path>
#   SKILL_MIRROR_ATTEMPTS fetch attempts per skill (default 6)
#   SKILL_MIRROR_SLEEP    command run between attempts (default: sleep 30)
set -euo pipefail

tag="${1:-}"
case "$tag" in
*/v?*) ;;
*)
    echo "usage: ${0##*/} <component>/v<version>" >&2
    exit 2
    ;;
esac
component="${tag%%/v*}"
version="${tag##*/v}"
[ -n "$component" ] || {
    echo "usage: ${0##*/} <component>/v<version>" >&2
    exit 2
}

skillsync="${SKILLSYNC:-go run ./internal/tools/skillsync}"
base="${SKILL_MIRROR_BASE:-https://raw.githubusercontent.com}"
attempts="${SKILL_MIRROR_ATTEMPTS:-6}"
wait_cmd="${SKILL_MIRROR_SLEEP:-sleep 30}"

errf="$(mktemp)"
trap 'rm -f "$errf"' EXIT

# shellcheck disable=SC2086 # $skillsync is a command with arguments.
if ! expected=$($skillsync expected --component "$component" 2>"$errf"); then
    if grep -q 'unknown component' "$errf"; then
        # Umbrella and meta tags are not in the publish matrix and the
        # publish workflow skips them too.
        echo "::notice::${component} is not in the publish matrix; nothing to check"
        exit 0
    fi
    cat "$errf" >&2
    echo "::error::skillsync expected failed for ${component}"
    exit 1
fi
if [ -z "$expected" ]; then
    echo "${component}: no skill expected in its mirror; nothing to check"
    exit 0
fi

# skill_version <file>: metadata.version of a SKILL.md frontmatter.
skill_version() {
    awk '
        NR == 1 && $0 == "---" { fm = 1; next }
        fm && $0 == "---" { exit }
        fm && /^metadata:/ { meta = 1; next }
        fm && meta && /^[^ ]/ { meta = 0 }
        fm && meta && /^  version:/ {
            v = $0
            sub(/^  version:[ ]*/, "", v)
            sub(/[ ]+#.*$/, "", v)
            gsub(/"/, "", v)
            print v
            exit
        }
    ' "$1"
}

# plugin_version <file>: the top-level version of a plugin manifest
# (two-space indent, as skillsync writes it; a nested key never matches).
plugin_version() {
    sed -n 's/^  "version": *"\([^"]*\)".*/\1/p' "$1" | head -n 1
}

# check_skill <mirror> <path>: print the problem, or nothing when fine.
check_skill() {
    local mirror="$1" path="$2" url="$base/$1/v$version" got
    local dir="$3"
    if ! curl -fsS "$url/$path/SKILL.md" -o "$dir/SKILL.md" 2>/dev/null; then
        echo "$path/SKILL.md not found in $mirror at v$version"
        return
    fi
    got=$(skill_version "$dir/SKILL.md")
    if [ "$got" != "$version" ]; then
        echo "$path/SKILL.md metadata.version is '${got}', tag is '${version}'"
        return
    fi
    if ! curl -fsS "$url/$path/.claude-plugin/plugin.json" -o "$dir/plugin.json" 2>/dev/null; then
        echo "$path/.claude-plugin/plugin.json not found in $mirror at v$version"
        return
    fi
    got=$(plugin_version "$dir/plugin.json")
    if [ "$got" != "$version" ]; then
        echo "$path/.claude-plugin/plugin.json version is '${got}', tag is '${version}'"
    fi
}

work="$(mktemp -d)"
trap 'rm -rf "$work"; rm -f "$errf"' EXIT

status=0
while IFS=$'\t' read -r mirror _ path; do
    problem=""
    for attempt in $(seq 1 "$attempts"); do
        problem=$(check_skill "$mirror" "$path" "$work")
        if [ -z "$problem" ]; then
            break
        fi
        echo "attempt ${attempt} of ${attempts}: ${problem}"
        if [ "$attempt" -lt "$attempts" ]; then
            $wait_cmd
        fi
    done
    if [ -n "$problem" ]; then
        echo "::error::${mirror} v${version}: ${problem}"
        status=1
    else
        echo "ok: ${path##*/} in ${mirror} at v${version} (SKILL.md and plugin.json at ${version})"
    fi
done <<<"$expected"
exit "$status"
