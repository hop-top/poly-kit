#!/usr/bin/env bash
set -euo pipefail

# -------------------------------------------------------
# test-scaffold-e2e.sh — E2E tests for scaffold.sh + reserve-packages
#
# Tests covering: single-lang scaffold (go/ts/py/rs), polyglot
# scaffold (go,ts,py and go,ts,py,rs), error cases (missing name,
# invalid lang/license, existing dir, missing flag value),
# --no-push skipping reservation, reserve-packages unit behavior
# (npm unauthed, PyPI name taken), and per-spec emitter
# assertions via `assert_managed_files`: mise.toml shape,
# .devcontainer/devcontainer.json compose-mode, docker-compose.yml
# telemetry + opted-in services, otel-config.yaml,
# .env.example managed blocks, and CI workflows on jdx/mise-action.
# Skips forge/tlc tests if CLIs not authenticated; missing tools
# (jq, docker compose) gracefully skip relevant sub-assertions.
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

skip() { printf "\033[1;33m[SKIP]\033[0m  %s\n" "$*"; }

assert_file_excludes() {
  local file="$1" pattern="$2" label="$3"
  if grep -q "$pattern" "$file" 2>/dev/null; then
    fail "$label: '$pattern' unexpectedly found in $file"
  else
    pass "$label"
  fi
}

