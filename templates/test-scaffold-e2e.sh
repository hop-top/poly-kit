#!/usr/bin/env bash
set -euo pipefail

# -------------------------------------------------------
# test-scaffold-e2e.sh — E2E tests for scaffold.sh + reserve-packages
#
# 10 tests covering: single-lang scaffold, polyglot scaffold,
# error cases (missing name, invalid lang/license, existing dir,
# missing flag value), --no-push skipping reservation, and
# reserve-packages unit behavior (npm unauthed, PyPI name taken).
# Skips forge/tlc tests if CLIs not authenticated.
# -------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMPDIR_BASE="$(mktemp -d)"
PASS=0
FAIL=0
ERRORS=()

cleanup() {
  rm -rf "$TMPDIR_BASE"
}
trap cleanup EXIT

# --- Helpers (reused from test-e2e.sh) -----------------

info()  { printf "\033[1;34m[INFO]\033[0m  %s\n" "$*"; }
pass()  { printf "\033[1;32m[PASS]\033[0m  %s\n" "$*"; PASS=$((PASS + 1)); }
fail()  { printf "\033[1;31m[FAIL]\033[0m  %s\n" "$*"; FAIL=$((FAIL + 1)); ERRORS+=("$*"); }

assert_file_exists() {
  local file="$1" label="${2:-$1}"
  if [ -f "$file" ]; then
    pass "$label exists"
  else
    fail "$label missing: $file"
  fi
}

assert_dir_exists() {
  local dir="$1" label="${2:-$1}"
  if [ -d "$dir" ]; then
    pass "$label exists"
  else
    fail "$label missing: $dir"
  fi
}

assert_no_placeholders() {
  local dir="$1" label="$2"
  local found
  found="$(grep -rl '{{[a-z_]*}}' "$dir" \
    --include='*.go' --include='*.ts' --include='*.py' \
    --include='*.json' --include='*.toml' --include='*.yml' \
    --include='*.yaml' --include='*.md' \
    2>/dev/null || true)"
  if [ -z "$found" ]; then
    pass "$label: no unreplaced placeholders"
  else
    fail "$label: unreplaced placeholders in: $found"
  fi
}

