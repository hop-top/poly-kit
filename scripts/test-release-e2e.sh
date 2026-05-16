#!/usr/bin/env bash
set -euo pipefail

# -------------------------------------------------------
# test-release-e2e.sh — E2e tests for release promotion
# and changelog rewrite scripts.
# -------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMPDIR_BASE="$(mktemp -d)"
PASS=0
FAIL=0
ERRORS=()

cleanup() { rm -rf "$TMPDIR_BASE"; }
trap cleanup EXIT

# --- Helpers -------------------------------------------

info()  { printf "\033[1;34m[INFO]\033[0m  %s\n" "$*"; }
pass()  { printf "\033[1;32m[PASS]\033[0m  %s\n" "$*"; PASS=$((PASS + 1)); }
fail()  { printf "\033[1;31m[FAIL]\033[0m  %s\n" "$*"; FAIL=$((FAIL + 1)); ERRORS+=("$*"); }

assert_file_contains() {
  local file="$1" pattern="$2" label="$3"
  if grep -q "$pattern" "$file" 2>/dev/null; then
    pass "$label"
  else
    fail "$label: '$pattern' not found in $file"
  fi
}

assert_file_not_contains() {
  local file="$1" pattern="$2" label="$3"
  if grep -q "$pattern" "$file" 2>/dev/null; then
    fail "$label: '$pattern' unexpectedly found in $file"
  else
    pass "$label"
  fi
}

assert_exit_code() {
  local expected="$1" actual="$2" label="$3"
  if [ "$actual" -eq "$expected" ]; then
    pass "$label"
  else
    fail "$label: expected exit $expected, got $actual"
  fi
}

# Create a temp git repo with release-please-config.json
# $1 = dir name, $2 = prerelease-type (empty = none/release)
make_promote_repo() {
  local dir="$TMPDIR_BASE/$1"
  mkdir -p "$dir"
  git -C "$dir" init -q
  git -C "$dir" config user.name "Test"
  git -C "$dir" config user.email "test@test.com"

  mkdir -p "$dir/.github"
  if [ -n "${2:-}" ]; then
    cat > "$dir/.github/release-please-config.json" <<EOJSON
{
  "packages": {
    ".": { "prerelease-type": "$2" },
    "sub": { "prerelease-type": "$2" }
  }
}
EOJSON
  else
    cat > "$dir/.github/release-please-config.json" <<EOJSON
{
  "packages": {
    ".": {},
    "sub": {}
  }
}
EOJSON
  fi

  git -C "$dir" add -A
  git -C "$dir" commit -q -m "init"
  echo "$dir"
}

# Create a temp git repo with a CHANGELOG.md
# $1 = dir name, $2 = changelog content
make_changelog_repo() {
  local dir="$TMPDIR_BASE/$1"
  mkdir -p "$dir"
  git -C "$dir" init -q
  git -C "$dir" config user.name "Test"
  git -C "$dir" config user.email "test@test.com"
  git -C "$dir" remote add origin "https://github.com/hop-top/kit.git"

  printf '%s' "$2" > "$dir/CHANGELOG.md"
  git -C "$dir" add -A
  git -C "$dir" commit -q -m "init"
  echo "$dir"
}

PROMOTE="$SCRIPT_DIR/promote-release.sh"
REWRITE="$SCRIPT_DIR/rewrite-changelog.sh"

# ======================================================
# promote-release.sh tests
# ======================================================

info "Test 1: Initialize alpha from no stage"
DIR=$(make_promote_repo "t1")
(cd "$DIR" && bash "$PROMOTE" alpha)
assert_file_contains "$DIR/.github/release-please-config.json" \
  '"prerelease-type": "alpha"' "t1: root has alpha"

info "Test 2: Alpha → beta promotion"
DIR=$(make_promote_repo "t2" "alpha")
(cd "$DIR" && bash "$PROMOTE" beta)
assert_file_contains "$DIR/.github/release-please-config.json" \
  '"prerelease-type": "beta"' "t2: root has beta"