# ------------------------------------------------------
# assert_managed_files <project-dir> <lang-csv> [<services-csv>] [<project-name>]
#
# Validates the per-spec emitter outputs:
#   - mise.toml shape + per-lang gating + tasks.install
#   - .devcontainer/devcontainer.json compose-mode + mise feature
#   - .devcontainer/docker-compose.yml telemetry + opted-in services
#   - .devcontainer/otel-config.yaml receivers/exporters
#   - .env.example five kit-managed blocks + queue driver
#   - .github/workflows/*.yml use mise-action, no legacy setup-*
#
# <project-name> defaults to basename(<project-dir>). Tests that
# scaffold with --output pointing to a different basename than the
# project arg must pass it explicitly so OTEL_SERVICE_NAME matches.
# ------------------------------------------------------
# shellcheck disable=SC2120
assert_managed_files() {
  local proj="$1" lang_csv="$2" services_csv="${3:-}" name_override="${4:-}"
  local tag="${proj##*/}"
  local pname="${name_override:-$tag}"

  # Normalize lang membership: ",<l>," substring tests.
  local lang_norm=",${lang_csv//[[:space:]]/},"

  # ---------- a. mise.toml ----------
  local mise="$proj/mise.toml"
  assert_file_exists "$mise" "$tag mise.toml"
  if [ -f "$mise" ]; then
    assert_file_contains "$mise" '# >>> kit-managed >>>' \
      "$tag mise.toml has kit-managed open marker"
    assert_file_contains "$mise" '# <<< kit-managed <<<' \
      "$tag mise.toml has kit-managed close marker"

    # Per-lang runtime gating: present iff lang opted in.
    if [[ "$lang_norm" == *,go,* ]]; then
      assert_file_contains "$mise" '^go = "' "$tag mise.toml has go runtime"
    else
      assert_file_excludes "$mise" '^go = "' "$tag mise.toml omits go runtime"
    fi
    if [[ "$lang_norm" == *,ts,* ]]; then
      assert_file_contains "$mise" '^node = "' "$tag mise.toml has node runtime"
      assert_file_contains "$mise" '^pnpm = "' "$tag mise.toml has pnpm runtime"
    else
      assert_file_excludes "$mise" '^node = "' "$tag mise.toml omits node"
      assert_file_excludes "$mise" '^pnpm = "' "$tag mise.toml omits pnpm"
    fi
    if [[ "$lang_norm" == *,py,* ]]; then
      assert_file_contains "$mise" '^python = "' "$tag mise.toml has python runtime"
      assert_file_contains "$mise" '^uv = "' "$tag mise.toml has uv runtime"
    else
      assert_file_excludes "$mise" '^python = "' "$tag mise.toml omits python"
      assert_file_excludes "$mise" '^uv = "' "$tag mise.toml omits uv"
    fi
    if [[ "$lang_norm" == *,rs,* ]]; then
      assert_file_contains "$mise" '^rust = "' "$tag mise.toml has rust runtime"
    else
      assert_file_excludes "$mise" '^rust = "' "$tag mise.toml omits rust"
    fi

    # Always-present workflow tools.
    assert_file_contains "$mise" '^lychee = "'     "$tag mise.toml has lychee"
    assert_file_contains "$mise" '^hadolint = "'   "$tag mise.toml has hadolint"
    assert_file_contains "$mise" '^actionlint = "' "$tag mise.toml has actionlint"
    assert_file_contains "$mise" '^shellcheck = "' "$tag mise.toml has shellcheck"
    assert_file_contains "$mise" '^shfmt = "'      "$tag mise.toml has shfmt"
    assert_file_contains "$mise" '"npm:release-please" = "' \
      "$tag mise.toml has release-please"

    # [tasks.install] with run = [...] entries (one per lang).
    assert_file_contains "$mise" '^\[tasks\.install\]' \
      "$tag mise.toml has [tasks.install] block"
    assert_file_contains "$mise" '^run = \[' \
      "$tag mise.toml [tasks.install] has run array"
    if [[ "$lang_norm" == *,go,* ]]; then
      assert_file_contains "$mise" '"go mod download"' \
        "$tag mise.toml install runs go mod download"
    fi
    if [[ "$lang_norm" == *,ts,* ]]; then
      assert_file_contains "$mise" '"pnpm install"' \
        "$tag mise.toml install runs pnpm install"
    fi
    if [[ "$lang_norm" == *,py,* ]]; then
      assert_file_contains "$mise" '"uv sync"' \
        "$tag mise.toml install runs uv sync"
    fi
    if [[ "$lang_norm" == *,rs,* ]]; then
      assert_file_contains "$mise" '"cargo fetch"' \
        "$tag mise.toml install runs cargo fetch"
    fi
  fi

  # ---------- b. devcontainer.json ----------
  local dc="$proj/.devcontainer/devcontainer.json"
  assert_file_exists "$dc" "$tag devcontainer.json"
  if [ -f "$dc" ]; then
    assert_file_contains "$dc" '"dockerComposeFile": "docker-compose.yml"' \
      "$tag devcontainer.json uses dockerComposeFile"
    assert_file_contains "$dc" '"service": "devcontainer"' \
      "$tag devcontainer.json service=devcontainer"
    # forwardPorts may be `[16686, 4318]` or `[ 16686, 4318 ]` etc.
    if grep -Eq '"forwardPorts":[[:space:]]*\[[[:space:]]*16686[[:space:]]*,[[:space:]]*4318[[:space:]]*\]' "$dc"; then
      pass "$tag devcontainer.json forwardPorts [16686, 4318]"
    else
      fail "$tag devcontainer.json forwardPorts [16686, 4318]: pattern not found in $dc"
    fi
    assert_file_contains "$dc" 'ghcr.io/jdx/mise/features/mise' \
      "$tag devcontainer.json includes mise feature"

    # jq validity check (strip // line comments + trailing commas first).
    if command -v jq >/dev/null 2>&1; then
      local jq_tmp
      jq_tmp="$(mktemp "${TMPDIR:-/tmp}/dcjq.XXXXXX")"
      # Strip `// ...` line comments, then strip trailing commas
      # before `]` or `}`. Accumulate the entire file into one
      # buffer in awk's END block so the trailing-comma gsubs see
      # multi-line context — BSD awk's `RS = ""` treats blank
      # lines as record terminators (the comment-strip pass leaves
      # blanks where comment-only lines used to be) and `RS = "\0"`
      # behaves the same, so we must do the accumulation ourselves.
      # Two literal gsubs (one per closing token) because BSD awk's
      # gsub() doesn't honor `\1` backreferences.
      awk '{ sub(/[[:space:]]*\/\/.*$/, ""); print }' "$dc" \
        | awk '{ buf = buf $0 "\n" }
               END { gsub(/,[[:space:]]*\]/, "]", buf);
                     gsub(/,[[:space:]]*\}/, "}", buf);
                     printf "%s", buf }' \
        > "$jq_tmp"
      if jq -e . "$jq_tmp" >/dev/null 2>&1; then
        pass "$tag devcontainer.json parses as JSON (after comment strip)"
      else
        fail "$tag devcontainer.json parses as JSON: jq rejected $jq_tmp"
      fi
      rm -f "$jq_tmp"
    else
      skip "$tag devcontainer.json jq validation (jq not on PATH)"
    fi
  fi

  # ---------- c. docker-compose.yml ----------
  local compose="$proj/.devcontainer/docker-compose.yml"
  assert_file_exists "$compose" "$tag docker-compose.yml"
  if [ -f "$compose" ]; then
    assert_file_contains "$compose" '^[[:space:]]*devcontainer:' \
      "$tag compose has devcontainer service"
    assert_file_contains "$compose" '^[[:space:]]*otel-collector:' \
      "$tag compose has otel-collector service"
    assert_file_contains "$compose" '^[[:space:]]*jaeger:' \
      "$tag compose has jaeger service"
    # Telemetry kit-managed block markers.
    assert_file_contains "$compose" 'kit-managed: telemetry' \
      "$tag compose has telemetry managed marker"

    # Opted-in service assertions (only if requested).
    if [ -n "$services_csv" ]; then
      local IFS=','
      # shellcheck disable=SC2206
      local svcs=( $services_csv )
      unset IFS
      local svc
      for svc in "${svcs[@]}"; do
        svc="${svc//[[:space:]]/}"
        [ -z "$svc" ] && continue
        # All catalog snippets begin "<svc>:" at indent 2.
        if grep -Eq "^[[:space:]]+${svc}:" "$compose"; then
          pass "$tag compose has $svc service"
        else
          fail "$tag compose has $svc service: '${svc}:' not found in $compose"
        fi
      done
    fi

    # docker compose config -q validation (if available).
    if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
      if (cd "$(dirname "$compose")" && docker compose -f "$(basename "$compose")" config -q) >/dev/null 2>&1; then
        pass "$tag docker compose config validates"
      else
        fail "$tag docker compose config validates: $compose rejected by docker compose"
      fi
    else
      skip "$tag docker compose validation (docker compose not on PATH)"
    fi
  fi

  # ---------- d. otel-config.yaml ----------
  local otel="$proj/.devcontainer/otel-config.yaml"
  assert_file_exists "$otel" "$tag otel-config.yaml"
  if [ -f "$otel" ]; then
    assert_file_contains "$otel" '^receivers:' \
      "$tag otel-config has receivers section"
    assert_file_contains "$otel" '^[[:space:]]*otlp:' \
      "$tag otel-config has otlp receiver"
    assert_file_contains "$otel" '^exporters:' \
      "$tag otel-config has exporters section"
    assert_file_contains "$otel" 'otlp/jaeger:' \
      "$tag otel-config has otlp/jaeger exporter"
    assert_file_contains "$otel" 'jaeger:4317' \
      "$tag otel-config exporter targets jaeger:4317"
  fi

  # ---------- e. .env.example ----------
  local env_ex="$proj/.env.example"
  assert_file_exists "$env_ex" "$tag .env.example"
  if [ -f "$env_ex" ]; then
    local label
    for label in telemetry storage queue log config; do
      assert_file_contains "$env_ex" "kit-managed: ${label}" \
        "$tag .env.example has ${label} managed block"
    done
    # OTEL_SERVICE_NAME=<project-name>
    assert_file_contains "$env_ex" "^OTEL_SERVICE_NAME=${pname}\$" \
      "$tag .env.example sets OTEL_SERVICE_NAME=${pname}"

    # Queue driver default sqlite unless redis/postgres opted in.
    local expect_driver="sqlite"
    case ",${services_csv}," in
      *,redis,*)    expect_driver="redis" ;;
      *,postgres,*) expect_driver="postgres" ;;
    esac
    if grep -Eq "^KIT_QUEUE_DRIVER=${expect_driver}[[:space:]]*(#|\$)" "$env_ex"; then
      pass "$tag .env.example KIT_QUEUE_DRIVER=${expect_driver}"
    else
      fail "$tag .env.example KIT_QUEUE_DRIVER=${expect_driver}: not set as expected in $env_ex"
    fi
  fi

  # ---------- f. CI workflows ----------
  local wf_dir="$proj/.github/workflows"
  if [ -d "$wf_dir" ]; then
    local wf_files
    wf_files="$(find "$wf_dir" -maxdepth 1 -type f \( -name '*.yml' -o -name '*.yaml' \) 2>/dev/null)"
    if [ -z "$wf_files" ]; then
      skip "$tag CI workflows (no .yml files under $wf_dir)"
    else
      local has_mise_action=false had_legacy=false legacy_hits=""
      local wf
      while IFS= read -r wf; do
        [ -z "$wf" ] && continue
        if grep -q 'jdx/mise-action@v2' "$wf" 2>/dev/null; then
          has_mise_action=true
        fi
        for pat in 'actions/setup-go@' 'actions/setup-node@' \
                   'actions/setup-python@' 'dtolnay/rust-toolchain@'; do
          if grep -q "$pat" "$wf" 2>/dev/null; then
            had_legacy=true
            legacy_hits="${legacy_hits} ${wf##*/}:${pat}"
          fi
        done
      done <<< "$wf_files"

      if $has_mise_action; then
        pass "$tag CI workflows use jdx/mise-action@v2"
      else
        fail "$tag CI workflows use jdx/mise-action@v2: not found under $wf_dir"
      fi
      if $had_legacy; then
        fail "$tag CI workflows omit legacy setup-* actions: found${legacy_hits}"
      else
        pass "$tag CI workflows omit legacy setup-* actions"
      fi
    fi
  else
    skip "$tag CI workflows (no .github/workflows dir)"
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
# Test 13: assert_managed_files across the lang matrix
# ======================================================
#
# Validates spec §3, §5, §6, §7, §8 against scaffolded outputs
# for the canonical lang combos plus a `--services` case. We
# reuse TEST1/TEST2/TEST11/TEST12 outputs (Go, polyglot
# go,ts,py, Rust, polyglot+rs) and additionally scaffold dedicated
# ts-only, py-only, and `--services postgres,redis` projects.

