#!/usr/bin/env bash
set -euo pipefail

# test-branch-protection-bulk.sh — unit tests for branch-protection-bulk.sh.
# `gh` is a shim on PATH; never touches the network.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/bin"
cat > "$TMP/bin/gh" <<'SHIM'
#!/usr/bin/env bash
echo "$*" >> "$GH_LOG"
case "$*" in
  "repo list hop-top"*)
    printf 'foo\nold\n.github\nbar\n' ;;   # post-jq output (shim ignores jq)
  *"-X POST"*|*"-X PUT"*)
    cat >/dev/null; echo '{}' ;;
  *"/rulesets"*)
    case "$*" in *"repos/hop-top/bar/"*) printf '77' ;; *) printf '' ;; esac ;;
  *)
    case "$*" in *"repos/other/locked"*) echo false ;; *) echo true ;; esac ;;
esac
SHIM
chmod +x "$TMP/bin/gh"
export PATH="$TMP/bin:$PATH"
export GH_LOG="$TMP/log"
export BP_BODY_FN=fake_body
# shellcheck disable=SC2329
fake_body() { printf '{}'; }
export -f fake_body

BULK="$HERE/branch-protection-bulk.sh"
PASS=0 FAIL=0
ok()  { PASS=$((PASS+1)); echo "ok   - $1"; }
bad() { FAIL=$((FAIL+1)); echo "FAIL - $1: $2"; }
has() { if [[ "$2" == *"$3"* ]]; then ok "$1"; else bad "$1" "[$2] lacks [$3]"; fi; }
lacks() { if [[ "$2" != *"$3"* ]]; then ok "$1"; else bad "$1" "[$2] has [$3]"; fi; }
rc_is() { if [[ "$RC" -eq "$1" ]]; then ok "$2"; else bad "$2" "rc=$RC"; fi; }
run() { : > "$GH_LOG"; set +e; OUT="$("$BULK" "$@" 2>&1)"; RC=$?; set -e; }

# shortname -> hop-top/<name>; owner/name preserved
run foo other/locked
has "short: org default" "$OUT" "SET: hop-top/foo"
has "slash: preserved" "$OUT" "FAILED: other/locked"
has "summary counts" "$OUT" "SET=1 UPDATED=0 FAILED=1"
rc_is 1 "rc 1 on any FAILED"

# existing ruleset -> UPDATED
run bar
has "updated" "$OUT" "UPDATED: hop-top/bar"
has "summary" "$OUT" "SET=0 UPDATED=1 FAILED=0"
rc_is 0 "rc 0 all ok"

# --all: skip .github (archived filtering is done by gh query)
run --all
has   "all: lists org" "$(cat "$GH_LOG")" "repo list hop-top"
has   "all: archived filter in query" "$(cat "$GH_LOG")" "isArchived"
has   "all: foo" "$OUT" "SET: hop-top/foo"
has   "all: bar" "$OUT" "UPDATED: hop-top/bar"
lacks "all: skips .github" "$OUT" "hop-top/.github"
has   "all: summary" "$OUT" "SET=2 UPDATED=1 FAILED=0"

# no args -> usage, rc 2
run
rc_is 2 "no args rc 2"
has "no args usage" "$OUT" "usage:"

# --help -> rc 0
run --help
rc_is 0 "help rc 0"

echo "pass=$PASS fail=$FAIL"
[[ "$FAIL" -eq 0 ]]
