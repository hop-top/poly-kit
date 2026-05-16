#!/usr/bin/env bash
set -euo pipefail

# -------------------------------------------------------
# test-e2e.sh — End-to-end tests for CLI app templates
#
# Builds all templates via build.sh, then runs init.sh
# against each with test values (via env var overrides).
# Verifies: placeholder replacement, structure, module
# paths, license, git init, and polyglot pruning.
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

# --- Helpers -------------------------------------------

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

assert_file_not_contains() {
  local file="$1" pattern="$2" label="$3"
  if grep -q "$pattern" "$file" 2>/dev/null; then
    fail "$label: '$pattern' unexpectedly found in $file"
  else
    pass "$label"
  fi
}

# --- Build templates -----------------------------------

info "Building templates..."
bash "$SCRIPT_DIR/build.sh"

DIST="$SCRIPT_DIR/dist"
if [ ! -d "$DIST" ]; then
  fail "build.sh did not produce dist/"
  exit 1
fi

for tpl in cli-template-go cli-template-ts cli-template-py cli-template-polyglot; do
  assert_dir_exists "$DIST/$tpl" "dist/$tpl"
done

# --- Test values ---------------------------------------

export INIT_APP_NAME="testcli"
export INIT_DESCRIPTION="A test CLI application"
export INIT_AUTHOR_NAME="Test Author"
export INIT_AUTHOR_EMAIL="test@example.com"
export INIT_LICENSE="MIT"
export INIT_MODULE_PREFIX="github.com/testorg"

# ======================================================
# Test: Go template
# ======================================================

info "Testing Go template..."
GO_DIR="$TMPDIR_BASE/test-go"
cp -r "$DIST/cli-template-go" "$GO_DIR"

(cd "$GO_DIR" && bash init.sh)

assert_no_placeholders "$GO_DIR" "go"
assert_file_exists "$GO_DIR/LICENSE" "go LICENSE"
assert_file_exists "$GO_DIR/RELEASING.md" "go RELEASING.md"
assert_file_exists "$GO_DIR/scripts/promote-release.sh" "go promote script"
assert_file_exists "$GO_DIR/.git/config" "go git init"
assert_file_contains "$GO_DIR/go.mod" "module github.com/testorg/testcli" "go module path"
assert_file_contains "$GO_DIR/main.go" '"github.com/testorg/testcli/cmd"' "go main.go import"
assert_file_contains "$GO_DIR/cmd/root.go" '"github.com/testorg/testcli/internal/version"' "go root.go import"
assert_file_contains "$GO_DIR/cmd/root.go" 'SetEnvPrefix("TESTCLI")' "go env prefix uppercased"
assert_file_contains "$GO_DIR/.goreleaser.yml" "github.com/testorg/testcli/internal/version" "go goreleaser ldflags"
assert_file_contains "$GO_DIR/README.md" "go install github.com/testorg/testcli@latest" "go README install"
assert_file_not_contains "$GO_DIR/README.md" "{{app_name_upper}}" "go README no raw placeholder"

# Verify git identity
git_name="$(git -C "$GO_DIR" config user.name)"
git_email="$(git -C "$GO_DIR" config user.email)"
if [ "$git_name" = "Test Author" ] && [ "$git_email" = "test@example.com" ]; then
  pass "go git identity set from prompted values"
else
  fail "go git identity: got name='$git_name' email='$git_email'"
fi

# Verify license files cleaned up
if [ ! -f "$GO_DIR/LICENSE-MIT" ] && [ ! -f "$GO_DIR/LICENSE-Apache-2.0" ]; then
  pass "go license templates cleaned up"
else
  fail "go license templates not cleaned up"
fi

# Verify init.sh self-deleted
if [ ! -f "$GO_DIR/init.sh" ]; then
  pass "go init.sh self-deleted"
else
  fail "go init.sh still present"
fi

# ======================================================
# Test: TypeScript template
# ======================================================

info "Testing TypeScript template..."
TS_DIR="$TMPDIR_BASE/test-ts"
cp -r "$DIST/cli-template-ts" "$TS_DIR"

(cd "$TS_DIR" && bash init.sh)

assert_no_placeholders "$TS_DIR" "ts"
assert_file_exists "$TS_DIR/LICENSE" "ts LICENSE"
assert_file_exists "$TS_DIR/RELEASING.md" "ts RELEASING.md"
assert_file_exists "$TS_DIR/.git/config" "ts git init"
assert_file_contains "$TS_DIR/package.json" '"name": "testcli"' "ts package name"
assert_file_contains "$TS_DIR/package.json" '"license": "MIT"' "ts license in package.json"
assert_file_contains "$TS_DIR/package.json" '@typescript-eslint/parser' "ts eslint parser dep"

# ======================================================
# Test: Python template
# ======================================================

info "Testing Python template..."
PY_DIR="$TMPDIR_BASE/test-py"
cp -r "$DIST/cli-template-py" "$PY_DIR"

(cd "$PY_DIR" && bash init.sh)

assert_no_placeholders "$PY_DIR" "py"
assert_file_exists "$PY_DIR/LICENSE" "py LICENSE"
assert_file_exists "$PY_DIR/RELEASING.md" "py RELEASING.md"
assert_file_exists "$PY_DIR/.git/config" "py git init"
assert_file_contains "$PY_DIR/pyproject.toml" 'name = "testcli"' "py project name"
assert_file_contains "$PY_DIR/pyproject.toml" 'license = "MIT"' "py license in pyproject"
assert_file_contains "$PY_DIR/pyproject.toml" 'pytest-cov' "py pytest-cov in dev deps"
assert_dir_exists "$PY_DIR/src/testcli" "py src dir renamed"
assert_file_exists "$PY_DIR/tests/unit/test_placeholder.py" "py placeholder test"

