#!/usr/bin/env bats

load test_helper

SCRIPT_DIR=""

setup() {
  SCRIPT_DIR="${BATS_TEST_DIRNAME}/.."
  TEST_DIR="$(mktemp -d)"
}

teardown() {
  rm -rf "$TEST_DIR"
}

# ----------------------------------------------------------
# Helpers
# ----------------------------------------------------------

create_go_project() {
  local dir="$1"
  mkdir -p "$dir/cmd/testapp"

  cat > "$dir/go.mod" <<'EOF'
module hop.top/testapp

go 1.26
EOF

  cat > "$dir/cmd/testapp/main.go" <<'EOF'
package main

import "fmt"

func main() { fmt.Println("hello") }
EOF

  cat > "$dir/Makefile" <<'EOF'
test:
	go test ./...
EOF

  (
    cd "$dir"
    git init -q
    git config user.email "test@test.com"
    git config user.name "Test"
    git add -A
    git commit -q -m "init"
  )
}

init_git_repo() {
  local dir="$1"
  (
    cd "$dir"
    git init -q
    git config user.email "test@test.com"
    git config user.name "Test"
    git add -A
    git commit -q -m "init"
  )
}

# ----------------------------------------------------------
# Test 1: Fresh Go project
# ----------------------------------------------------------

@test "conform: fresh go project creates expected files" {
  local d="$TEST_DIR/t1"
  create_go_project "$d"

  run "$SCRIPT_DIR/conform.sh" --path "$d" --no-tlc
  [ "$status" -eq 2 ]

  assert_file_exists "$d/.golangci.yml"
  assert_file_exists "$d/.trivyignore"
  assert_file_exists "$d/CONTRIBUTING.md"
  assert_file_exists "$d/SECURITY.md"
  assert_file_exists "$d/CHANGELOG.md"
  assert_file_exists "$d/.github/workflows/ci.yml"
  assert_file_exists "$d/.goreleaser.yaml"
  assert_file_exists "$d/scripts/promote-release.sh"
  assert_file_exists "$d/internal/version/version.go"
  assert_file_exists "$d/docs/RELEASING.md"
}

@test "conform: fresh go project merges Makefile targets" {
  local d="$TEST_DIR/t1mk"
  create_go_project "$d"

  run "$SCRIPT_DIR/conform.sh" --path "$d" --no-tlc
  [ "$status" -eq 2 ]

  assert_file_contains "$d/Makefile" "kit conform targets"
  assert_file_contains "$d/Makefile" "build:"
  assert_file_contains "$d/Makefile" "release:"
}

@test "conform: fresh go project generates report with tasks" {
  local d="$TEST_DIR/t1rpt"
  create_go_project "$d"

  run "$SCRIPT_DIR/conform.sh" --path "$d" --no-tlc
  [ "$status" -eq 2 ]

  assert_file_exists "$d/conform-report.md"
  assert_file_contains "$d/conform-report.md" "tasks:"
}

@test "conform: AGENTS.md flagged as review, not created" {
  local d="$TEST_DIR/t1agents"
  create_go_project "$d"

  run "$SCRIPT_DIR/conform.sh" --path "$d" --no-tlc
  assert_file_not_exists "$d/AGENTS.md"
}

# ----------------------------------------------------------
# Test 2: Go + Python project
# ----------------------------------------------------------

@test "conform: go+python project creates both CI workflows" {
  local d="$TEST_DIR/t2"
  mkdir -p "$d/cmd/testapp"

  cat > "$d/go.mod" <<'EOF'
module hop.top/testapp

go 1.26
EOF

  cat > "$d/cmd/testapp/main.go" <<'EOF'
package main

import "fmt"

func main() { fmt.Println("hello") }
EOF

  echo "flask>=3.0" > "$d/requirements.txt"

  cat > "$d/Makefile" <<'EOF'
test:
	go test ./...
EOF

  init_git_repo "$d"

  run "$SCRIPT_DIR/conform.sh" --path "$d" --no-tlc

  assert_file_exists "$d/.github/workflows/ci.yml"
  assert_file_exists "$d/.github/workflows/ci-py.yml"
  assert_file_exists "$d/.github/dependabot.yml"
  assert_file_contains "$d/.github/dependabot.yml" "gomod"
  assert_file_contains "$d/.github/dependabot.yml" "pip"
}

# ----------------------------------------------------------
# Test 3: Idempotency
# ----------------------------------------------------------

