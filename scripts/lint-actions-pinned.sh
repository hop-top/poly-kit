#!/usr/bin/env bash
#
# lint-actions-pinned.sh — fail when a GitHub Actions `uses:` ref is not
# pinned to a full commit SHA.
#
# A tag or branch (`@v4`, `@main`) can be moved to new code by whoever
# controls the action's repo; a 40-hex commit SHA cannot. Write the
# version the SHA stands for in a trailing comment so Dependabot (see
# .github/dependabot.yml) and readers can follow it:
#
#   uses: owner/repo@<40-hex sha> # v1.2.3
#
# Accepted refs:
#   - owner/repo[/path]@<40-hex commit SHA>   (actions, reusable workflows)
#   - ./local/path                            (actions in this repo)
#   - docker://image@sha256:<64-hex digest>
#
# Comment lines are skipped, so example snippets in a header stay free.
#
# Scope: tracked .github/workflows/*.yml|yaml and
# .github/actions/**/action.yml|yaml, or the files given as arguments.
#
# Usage: scripts/lint-actions-pinned.sh [file...]   (from inside the repo)
# Exit:  0 clean, 1 findings, 2 usage or scan error.
set -euo pipefail

if [ $# -gt 0 ]; then
    files=("$@")
else
    cd "$(git rev-parse --show-toplevel)"
    files=()
    while IFS= read -r f; do
        files+=("$f")
    done < <(git ls-files -- \
        '.github/workflows/*.yml' '.github/workflows/*.yaml' \
        '.github/actions/**/action.yml' '.github/actions/**/action.yaml' \
        '.github/actions/*/action.yml' '.github/actions/*/action.yaml' | sort -u)
fi

[ ${#files[@]} -gt 0 ] || exit 0

for f in "${files[@]}"; do
    [ -f "$f" ] || { printf 'lint-actions-pinned: missing file: %s\n' "$f" >&2; exit 2; }
done

# No regex interval counts ({40}): mawk on older runners lacks them.
awk '
    function hex(s, n) { return length(s) == n && s !~ /[^0-9a-f]/ }
    # `uses:` as a key: a step (`- uses:`) or a job-level reusable call.
    /^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*/ {
        ref = $0
        sub(/^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*/, "", ref)
        sub(/[[:space:]]+#.*$/, "", ref)
        sub(/[[:space:]]+$/, "", ref)
        gsub(/^["\047]|["\047]$/, "", ref)
        if (ref ~ /^\.\//) next
        at = index(ref, "@")
        name = substr(ref, 1, at - 1); pin = substr(ref, at + 1)
        if (at > 0 && name ~ /^docker:\/\/[^@[:space:]]+$/ && pin ~ /^sha256:/ \
            && hex(substr(pin, 8), 64)) next
        if (at > 0 && name ~ /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+(\/[^@[:space:]]+)?$/ \
            && hex(pin, 40)) next
        printf "%s:%d: unpinned uses: %s\n", FILENAME, FNR, ref
        bad = 1
    }
    END { exit bad }
' "${files[@]}" || {
    printf 'lint-actions-pinned: pin each ref to a full commit SHA, version in a trailing comment (uses: owner/repo@<sha> # vX.Y.Z)\n' >&2
    exit 1
}
