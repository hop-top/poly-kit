#!/usr/bin/env bash
set -euo pipefail

# test-branch-protection.sh — unit tests for branch-protection.sh.
# `gh` is a shim on PATH; never touches the network.
#
# Shim controls (env):
#   GH_ADMIN      true|false|error   answer to repos/<slug> admin query
#   GH_RULESET_ID id of existing ruleset ("" = none)
#   GH_WRITE_RC   exit code of POST/PUT (default 0)
# Shim records: $GH_LOG (one line per call), $GH_BODY (last stdin body).

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/bin"
cat > "$TMP/bin/gh" <<'SHIM'
#!/usr/bin/env bash
echo "$*" >> "$GH_LOG"
case "$*" in
  *"-X POST"*|*"-X PUT"*)
    cat > "$GH_BODY"
    [[ "${GH_WRITE_RC:-0}" == 0 ]] || { echo "boom" >&2; exit "$GH_WRITE_RC"; }
    echo '{}' ;;
  *"/rulesets"*)
    printf '%s' "${GH_RULESET_ID:-}" ;;
  *)
    case "${GH_ADMIN:-true}" in
      error) echo "HTTP 404" >&2; exit 1 ;;
      *) echo "${GH_ADMIN:-true}" ;;
    esac ;;
esac
SHIM
chmod +x "$TMP/bin/gh"
export PATH="$TMP/bin:$PATH"
export GH_LOG="$TMP/log" GH_BODY="$TMP/body"

# Injected body builder (the real one is a separate concern).
# shellcheck disable=SC2329
fake_body() { printf '{"name":"hop-top-default-main","slug":"%s"}' "$1"; }

# shellcheck source=branch-protection.sh
source "$HERE/branch-protection.sh"

PASS=0 FAIL=0
fresh() { : > "$GH_LOG"; rm -f "$GH_BODY"; unset GH_ADMIN GH_RULESET_ID GH_WRITE_RC; }
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
bad()  { FAIL=$((FAIL+1)); echo "FAIL - $1: $2"; }
check() { # name, actual, expected
  if [[ "$2" == "$3" ]]; then ok "$1"; else bad "$1" "got [$2] want [$3]"; fi
}
has()  { # name, haystack, needle
  if [[ "$2" == *"$3"* ]]; then ok "$1"; else bad "$1" "[$2] lacks [$3]"; fi
}

run() { # runs apply with injected builder; sets OUT, RC
  set +e
  OUT="$(BP_BODY_FN=fake_body apply_branch_protection "$@")"; RC=$?
  set -e
}

# 1. no existing ruleset -> SET via POST
fresh
run hop-top/foo
check "no_existing: stdout" "$OUT" "SET: hop-top/foo"
check "no_existing: rc" "$RC" "0"
has   "no_existing: POST" "$(cat "$GH_LOG")" "-X POST repos/hop-top/foo/rulesets"
has   "no_existing: body sent" "$(cat "$GH_BODY" 2>/dev/null || true)" '"slug":"hop-top/foo"'

# 2. existing ruleset -> UPDATED via PUT on that id
fresh; export GH_RULESET_ID=4242
run hop-top/foo
check "existing: stdout" "$OUT" "UPDATED: hop-top/foo"
check "existing: rc" "$RC" "0"
has   "existing: PUT" "$(cat "$GH_LOG")" "-X PUT repos/hop-top/foo/rulesets/4242"

# 3. not admin -> FAILED, no write
fresh; export GH_ADMIN=false
run hop-top/foo
has   "no_admin: stdout" "$OUT" "FAILED: hop-top/foo"
check "no_admin: rc" "$RC" "1"
has   "no_admin: diagnostic" "$OUT" "admin"
if grep -qE -- "-X (POST|PUT)" "$GH_LOG"; then bad "no_admin: no write" "write attempted"; else ok "no_admin: no write"; fi

# 4. admin query errors -> FAILED
fresh; export GH_ADMIN=error
run hop-top/foo
has   "admin_err: stdout" "$OUT" "FAILED: hop-top/foo"

# 5. call order: admin check, then ruleset lookup, then write
fresh
run hop-top/foo
check "order" "$(awk '{print ($0 ~ /-X/) ? "write" : ($0 ~ /rulesets/ ? "lookup" : "admin")}' "$GH_LOG" | paste -sd, -)" "admin,lookup,write"

# 6. write failure -> FAILED with diagnostic
fresh; export GH_WRITE_RC=1
run hop-top/foo
has   "write_fail: stdout" "$OUT" "FAILED: hop-top/foo"
check "write_fail: rc" "$RC" "1"

# 7. body builder failure -> FAILED, no write
fresh
set +e
OUT="$(BP_BODY_FN=false apply_branch_protection hop-top/foo)"; RC=$?
set -e
has   "body_fail: stdout" "$OUT" "FAILED: hop-top/foo"
if grep -qE -- "-X (POST|PUT)" "$GH_LOG"; then bad "body_fail: no write" "write attempted"; else ok "body_fail: no write"; fi

# 8. missing slug -> FAILED
fresh
set +e; OUT="$(apply_branch_protection)"; RC=$?; set -e
check "no_slug: rc" "$RC" "1"
has   "no_slug: stdout" "$OUT" "FAILED:"

echo "pass=$PASS fail=$FAIL"
[[ "$FAIL" -eq 0 ]]