@test "conform: second run does not modify existing files" {
  local d="$TEST_DIR/t3"
  create_go_project "$d"

  "$SCRIPT_DIR/conform.sh" --path "$d" --no-tlc 2>&1 || true

  local idem_files=".golangci.yml .trivyignore CONTRIBUTING.md
SECURITY.md CHANGELOG.md .github/workflows/ci.yml
.goreleaser.yaml scripts/promote-release.sh
internal/version/version.go docs/RELEASING.md
Makefile .gitignore"

  local mtime_snap="$TEST_DIR/mtimes"
  for f in $idem_files; do
    local target="$d/$f"
    if [ -f "$target" ]; then
      local mt
      mt="$(stat -f '%m' "$target" 2>/dev/null \
        || stat -c '%Y' "$target" 2>/dev/null \
        || echo 0)"
      echo "$f $mt" >> "$mtime_snap"
    fi
  done

  sleep 1

  run "$SCRIPT_DIR/conform.sh" --path "$d" --no-tlc
  [ "$status" -eq 2 ]

  local modified=0
  while IFS=' ' read -r f mt_before; do
    local target="$d/$f"
    if [ -f "$target" ]; then
      local mt_after
      mt_after="$(stat -f '%m' "$target" 2>/dev/null \
        || stat -c '%Y' "$target" 2>/dev/null \
        || echo 0)"
      if [ "$mt_after" != "$mt_before" ]; then
        modified=$((modified + 1))
      fi
    fi
  done < "$mtime_snap"

  [ "$modified" -eq 0 ]
}

@test "conform: idempotent report has Skipped section" {
  local d="$TEST_DIR/t3skip"
  create_go_project "$d"

  "$SCRIPT_DIR/conform.sh" --path "$d" --no-tlc 2>&1 || true
  "$SCRIPT_DIR/conform.sh" --path "$d" --no-tlc 2>&1 || true

  assert_file_contains "$d/conform-report.md" "Skipped"
}

# ----------------------------------------------------------
# Test 4: Dry run
# ----------------------------------------------------------

@test "conform: dry run creates no project files" {
  local d="$TEST_DIR/t4"
  mkdir -p "$d"
  cat > "$d/go.mod" <<'EOF'
module hop.top/testapp

go 1.26
EOF
  init_git_repo "$d"

  run "$SCRIPT_DIR/conform.sh" \
    --path "$d" --dry-run --no-tlc

  assert_file_not_exists "$d/.golangci.yml"
  assert_file_not_exists "$d/CONTRIBUTING.md"
  assert_file_not_exists "$d/.github/workflows/ci.yml"
  assert_file_not_exists "$d/.goreleaser.yaml"
  assert_file_not_exists "$d/internal/version/version.go"
}

@test "conform: dry run still creates report" {
  local d="$TEST_DIR/t4rpt"
  mkdir -p "$d"
  cat > "$d/go.mod" <<'EOF'
module hop.top/testapp

go 1.26
EOF
  init_git_repo "$d"

  run "$SCRIPT_DIR/conform.sh" \
    --path "$d" --dry-run --no-tlc

  assert_file_exists "$d/conform-report.md"
  assert_file_contains "$d/conform-report.md" "dry run"
}

# ----------------------------------------------------------
# Test 5: go.mod replace detection
# ----------------------------------------------------------

@test "conform: detects go.mod replace directives" {
  local d="$TEST_DIR/t5"
  mkdir -p "$d"
  cat > "$d/go.mod" <<'EOF'
module hop.top/testapp

go 1.26

require hop.top/kit v0.1.0

replace hop.top/kit => ../kit
EOF
  init_git_repo "$d"

  run "$SCRIPT_DIR/conform.sh" \
    --path "$d" --no-tlc

  assert_file_contains \
    "$d/conform-report.md" "go.mod replace"
  assert_file_contains \
    "$d/go.mod" "replace hop.top/kit => ../kit"
}

# ----------------------------------------------------------
# Test 6: kit init --update wrapper (T-0811)
# ----------------------------------------------------------
#
# These tests exercise the new managed-block refresh
# delegation. They use a stub KIT_BIN so they don't depend
# on the real kit binary being on PATH.
#
# To make the detection ladder deterministic (the dev's
# real `kit` and `go` live next to each other in
# ~/.local/bin and ~/.local/share/mise), we sanitize PATH
# to strip those two locations. The remainder retains the
# tools conform.sh needs (git, grep, sed, awk, etc.) so
# the additive-merge checks still run.
sanitized_path() {
  echo "$PATH" \
    | tr ':' '\n' \
    | grep -v '\.local/bin' \
    | grep -v 'mise/installs/go' \
    | paste -sd: -
}

# Write a stub kit binary that records its invocation
# arguments and emits a managed.go-style "refreshed N
# managed file(s)" stdout block.
write_kit_stub() {
  local stub="$1" record="$2" exit_code="${3:-0}"
  cat > "$stub" <<EOF
#!/usr/bin/env bash
echo "\$@" > "$record"
cat <<OUT
kit init: refreshed 2 managed file(s):
  - mise.toml
  - .devcontainer/devcontainer.json
OUT
exit $exit_code
EOF
  chmod +x "$stub"
}