# ======================================================
# Test: Polyglot template (all languages)
# ======================================================

info "Testing Polyglot template (all languages)..."
POLY_ALL_DIR="$TMPDIR_BASE/test-poly-all"
cp -r "$DIST/cli-template-polyglot" "$POLY_ALL_DIR"

export INIT_LANGUAGES="go,ts,py"
(cd "$POLY_ALL_DIR" && bash init.sh)

assert_no_placeholders "$POLY_ALL_DIR" "poly-all"
assert_file_exists "$POLY_ALL_DIR/RELEASING.md" "poly-all RELEASING.md"
assert_dir_exists "$POLY_ALL_DIR/go" "poly-all go/ dir"
assert_dir_exists "$POLY_ALL_DIR/ts" "poly-all ts/ dir"
assert_dir_exists "$POLY_ALL_DIR/py" "poly-all py/ dir"
assert_file_contains "$POLY_ALL_DIR/go/go.mod" "module github.com/testorg/testcli" "poly-all go module path"
assert_dir_exists "$POLY_ALL_DIR/py/src/testcli" "poly-all py src renamed"
assert_file_contains "$POLY_ALL_DIR/.github/dependabot.yml" '"/go"' "poly-all dependabot watches /go"
assert_file_contains "$POLY_ALL_DIR/.github/dependabot.yml" '"/ts"' "poly-all dependabot watches /ts"
assert_file_contains "$POLY_ALL_DIR/.github/dependabot.yml" '"/py"' "poly-all dependabot watches /py"

# ======================================================
# Test: Polyglot template (prune to Go only)
# ======================================================

info "Testing Polyglot template (Go only — prune ts,py)..."
POLY_GO_DIR="$TMPDIR_BASE/test-poly-go"
cp -r "$DIST/cli-template-polyglot" "$POLY_GO_DIR"

export INIT_LANGUAGES="go"
(cd "$POLY_GO_DIR" && bash init.sh)

assert_dir_exists "$POLY_GO_DIR/go" "poly-go go/ kept"

if [ ! -d "$POLY_GO_DIR/ts" ]; then
  pass "poly-go ts/ pruned"
else
  fail "poly-go ts/ not pruned"
fi

if [ ! -d "$POLY_GO_DIR/py" ]; then
  pass "poly-go py/ pruned"
else
  fail "poly-go py/ not pruned"
fi

# Verify CI workflows pruned
if [ ! -f "$POLY_GO_DIR/.github/workflows/ci-ts.yml" ]; then
  pass "poly-go ts CI workflow pruned"
else
  fail "poly-go ts CI workflow not pruned"
fi

if [ ! -f "$POLY_GO_DIR/.github/workflows/ci-py.yml" ]; then
  pass "poly-go py CI workflow pruned"
else
  fail "poly-go py CI workflow not pruned"
fi

assert_file_exists "$POLY_GO_DIR/.github/workflows/ci-go.yml" "poly-go go CI workflow kept"

# Verify Makefile pruned correctly (no ts or py delegation)
assert_file_not_contains "$POLY_GO_DIR/Makefile" 'MAKE) -C ts' "poly-go Makefile no ts delegation"
assert_file_not_contains "$POLY_GO_DIR/Makefile" 'MAKE) -C py' "poly-go Makefile no py delegation"
assert_file_contains "$POLY_GO_DIR/Makefile" 'MAKE) -C go' "poly-go Makefile has go delegation"

# ======================================================
# Test: Polyglot template (prune to ts+py)
# ======================================================

info "Testing Polyglot template (ts+py — prune go)..."
POLY_TSPY_DIR="$TMPDIR_BASE/test-poly-tspy"
cp -r "$DIST/cli-template-polyglot" "$POLY_TSPY_DIR"

export INIT_LANGUAGES="ts,py"
(cd "$POLY_TSPY_DIR" && bash init.sh)

if [ ! -d "$POLY_TSPY_DIR/go" ]; then
  pass "poly-tspy go/ pruned"
else
  fail "poly-tspy go/ not pruned"
fi

assert_dir_exists "$POLY_TSPY_DIR/ts" "poly-tspy ts/ kept"
assert_dir_exists "$POLY_TSPY_DIR/py" "poly-tspy py/ kept"
assert_file_not_contains "$POLY_TSPY_DIR/Makefile" 'MAKE) -C go' "poly-tspy Makefile no go delegation"

# ======================================================
# Test: Apache-2.0 license
# ======================================================

info "Testing Apache-2.0 license selection..."
APACHE_DIR="$TMPDIR_BASE/test-apache"
cp -r "$DIST/cli-template-go" "$APACHE_DIR"

export INIT_LICENSE="Apache-2.0"
(cd "$APACHE_DIR" && bash init.sh)

assert_file_contains "$APACHE_DIR/LICENSE" "Apache License" "apache license content"

# ======================================================
# Summary
# ======================================================

unset INIT_APP_NAME INIT_DESCRIPTION INIT_AUTHOR_NAME INIT_AUTHOR_EMAIL
unset INIT_LICENSE INIT_MODULE_PREFIX INIT_LANGUAGES

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
