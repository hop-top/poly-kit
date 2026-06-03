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

# Consistency checks: language lists in init.sh must enumerate
# all 5 supported langs (go, ts, py, rs, php). scaffold.sh already
# validates this set at --lang parse time; init.sh's polyglot
# detection, prompt default, and prune loop must match or php gets
# silently ignored when init.sh runs (e.g. on a future clone-then-init
# flow).

@test "init.sh polyglot detection includes php" {
  run grep -A 5 "is_polyglot=false" \
    "$BATS_TEST_DIRNAME/../shared/init.sh"
  echo "$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *'"$PROJECT_DIR/php"'* ]]
}

@test "init.sh selected_langs prompt default includes php" {
  run grep "prompt_multi selected_langs" \
    -A 2 "$BATS_TEST_DIRNAME/../shared/init.sh"
  echo "$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *"go,ts,py,rs,php"* ]]
}

@test "init.sh polyglot prune loop iterates php" {
  run grep "for lang in" \
    "$BATS_TEST_DIRNAME/../shared/init.sh"
  echo "$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *"php"* ]]
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
