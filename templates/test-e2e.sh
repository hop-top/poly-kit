#!/usr/bin/env bash
set -euo pipefail

# -------------------------------------------------------
# test-e2e.sh — End-to-end tests for CLI app templates
#
# Builds all templates via build.sh, then runs init.sh
# against each with test values (via env var overrides).
# Verifies: placeholder replacement, structure, module
# paths, license, git init, and polyglot composition
# (scaffold.sh --lang subsets).
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

# assert_clean_init <dir> <label>: init.sh's first commit holds no
# scaffolding helper and leaves nothing uncommitted.
assert_clean_init() {
  local dir="$1" label="$2" tracked dirty
  tracked="$(git -C "$dir" ls-tree -r --name-only HEAD 2>&1)"
  if printf '%s\n' "$tracked" | grep -qxE 'init\.sh|lib\.sh'; then
    fail "$label: first commit tracks init.sh/lib.sh"
  else
    pass "$label: first commit has no init.sh/lib.sh"
  fi
  dirty="$(git -C "$dir" status --porcelain 2>&1)"
  if [ -z "$dirty" ]; then
    pass "$label: tree clean after init"
  else
    fail "$label: tree dirty after init: $dirty"
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

for tpl in cli-template-go cli-template-ts cli-template-py cli-template-base; do
  assert_dir_exists "$DIST/$tpl" "dist/$tpl"
done

# Every built template ships lib.sh next to init.sh so a copied or
# cloned template inits standalone, with no templates/ checkout around.
for tpl in "$DIST"/cli-template-*; do
  assert_file_exists "$tpl/lib.sh" "$(basename "$tpl") lib.sh"
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
assert_clean_init "$GO_DIR" "go"
assert_file_exists "$GO_DIR/LICENSE" "go LICENSE"
assert_file_exists "$GO_DIR/RELEASING.md" "go RELEASING.md"
assert_file_exists "$GO_DIR/scripts/promote-release.sh" "go promote script"
assert_file_exists "$GO_DIR/.git/config" "go git init"
assert_file_contains "$GO_DIR/go.mod" "module github.com/testorg/testcli" "go module path"
assert_file_contains "$GO_DIR/main.go" '"github.com/testorg/testcli/cmd"' "go main.go import"
assert_file_contains "$GO_DIR/cmd/root.go" '"github.com/testorg/testcli/internal/version"' "go root.go import"
assert_file_contains "$GO_DIR/cmd/root.go" 'config.BindEnv(r.Viper, "TESTCLI")' "go env prefix uppercased"
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

# Verify lib.sh (scaffolding helpers) removed with init.sh
if [ ! -f "$GO_DIR/lib.sh" ]; then
  pass "go lib.sh removed"
else
  fail "go lib.sh still present"
fi

# ======================================================
# Test: TypeScript template
# ======================================================

info "Testing TypeScript template..."
TS_DIR="$TMPDIR_BASE/test-ts"
cp -r "$DIST/cli-template-ts" "$TS_DIR"

(cd "$TS_DIR" && bash init.sh)

assert_no_placeholders "$TS_DIR" "ts"
assert_clean_init "$TS_DIR" "ts"
assert_file_exists "$TS_DIR/LICENSE" "ts LICENSE"
assert_file_exists "$TS_DIR/RELEASING.md" "ts RELEASING.md"
assert_file_exists "$TS_DIR/.git/config" "ts git init"
assert_file_contains "$TS_DIR/package.json" '"name": "testcli"' "ts package name"
assert_file_contains "$TS_DIR/package.json" '"license": "MIT"' "ts license in package.json"
assert_file_contains "$TS_DIR/package.json" '"typescript-eslint"' "ts typescript-eslint dep"

# ======================================================
# Test: Python template
# ======================================================

info "Testing Python template..."
PY_DIR="$TMPDIR_BASE/test-py"
cp -r "$DIST/cli-template-py" "$PY_DIR"

(cd "$PY_DIR" && bash init.sh)

assert_no_placeholders "$PY_DIR" "py"
assert_clean_init "$PY_DIR" "py"
assert_file_exists "$PY_DIR/LICENSE" "py LICENSE"
assert_file_exists "$PY_DIR/RELEASING.md" "py RELEASING.md"
assert_file_exists "$PY_DIR/.git/config" "py git init"
assert_file_contains "$PY_DIR/pyproject.toml" 'name = "testcli"' "py project name"
assert_file_contains "$PY_DIR/pyproject.toml" 'license = "MIT"' "py license in pyproject"
assert_file_contains "$PY_DIR/pyproject.toml" 'pytest-cov' "py pytest-cov in dev deps"
assert_dir_exists "$PY_DIR/src/testcli" "py src dir renamed"
assert_file_exists "$PY_DIR/tests/unit/test_placeholder.py" "py placeholder test"

# ======================================================
# Test: Polyglot scaffold (all languages)
# ======================================================
# build.sh ships no polyglot template: scaffold.sh composes one from
# cli-template-base plus the --lang subset (templates/lib.sh), so the
# polyglot cases scaffold instead of copying a dist tree.

# scaffold_polyglot <dir> <langs>
scaffold_polyglot() {
  bash "$SCRIPT_DIR/scaffold.sh" "$INIT_APP_NAME" \
    --output "$1" \
    --lang "$2" \
    --description "$INIT_DESCRIPTION" \
    --license mit \
    --author "$INIT_AUTHOR_NAME" \
    --email "$INIT_AUTHOR_EMAIL" \
    --module-prefix "$INIT_MODULE_PREFIX" \
    --forge none \
    --no-push \
    --no-tlc >/dev/null
}

info "Testing polyglot scaffold (go,ts,py)..."
POLY_ALL_DIR="$TMPDIR_BASE/test-poly-all"
scaffold_polyglot "$POLY_ALL_DIR" "go,ts,py"

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

# scaffold.sh leaves no scaffolding helpers behind, in the project or
# beside it.
for f in "$POLY_ALL_DIR/init.sh" "$POLY_ALL_DIR/lib.sh" "$TMPDIR_BASE/lib.sh"; do
  if [ ! -e "$f" ]; then
    pass "poly-all no leftover ${f#"$TMPDIR_BASE"/}"
  else
    fail "poly-all leftover $f"
  fi
done

# ======================================================
# Test: Polyglot scaffold (go,py — ts left out)
# ======================================================

info "Testing polyglot scaffold (go,py — no ts)..."
POLY_GOPY_DIR="$TMPDIR_BASE/test-poly-gopy"
scaffold_polyglot "$POLY_GOPY_DIR" "go,py"

assert_dir_exists "$POLY_GOPY_DIR/go" "poly-gopy go/ kept"
assert_dir_exists "$POLY_GOPY_DIR/py" "poly-gopy py/ kept"

if [ ! -d "$POLY_GOPY_DIR/ts" ]; then
  pass "poly-gopy ts/ not composed"
else
  fail "poly-gopy ts/ composed"
fi

# Verify CI workflows composed for chosen langs only
if [ ! -f "$POLY_GOPY_DIR/.github/workflows/ci-ts.yml" ]; then
  pass "poly-gopy ts CI workflow not composed"
else
  fail "poly-gopy ts CI workflow composed"
fi

assert_file_exists "$POLY_GOPY_DIR/.github/workflows/ci-go.yml" "poly-gopy go CI workflow"
assert_file_exists "$POLY_GOPY_DIR/.github/workflows/ci-py.yml" "poly-gopy py CI workflow"

# Verify Makefile delegates to chosen langs only
assert_file_not_contains "$POLY_GOPY_DIR/Makefile" 'MAKE) -C ts' "poly-gopy Makefile no ts delegation"
assert_file_contains "$POLY_GOPY_DIR/Makefile" 'MAKE) -C go' "poly-gopy Makefile has go delegation"
assert_file_contains "$POLY_GOPY_DIR/Makefile" 'MAKE) -C py' "poly-gopy Makefile has py delegation"

# ======================================================
# Test: Polyglot scaffold (ts,py — go left out)
# ======================================================

info "Testing polyglot scaffold (ts,py — no go)..."
POLY_TSPY_DIR="$TMPDIR_BASE/test-poly-tspy"
scaffold_polyglot "$POLY_TSPY_DIR" "ts,py"

if [ ! -d "$POLY_TSPY_DIR/go" ]; then
  pass "poly-tspy go/ not composed"
else
  fail "poly-tspy go/ composed"
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
unset INIT_LICENSE INIT_MODULE_PREFIX

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