@test "conform: --no-managed-refresh skips kit init" {
  local d="$TEST_DIR/t6no"
  create_go_project "$d"

  # If kit were invoked, this stub would fail.
  local stub="$TEST_DIR/kit-stub-fail"
  local record="$TEST_DIR/kit-stub-fail.log"
  write_kit_stub "$stub" "$record" 99

  run env KIT_BIN="$stub" PATH="$(sanitized_path)" \
    "$SCRIPT_DIR/conform.sh" \
    --path "$d" --no-tlc --no-managed-refresh

  # Stub should NOT have been called.
  [ ! -f "$record" ]
  # Report should reflect the skip (anchor on a literal
  # substring that doesn't start with --).
  assert_file_contains "$d/conform-report.md" \
    "no-managed-refresh"
}

@test "conform: KIT_BIN env var is used when kit not on PATH" {
  local d="$TEST_DIR/t6env"
  create_go_project "$d"

  local stub="$TEST_DIR/kit-stub-env"
  local record="$TEST_DIR/kit-stub-env.log"
  write_kit_stub "$stub" "$record" 0

  run env KIT_BIN="$stub" PATH="$(sanitized_path)" \
    "$SCRIPT_DIR/conform.sh" \
    --path "$d" --no-tlc

  # Stub must have been called with `init --update --quiet`.
  [ -f "$record" ]
  grep -q -- "init --update --quiet" "$record"

  # Report should list the managed files the stub emitted.
  assert_file_contains "$d/conform-report.md" \
    "Managed Blocks"
  assert_file_contains "$d/conform-report.md" "mise.toml"
}

@test "conform: --dry-run uses kit init --check (no drift)" {
  local d="$TEST_DIR/t6dry"
  create_go_project "$d"

  local stub="$TEST_DIR/kit-stub-dry"
  local record="$TEST_DIR/kit-stub-dry.log"
  # Exit 0 — no drift.
  write_kit_stub "$stub" "$record" 0

  run env KIT_BIN="$stub" PATH="$(sanitized_path)" \
    "$SCRIPT_DIR/conform.sh" \
    --path "$d" --no-tlc --dry-run

  # Stub fires with --check.
  [ -f "$record" ]
  grep -q -- "init --check --quiet" "$record"

  # Managed status reflects "ok" in the report.
  assert_file_contains "$d/conform-report.md" "Status: ok"
}

@test "conform: --dry-run reports drift in report" {
  local d="$TEST_DIR/t6drift"
  create_go_project "$d"

  local stub="$TEST_DIR/kit-stub-drift"
  local record="$TEST_DIR/kit-stub-drift.log"
  # exit 1 = drift detected by kit init --check
  write_kit_stub "$stub" "$record" 1

  run env KIT_BIN="$stub" PATH="$(sanitized_path)" \
    "$SCRIPT_DIR/conform.sh" \
    --path "$d" --no-tlc --dry-run

  # Stub fires with --check.
  [ -f "$record" ]
  grep -q -- "init --check --quiet" "$record"

  # Managed status reflects "drift" in the report.
  assert_file_contains "$d/conform-report.md" "Status: drift"
}

@test "conform: missing kit + go src warns + continues" {
  local d="$TEST_DIR/t6miss"
  create_go_project "$d"

  # Stage conform.sh + sibling sources in a fake
  # templates dir WITHOUT scaffold.sh so detection rule
  # #3 (in-repo build) misses. PATH stripped of kit/go
  # so #1 and #3 both fail; KIT_BIN unset so #2 misses.
  local fake_templates="$TEST_DIR/fake-templates"
  mkdir -p "$fake_templates"
  cp "$SCRIPT_DIR/lib.sh" "$fake_templates/"
  cp "$SCRIPT_DIR/conform-actions.sh" "$fake_templates/"
  cp "$SCRIPT_DIR/setup-release-please.sh" "$fake_templates/"
  cp "$SCRIPT_DIR/conform.sh" "$fake_templates/"
  # Copy any blueprint dirs the action helpers need so
  # the additive-merge checks don't fail looking for
  # source files. (conform-actions.sh resolves these
  # relative to its own SCRIPT_DIR.)
  if [ -d "$SCRIPT_DIR/shared" ]; then
    cp -R "$SCRIPT_DIR/shared" "$fake_templates/"
  fi
  if [ -d "$SCRIPT_DIR/ci" ]; then
    cp -R "$SCRIPT_DIR/ci" "$fake_templates/"
  fi

  run env -u KIT_BIN PATH="$(sanitized_path)" \
    "$fake_templates/conform.sh" \
    --path "$d" --no-tlc

  # Report exists; additive-merge checks still ran.
  assert_file_exists "$d/conform-report.md"
  # Report should note that managed refresh was skipped.
  assert_file_contains "$d/conform-report.md" \
    "kit binary not found"
}