info "Test 13: managed-file assertions (Go single-lang from TEST1)"
assert_managed_files "$TEST1_DIR" "go" "" "test1app"

info "Test 13: managed-file assertions (polyglot go,ts,py from TEST2)"
assert_managed_files "$TEST2_DIR" "go,ts,py" "" "test2app"

info "Test 13: managed-file assertions (Rust single-lang from TEST11)"
assert_managed_files "$TEST11_DIR" "rs" "" "test11app"

info "Test 13: managed-file assertions (polyglot go,ts,py,rs from TEST12)"
assert_managed_files "$TEST12_DIR" "go,ts,py,rs" "" "test12app"

# Additional ts-only and py-only scaffolds — TEST1/TEST2/TEST11/TEST12
# do not cover those single-lang permutations of mise.toml gating.

info "Test 13.a: ts-only scaffold"
TEST13A_DIR="$TMPDIR_BASE/test13a"
bash "$SCRIPT_DIR/scaffold.sh" test13aapp \
  --output "$TEST13A_DIR" \
  --lang ts \
  --description "Test ts-only" \
  --license mit --author "Test Author" --email "test@example.com" \
  --module-prefix "github.com/testorg" \
  --forge none --no-push --no-tlc
assert_managed_files "$TEST13A_DIR" "ts" "" "test13aapp"

info "Test 13.b: py-only scaffold"
TEST13B_DIR="$TMPDIR_BASE/test13b"
bash "$SCRIPT_DIR/scaffold.sh" test13bapp \
  --output "$TEST13B_DIR" \
  --lang py \
  --description "Test py-only" \
  --license mit --author "Test Author" --email "test@example.com" \
  --module-prefix "github.com/testorg" \
  --forge none --no-push --no-tlc
assert_managed_files "$TEST13B_DIR" "py" "" "test13bapp"

# `--services postgres,redis` exercises the catalog applier:
# expects opted-in service stanzas in compose + queue driver
# flipped to `redis` in .env.example.

info "Test 13.c: scaffold with --services postgres,redis"
TEST13C_DIR="$TMPDIR_BASE/test13c"
bash "$SCRIPT_DIR/scaffold.sh" test-app-with-svc \
  --output "$TEST13C_DIR" \
  --lang go \
  --description "Test services catalog" \
  --license mit --author "Test Author" --email "test@example.com" \
  --module-prefix "github.com/testorg" \
  --services postgres,redis \
  --forge none --no-push --no-tlc
assert_managed_files "$TEST13C_DIR" "go" "postgres,redis" "test-app-with-svc"

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
