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