info "Test 3: Beta → rc promotion"
DIR=$(make_promote_repo "t3" "beta")
(cd "$DIR" && bash "$PROMOTE" rc)
assert_file_contains "$DIR/.github/release-please-config.json" \
  '"prerelease-type": "rc"' "t3: root has rc"

info "Test 4: RC → release promotion"
DIR=$(make_promote_repo "t4" "rc")
(cd "$DIR" && bash "$PROMOTE" release)
assert_file_not_contains "$DIR/.github/release-please-config.json" \
  'prerelease-type' "t4: prerelease-type removed"

info "Test 5: Invalid transition rejected (alpha → rc)"
DIR=$(make_promote_repo "t5" "alpha")
rc=0; (cd "$DIR" && bash "$PROMOTE" rc) 2>/dev/null || rc=$?
assert_exit_code 1 "$rc" "t5: alpha→rc rejected"

info "Test 6: Skip stage rejected (alpha → release)"
DIR=$(make_promote_repo "t6" "alpha")
rc=0; (cd "$DIR" && bash "$PROMOTE" release) 2>/dev/null || rc=$?
assert_exit_code 1 "$rc" "t6: alpha→release rejected"

info "Test 7: Dirty index rejected"
DIR=$(make_promote_repo "t7" "alpha")
echo "dirty" > "$DIR/staged.txt"
git -C "$DIR" add "$DIR/staged.txt"
rc=0; (cd "$DIR" && bash "$PROMOTE" beta) 2>/dev/null || rc=$?
assert_exit_code 1 "$rc" "t7: dirty index rejected"

info "Test 8: Interactive mode shows valid next stage"
DIR=$(make_promote_repo "t8" "alpha")
out=$(cd "$DIR" && echo "n" | bash "$PROMOTE" 2>&1) || true
echo "$out" | grep -q "beta" \
  && pass "t8: interactive shows beta as next" \
  || fail "t8: interactive did not show beta"

DIR=$(make_promote_repo "t8b")
out=$(cd "$DIR" && echo "n" | bash "$PROMOTE" 2>&1) || true
echo "$out" | grep -q "alpha" \
  && pass "t8b: interactive shows alpha for no-stage" \
  || fail "t8b: interactive did not show alpha"

# ======================================================
# rewrite-changelog.sh tests
# ======================================================

RAW_CHANGELOG='# Changelog

## [0.4.0](https://github.com/hop-top/kit/compare/kit-v0.3.2...kit-v0.4.0) (2026-04-18)

### Features

