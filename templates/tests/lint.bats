#!/usr/bin/env bats

# shellcheck lint tests for template scripts

@test "lib.sh passes shellcheck" {
  run shellcheck -x "$BATS_TEST_DIRNAME/../lib.sh"
  echo "$output"
  [ "$status" -eq 0 ]
}

@test "conform.sh passes shellcheck" {
  run shellcheck -x "$BATS_TEST_DIRNAME/../conform.sh"
  echo "$output"
  [ "$status" -eq 0 ]
}

@test "conform-actions.sh passes shellcheck" {
  run shellcheck -x \
    "$BATS_TEST_DIRNAME/../conform-actions.sh"
  echo "$output"
  [ "$status" -eq 0 ]
}

@test "scaffold.sh passes shellcheck" {
  run shellcheck -x "$BATS_TEST_DIRNAME/../scaffold.sh"
  echo "$output"
  [ "$status" -eq 0 ]
}

@test "setup-release-please.sh passes shellcheck" {
  run shellcheck -x \
    "$BATS_TEST_DIRNAME/../setup-release-please.sh"
  echo "$output"
  [ "$status" -eq 0 ]
}

@test "build.sh passes shellcheck" {
  run shellcheck -x "$BATS_TEST_DIRNAME/../build.sh"
  echo "$output"
  [ "$status" -eq 0 ]
}

@test "reserve-packages.sh passes shellcheck" {
  run shellcheck -x \
    "$BATS_TEST_DIRNAME/../reserve-packages.sh"
  echo "$output"
  [ "$status" -eq 0 ]
}

# shfmt checks (skipped if shfmt not installed)

@test "shell scripts pass shfmt formatting" {
  command -v shfmt >/dev/null 2>&1 || skip "shfmt not installed"
  local scripts=(
    "$BATS_TEST_DIRNAME/../lib.sh"
    "$BATS_TEST_DIRNAME/../conform.sh"
    "$BATS_TEST_DIRNAME/../conform-actions.sh"
    "$BATS_TEST_DIRNAME/../scaffold.sh"
    "$BATS_TEST_DIRNAME/../setup-release-please.sh"
    "$BATS_TEST_DIRNAME/../build.sh"
    "$BATS_TEST_DIRNAME/../reserve-packages.sh"
  )
  run shfmt -d -i 2 -ci "${scripts[@]}"
  echo "$output"
  [ "$status" -eq 0 ]
}
