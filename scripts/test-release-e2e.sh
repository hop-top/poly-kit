#!/usr/bin/env bash
set -euo pipefail

# -------------------------------------------------------
# test-release-e2e.sh — E2e tests for the release promotion wrapper
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

assert_eq() {
  local got="$1" want="$2" label="$3"
  if [ "$got" = "$want" ]; then
    pass "$label"
  else
    fail "$label: got '$got', want '$want'"
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
#
# promote-release.sh hands its arguments to hop-top/.github's promote.js;
# the promotion rules are that tool's (and its tests'). These cover the
# wrapper: the pin it reads, the pass-through, the exit status. A stub
# extension directory and a stub NODE keep them offline.

STUB_EXT="$TMPDIR_BASE/stub-ext"
STUB_NODE="$TMPDIR_BASE/stub-node"
STUB_LOG="$TMPDIR_BASE/stub-node.log"
mkdir -p "$STUB_EXT/bin"
echo "// stub" > "$STUB_EXT/bin/promote.js"
cat > "$STUB_NODE" <<'EOSTUB'
#!/usr/bin/env bash
# Records the working directory and every argument, one per line.
{ pwd; printf '%s\n' "$@"; } > "$STUB_LOG"
exit "${STUB_EXIT:-0}"
EOSTUB
chmod +x "$STUB_NODE"
export STUB_LOG

# Copy of the wrapper in a throwaway repo layout whose release-please.yml
# holds $1 as its body.
make_pin_repo() {
  local dir="$TMPDIR_BASE/$2"
  mkdir -p "$dir/scripts" "$dir/.github/workflows"
  cp "$PROMOTE" "$dir/scripts/promote-release.sh"
  printf '%s\n' "$1" > "$dir/.github/workflows/release-please.yml"
  echo "$dir"
}

info "Test 1: --pin prints the ref release-please.yml calls the extension at"
want=$(sed -n 's|.*release-please-on-push\.yml@\([0-9a-f]*\).*|\1|p' \
  "$SCRIPT_DIR/../.github/workflows/release-please.yml")
got=$(bash "$PROMOTE" --pin)
assert_eq "$got" "$want" "t1: pin as release-please.yml has it"
if [[ "$got" =~ ^[0-9a-f]{40}$ ]]; then
  pass "t1: pin is a full commit SHA"
else
  fail "t1: pin '$got' is not a full commit SHA"
fi

info "Test 2: arguments reach promote.js unchanged, from the repo root"
rc=0
(cd "$TMPDIR_BASE" && RELEASE_PLEASE_TARGET_DIR="$STUB_EXT" NODE="$STUB_NODE" \
  bash "$PROMOTE" channel beta --package kit --package 'a b' --dry-run) || rc=$?
assert_exit_code 0 "$rc" "t2: exit 0"
expected=$(printf '%s\n' "$(cd "$SCRIPT_DIR/.." && pwd)" \
  "$STUB_EXT/bin/promote.js" channel beta --package kit --package 'a b' --dry-run)
assert_eq "$(cat "$STUB_LOG")" "$expected" "t2: argv and cwd as given"

info "Test 3: promote.js's exit status is the wrapper's"
for code in 1 2; do
  rc=0
  RELEASE_PLEASE_TARGET_DIR="$STUB_EXT" NODE="$STUB_NODE" STUB_EXIT=$code \
    bash "$PROMOTE" channel rc --group kit 2>/dev/null || rc=$?
  assert_exit_code "$code" "$rc" "t3: exit $code passed through"
done

info "Test 4: an extension directory without promote.js fails setup"
rc=0
out=$(RELEASE_PLEASE_TARGET_DIR="$TMPDIR_BASE/nowhere" NODE="$STUB_NODE" \
  bash "$PROMOTE" channel beta --package kit 2>&1) || rc=$?
assert_exit_code 3 "$rc" "t4: exit 3"
if echo "$out" | grep -q 'nowhere/bin/promote.js not found'; then
  pass "t4: names the missing file"
else
  fail "t4: message: $out"
fi

info "Test 5: a missing, doubled or malformed pin fails setup"
DIR=$(make_pin_repo "name: release-please" t5a)
rc=0; out=$(bash "$DIR/scripts/promote-release.sh" --pin 2>&1) || rc=$?
assert_exit_code 3 "$rc" "t5a: no release-please-on-push call"
assert_eq "${out#*: }" "no hop-top/.github release-please-on-push.yml call in $DIR/.github/workflows/release-please.yml" "t5a: message"
DIR=$(make_pin_repo "$(printf '%s\n' \
  '    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@v0' \
  '    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@v1')" t5b)
rc=0; out=$(bash "$DIR/scripts/promote-release.sh" --pin 2>&1) || rc=$?
assert_exit_code 3 "$rc" "t5b: two calls"
assert_eq "${out#*: }" "more than one release-please-on-push.yml call in $DIR/.github/workflows/release-please.yml" "t5b: message"
# shellcheck disable=SC2016 # the literal `$(id)` is the malformed ref
DIR=$(make_pin_repo \
  '    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@$(id)' t5c)
rc=0; bash "$DIR/scripts/promote-release.sh" --pin 2>/dev/null || rc=$?
assert_exit_code 3 "$rc" "t5c: not a ref"
DIR=$(make_pin_repo \
  '    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@v0  # pin' t5d)
got=$(bash "$DIR/scripts/promote-release.sh" --pin)
assert_eq "$got" "v0" "t5d: trailing comment ignored"

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
assert_file_contains "$DIR/CHANGELOG.md" \
  'pluggable adapter (abc1234)$' "t9: commit SHA kept"
assert_file_contains "$DIR/CHANGELOG.md" \
  'scaffolder (\[#46\](https://github.com/hop-top/kit/pull/46))$' "t9: PR link kept"
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
# Linked SHA and PR link tests
# ======================================================

LINKED_SHA_CHANGELOG='# Changelog

## [0.6.0](https://github.com/hop-top/kit/compare/kit-v0.5.0...kit-v0.6.0) (2026-04-18)

### Features

* **bus:** pluggable adapter ([abc1234](https://github.com/hop-top/kit/commit/abc1234))
* **cli:** scaffolder ([#46](https://github.com/hop-top/kit/pull/46)) ([def5678](https://github.com/hop-top/kit/commit/def5678))

### Bug Fixes

* **core:** raw sha (aaa1111)
'

info "Test 20: Linked SHAs and PR links kept"
DIR=$(make_changelog_repo "t20" "$LINKED_SHA_CHANGELOG")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  'pluggable adapter (\[abc1234\](https://github.com/hop-top/kit/commit/abc1234))$' \
  "t20a: linked SHA kept"
assert_file_contains "$DIR/CHANGELOG.md" \
  'scaffolder (\[#46\](https://github.com/hop-top/kit/pull/46)) (\[def5678\](https://github.com/hop-top/kit/commit/def5678))$' \
  "t20b: combined PR+SHA links kept"
assert_file_contains "$DIR/CHANGELOG.md" \
  'raw sha (aaa1111)$' "t20c: raw SHA kept"

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