* **bus:** pluggable adapter (abc1234)
* **cli:** scaffolder ([#46](https://github.com/hop-top/kit/pull/46))

### Bug Fixes

* **output:** fix rendering (def5678)
'

FEATURES_ONLY='# Changelog

## [1.0.0](https://github.com/hop-top/kit/compare/kit-v0.9.0...kit-v1.0.0) (2026-04-18)

### Features

* **core:** new thing (aaa1111)
'

FIXES_ONLY='# Changelog

## [1.0.1](https://github.com/hop-top/kit/compare/kit-v1.0.0...kit-v1.0.1) (2026-04-18)

### Bug Fixes

* **core:** patch (bbb2222)
'

TWO_ENTRIES='# Changelog

## [0.5.0](https://github.com/hop-top/kit/compare/kit-v0.4.0...kit-v0.5.0) (2026-04-18)

### Features

* **new:** thing (ccc3333)

## [0.4.0](https://github.com/hop-top/kit/compare/kit-v0.3.2...kit-v0.4.0) (2026-04-17)

### Bug Fixes

* **old:** preserved entry (ddd4444)
'

# Mock gh by ensuring GH_TOKEN is unset and gh api fails gracefully
export GH_TOKEN=""

info "Test 9: Basic rewrite"
DIR=$(make_changelog_repo "t9" "$RAW_CHANGELOG")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  "team is happy" "t9: intro paragraph present"
assert_file_not_contains "$DIR/CHANGELOG.md" \
  '(abc1234)' "t9: no commit SHAs"
assert_file_not_contains "$DIR/CHANGELOG.md" \
  '\[#46\]' "t9: no PR link syntax"
assert_file_contains "$DIR/CHANGELOG.md" \
  "Full diff" "t9: full diff link"

info "Test 10: Idempotent"
DIR=$(make_changelog_repo "t10" "$RAW_CHANGELOG")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
cp "$DIR/CHANGELOG.md" "$DIR/first.md"
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
if diff -q "$DIR/first.md" "$DIR/CHANGELOG.md" >/dev/null 2>&1; then
  pass "t10: idempotent — second run unchanged"
else
  fail "t10: idempotent — output differs on second run"
fi

info "Test 11: Old entries preserved"
DIR=$(make_changelog_repo "t11" "$TWO_ENTRIES")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  "team is happy" "t11: latest entry rewritten"
assert_file_contains "$DIR/CHANGELOG.md" \
  '(ddd4444)' "t11: old entry SHA preserved"
assert_file_contains "$DIR/CHANGELOG.md" \
  'preserved entry' "t11: old entry text intact"

info "Test 12: Release type detection — features only"
DIR=$(make_changelog_repo "t12a" "$FEATURES_ONLY")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  "new features" "t12a: features-only → new features"

info "Test 12: Release type detection — fixes only"
DIR=$(make_changelog_repo "t12b" "$FIXES_ONLY")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  "maintenance release with bug fixes" "t12b: fixes-only → maintenance"

info "Test 12: Release type detection — both"
DIR=$(make_changelog_repo "t12c" "$RAW_CHANGELOG")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  "new features and bug fixes" "t12c: both → features and fixes"

info "Test 13: Already rewritten (marker present)"
DIR=$(make_changelog_repo "t13" "$RAW_CHANGELOG")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
cp "$DIR/CHANGELOG.md" "$DIR/before.md"
rc=0
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit || rc=$?
assert_exit_code 0 "$rc" "t13: already rewritten exits 0"
if diff -q "$DIR/before.md" "$DIR/CHANGELOG.md" >/dev/null 2>&1; then
  pass "t13: no changes on already-rewritten file"
else
  fail "t13: file modified despite marker"
fi

# ======================================================
# --dry-run tests
# ======================================================

info "Test 14: promote --dry-run doesn't modify config"
DIR=$(make_promote_repo "t14" "alpha")
cp "$DIR/.github/release-please-config.json" "$DIR/before.json"
(cd "$DIR" && bash "$PROMOTE" --dry-run beta)
if diff -q "$DIR/before.json" "$DIR/.github/release-please-config.json" \
  >/dev/null 2>&1; then
  pass "t14: config unchanged after --dry-run"
else
  fail "t14: config modified despite --dry-run"
fi

info "Test 15: promote --dry-run shows correct output"
DIR=$(make_promote_repo "t15" "alpha")
out=$(cd "$DIR" && bash "$PROMOTE" --dry-run beta 2>&1)
echo "$out" | grep -q '\[dry-run\] Current stage: alpha' \
  && pass "t15a: shows current stage" \
  || fail "t15a: missing current stage"
echo "$out" | grep -q '\[dry-run\] Target stage: beta' \
  && pass "t15b: shows target stage" \
  || fail "t15b: missing target stage"
echo "$out" | grep -q '\[dry-run\] Would commit' \
  && pass "t15c: shows would-commit message" \
  || fail "t15c: missing would-commit"
echo "$out" | grep -q '\[dry-run\] No changes made' \
  && pass "t15d: shows no-changes notice" \
  || fail "t15d: missing no-changes notice"

info "Test 16: promote --dry-run interactive mode"
DIR=$(make_promote_repo "t16" "beta")
out=$(cd "$DIR" && bash "$PROMOTE" --dry-run 2>&1)
echo "$out" | grep -q 'Current stage: beta' \
  && pass "t16a: interactive shows current stage" \
  || fail "t16a: missing current stage"
echo "$out" | grep -q '\[dry-run\] No changes made' \
  && pass "t16b: interactive dry-run ends cleanly" \
  || fail "t16b: missing no-changes notice"

info "Test 17: promote --dry-run with dirty index is allowed"
DIR=$(make_promote_repo "t17" "alpha")
echo "dirty" > "$DIR/staged.txt"
git -C "$DIR" add "$DIR/staged.txt"
rc=0; (cd "$DIR" && bash "$PROMOTE" --dry-run beta 2>&1) || rc=$?
assert_exit_code 0 "$rc" "t17: dry-run ignores dirty index"

info "Test 18: rewrite --dry-run doesn't modify changelog"
DIR=$(make_changelog_repo "t18" "$RAW_CHANGELOG")
cp "$DIR/CHANGELOG.md" "$DIR/before.md"
bash "$REWRITE" --dry-run --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
if diff -q "$DIR/before.md" "$DIR/CHANGELOG.md" >/dev/null 2>&1; then
  pass "t18: changelog unchanged after --dry-run"
else
  fail "t18: changelog modified despite --dry-run"
fi

info "Test 19: rewrite --dry-run shows diff"
DIR=$(make_changelog_repo "t19" "$RAW_CHANGELOG")
out=$(bash "$REWRITE" --dry-run --file "$DIR/CHANGELOG.md" \
  --component kit --repo hop-top/kit 2>&1)
echo "$out" | grep -q '\[dry-run\] Would rewrite' \
  && pass "t19a: shows would-rewrite" \
  || fail "t19a: missing would-rewrite"
echo "$out" | grep -q '\[dry-run\] Version: 0.4.0' \
  && pass "t19b: shows version" \
  || fail "t19b: missing version"
echo "$out" | grep -q '\[dry-run\] Release type:' \
  && pass "t19c: shows release type" \
  || fail "t19c: missing release type"
echo "$out" | grep -q '\[dry-run\] No changes made' \
  && pass "t19d: shows no-changes notice" \
  || fail "t19d: missing no-changes notice"
echo "$out" | grep -q '^---\|^+++\|^@@' \
  && pass "t19e: shows unified diff" \
  || fail "t19e: missing diff output"

# ======================================================
# Linked SHA stripping tests
# ======================================================

LINKED_SHA_CHANGELOG='# Changelog

## [0.6.0](https://github.com/hop-top/kit/compare/kit-v0.5.0...kit-v0.6.0) (2026-04-18)

### Features

* **bus:** pluggable adapter ([abc1234](https://github.com/hop-top/kit/commit/abc1234))
* **cli:** scaffolder ([#46](https://github.com/hop-top/kit/pull/46)) ([def5678](https://github.com/hop-top/kit/commit/def5678))

### Bug Fixes

* **core:** raw sha (aaa1111)
'

info "Test 20: Linked SHA stripping"
DIR=$(make_changelog_repo "t20" "$LINKED_SHA_CHANGELOG")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_not_contains "$DIR/CHANGELOG.md" \
  'abc1234' "t20a: linked SHA stripped"
assert_file_not_contains "$DIR/CHANGELOG.md" \
  'def5678' "t20b: combined PR+SHA stripped"
assert_file_not_contains "$DIR/CHANGELOG.md" \
  '\[#46\]' "t20c: PR link stripped"
assert_file_not_contains "$DIR/CHANGELOG.md" \
  'aaa1111' "t20d: raw SHA stripped"
assert_file_contains "$DIR/CHANGELOG.md" \
  'pluggable adapter$' "t20e: bus line is clean"
assert_file_contains "$DIR/CHANGELOG.md" \
  'scaffolder$' "t20f: cli line is clean"

# ======================================================
# Summary
# ======================================================

echo ""
echo "========================================"
printf "  Results: \033[1;32m%d passed\033[0m" "$PASS"
if [ "$FAIL" -gt 0 ]; then
  printf ", \033[1;31m%d failed\033[0m" "$FAIL"
fi
echo ""
echo "========================================"

if [ "$FAIL" -gt 0 ]; then
  echo ""
  echo "Failures:"
  for err in "${ERRORS[@]}"; do
    printf "  - %s\n" "$err"
  done
  exit 1
fi