assert_file_contains() {
  local file="$1" pattern="$2" label="$3"
  if grep -q "$pattern" "$file" 2>/dev/null; then
    pass "$label"
  else
    fail "$label: '$pattern' not found in $file"
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

# ======================================================
# Test 1: Single-lang Go with --no-push
# ======================================================

info "Test 1: Single-lang Go with --no-push"
TEST1_DIR="$TMPDIR_BASE/test1"

bash "$SCRIPT_DIR/scaffold.sh" test1app \
  --output "$TEST1_DIR" \
  --lang go \
  --description "Test Go CLI" \
  --license mit \
  --author "Test Author" \
  --email "test@example.com" \
  --module-prefix "github.com/testorg" \
  --forge none \
  --no-push

assert_dir_exists "$TEST1_DIR" "test1 project dir"
assert_file_exists "$TEST1_DIR/Makefile" "test1 Makefile"
assert_file_exists "$TEST1_DIR/.git/config" "test1 git init"
assert_file_exists "$TEST1_DIR/.github/copilot-instructions.md" \
  "test1 copilot instructions"
assert_no_placeholders "$TEST1_DIR" "test1"
assert_file_contains "$TEST1_DIR/.github/copilot-instructions.md" \
  "Go" "test1 copilot mentions Go"

# ======================================================
# Test 2: Polyglot with --no-push
# ======================================================

info "Test 2: Polyglot with --no-push"
TEST2_DIR="$TMPDIR_BASE/test2"

bash "$SCRIPT_DIR/scaffold.sh" test2app \
  --output "$TEST2_DIR" \
  --lang go,ts,py \
  --description "Test polyglot CLI" \
  --license apache \
  --author "Test Author" \
  --email "test@example.com" \
  --module-prefix "github.com/testorg" \
  --forge none \
  --no-push

assert_dir_exists "$TEST2_DIR" "test2 project dir"
assert_dir_exists "$TEST2_DIR/go" "test2 go/ dir"
assert_dir_exists "$TEST2_DIR/ts" "test2 ts/ dir"
assert_dir_exists "$TEST2_DIR/py" "test2 py/ dir"
assert_file_exists "$TEST2_DIR/Makefile" "test2 root Makefile"
assert_file_contains "$TEST2_DIR/Makefile" 'MAKE) -C go' \
  "test2 Makefile delegates to go"
assert_no_placeholders "$TEST2_DIR" "test2"

# ======================================================
# Test 3: Missing name -> exit 1
# ======================================================

info "Test 3: Missing name -> exit 1"
EXIT_CODE=0
echo "" | bash "$SCRIPT_DIR/scaffold.sh" \
  --forge none --no-push 2>/dev/null || EXIT_CODE=$?
assert_exit_code 1 "$EXIT_CODE" "test3 missing name exits 1"

# ======================================================
# Test 4: Invalid lang -> exit 1
# ======================================================

info "Test 4: Invalid lang -> exit 1"
EXIT_CODE=0
bash "$SCRIPT_DIR/scaffold.sh" badlangapp \
  --output "$TMPDIR_BASE/test4" \
  --lang rust \
  --forge none \
  --no-push 2>/dev/null || EXIT_CODE=$?
assert_exit_code 1 "$EXIT_CODE" "test4 invalid lang exits 1"

# ======================================================
# Test 5: Output dir exists -> exit 1
# ======================================================

info "Test 5: Output dir exists -> exit 1"
mkdir -p "$TMPDIR_BASE/test5"
EXIT_CODE=0
bash "$SCRIPT_DIR/scaffold.sh" existsapp \
  --output "$TMPDIR_BASE/test5" \
  --lang go \
  --forge none \
  --no-push 2>/dev/null || EXIT_CODE=$?
assert_exit_code 1 "$EXIT_CODE" "test5 existing output exits 1"

# ======================================================
# Test 6: Missing flag value -> exit 1 (BUG-1)
# ======================================================

info "Test 6: Missing flag value -> exit 1"
EXIT_CODE=0
bash "$SCRIPT_DIR/scaffold.sh" test6app \
  --output 2>/dev/null || EXIT_CODE=$?
assert_exit_code 1 "$EXIT_CODE" "test6 missing flag value exits 1"

# ======================================================
# Test 7: Invalid license -> exit 1 (BUG-2)
# ======================================================

info "Test 7: Invalid license -> exit 1"
EXIT_CODE=0
bash "$SCRIPT_DIR/scaffold.sh" test7app \
  --output "$TMPDIR_BASE/test7" \
  --license bogus \
  --forge none \
  --no-push 2>/dev/null || EXIT_CODE=$?
assert_exit_code 1 "$EXIT_CODE" "test7 invalid license exits 1"

# ======================================================
# Test 8: --no-push skips package reservation
# ======================================================

info "Test 8: --no-push skips package reservation"
TEST8_DIR="$TMPDIR_BASE/test8"
TEST8_OUT="$(bash "$SCRIPT_DIR/scaffold.sh" test8app \
  --output "$TEST8_DIR" \
  --lang ts,py \
  --description "Test no-push skips reserve" \
  --author "Test Author" \
  --email "test@example.com" \
  --module-prefix "github.com/testorg" \
  --forge none \
  --no-push 2>&1)"
if echo "$TEST8_OUT" | grep -q "Checking package name availability"; then
  fail "test8 --no-push should skip package reservation"
else
  pass "test8 --no-push skips package reservation"
fi

# ======================================================
# Test 9: npm not authenticated -> skips gracefully
# ======================================================

info "Test 9: npm not authenticated -> skips gracefully"
# Source reserve-packages.sh to test _reserve_npm directly
source "$SCRIPT_DIR/reserve-packages.sh"

# Create a fake ts dir with package.json
TEST9_DIR="$TMPDIR_BASE/test9_ts"
mkdir -p "$TEST9_DIR"
echo '{"name": "@test-scaffold-e2e/nonexistent-pkg-12345"}' > "$TEST9_DIR/package.json"

# Override npm to simulate unauthenticated state
_npm_orig="$(command -v npm 2>/dev/null || true)"
npm() { return 1; }
export -f npm

RESERVE_OUT="$(_reserve_npm "$TEST9_DIR" 2>&1)"
if echo "$RESERVE_OUT" | grep -q "not authenticated"; then
  pass "test9 npm unauthenticated prints skip message"
else
  fail "test9 expected 'not authenticated' message, got: $RESERVE_OUT"
fi

# Restore npm
unset -f npm

# ======================================================
# Test 10: PyPI package exists -> prints "already exists"
# ======================================================

info "Test 10: PyPI package exists -> prints 'already exists'"

# Create a fake py dir with pyproject.toml using a known-existing package
TEST10_DIR="$TMPDIR_BASE/test10_py"
mkdir -p "$TEST10_DIR"
cat > "$TEST10_DIR/pyproject.toml" <<'TOML'
[project]
name = "requests"
version = "0.0.1"
TOML

RESERVE_OUT="$(_reserve_pypi "$TEST10_DIR" 2>&1)"
if echo "$RESERVE_OUT" | grep -q "already exists"; then
  pass "test10 PyPI existing package prints 'already exists'"
else
  fail "test10 expected 'already exists' message, got: $RESERVE_OUT"
fi

# ======================================================
# Test 11: Single-lang Rust with --no-push
# ======================================================

info "Test 11: Single-lang Rust with --no-push"
TEST11_DIR="$TMPDIR_BASE/test11"

bash "$SCRIPT_DIR/scaffold.sh" test11app \
  --output "$TEST11_DIR" \
  --lang rs \
  --description "Test Rust CLI" \
  --license mit \
  --author "Test Author" \
  --email "test@example.com" \
  --module-prefix "github.com/testorg" \
  --forge none \
  --no-push

assert_dir_exists "$TEST11_DIR" "test11 project dir"
assert_file_exists "$TEST11_DIR/.git/config" "test11 git init"
assert_file_exists "$TEST11_DIR/.github/copilot-instructions.md" \
  "test11 copilot instructions"
assert_file_contains "$TEST11_DIR/.github/copilot-instructions.md" \
  "Rust" "test11 copilot mentions Rust"
assert_file_exists "$TEST11_DIR/.github/workflows/ci.yml" \
  "test11 ci.yml"
assert_file_contains "$TEST11_DIR/.github/workflows/ci.yml" \
  "cargo" "test11 ci.yml mentions cargo"
assert_file_exists "$TEST11_DIR/.github/dependabot.yml" \
  "test11 dependabot.yml"
assert_file_contains "$TEST11_DIR/.github/dependabot.yml" \
  "cargo" "test11 dependabot has cargo ecosystem"
assert_file_exists "$TEST11_DIR/release-please-config.json" \
  "test11 release-please-config.json"
assert_file_contains "$TEST11_DIR/release-please-config.json" \
  '"release-type": "rust"' "test11 release-please uses rust release-type"
assert_file_contains "$TEST11_DIR/release-please-config.json" \
  '"path": "Cargo.toml"' "test11 release-please tracks Cargo.toml"
assert_file_exists "$TEST11_DIR/.github/workflows/release-please.yml" \
  "test11 release-please workflow"
assert_file_contains "$TEST11_DIR/.github/workflows/release-please.yml" \
  "release-rs:" "test11 release workflow has release-rs job"

# ======================================================
# Test 12: Polyglot with rs (go,ts,py,rs)
# ======================================================

info "Test 12: Polyglot including rs"
TEST12_DIR="$TMPDIR_BASE/test12"

bash "$SCRIPT_DIR/scaffold.sh" test12app \
  --output "$TEST12_DIR" \
  --lang go,ts,py,rs \
  --description "Test polyglot+rs" \
  --license apache \
  --author "Test Author" \
  --email "test@example.com" \
  --module-prefix "github.com/testorg" \
  --forge none \
  --no-push

assert_dir_exists "$TEST12_DIR/rs" "test12 rs/ dir"
assert_file_contains "$TEST12_DIR/Makefile" 'MAKE) -C rs' \
  "test12 Makefile delegates to rs"
assert_file_contains "$TEST12_DIR/.github/dependabot.yml" \
  "cargo" "test12 polyglot dependabot includes cargo"

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
