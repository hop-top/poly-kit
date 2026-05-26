#!/usr/bin/env bats
# emit-env-example.bats — Tests for templates/shared/emit-env-example.sh
#
# Run with:
#   bats templates/shared/emit-env-example.bats
#
# Requires bats-core. No bats helpers — pure stdlib so tests
# can run anywhere (CI, devcontainer, dev laptop).

setup() {
  SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
  # shellcheck source=managed-block.sh
  source "$SCRIPT_DIR/managed-block.sh"
  # shellcheck source=emit-env-example.sh
  source "$SCRIPT_DIR/emit-env-example.sh"
  TMP="$(mktemp -d -t eee-bats.XXXXXX)"
  PROJ="$TMP/myapp"
  mkdir -p "$PROJ"
}

teardown() {
  [[ -n "${TMP:-}" ]] && rm -rf "$TMP"
}

# ----------------------------------------------------------
# Block presence
# ----------------------------------------------------------

@test "emit_env_example: emits all five labeled blocks" {
  emit_env_example "$PROJ" myapp
  local f="$PROJ/.env.example"
  [ -f "$f" ]
  mb_has "$f" telemetry
  mb_has "$f" storage
  mb_has "$f" queue
  mb_has "$f" log
  mb_has "$f" config
}

# ----------------------------------------------------------
# Interpolation
# ----------------------------------------------------------

@test "emit_env_example: OTEL_SERVICE_NAME equals project name" {
  emit_env_example "$PROJ" myapp
  grep -q '^OTEL_SERVICE_NAME=myapp$' "$PROJ/.env.example"
}

@test "emit_env_example: OTEL_SERVICE_NAME re-interpolates with different name" {
  emit_env_example "$PROJ" another-app
  grep -q '^OTEL_SERVICE_NAME=another-app$' "$PROJ/.env.example"
}

# ----------------------------------------------------------
# Default values
# ----------------------------------------------------------

@test "emit_env_example: default KIT_QUEUE_DRIVER is sqlite" {
  emit_env_example "$PROJ" myapp
  grep -qE '^KIT_QUEUE_DRIVER=sqlite([[:space:]]|$)' "$PROJ/.env.example"
}

@test "emit_env_example: redis URL is commented out" {
  emit_env_example "$PROJ" myapp
  grep -q '^# KIT_QUEUE_REDIS_URL=redis://localhost:6379$' "$PROJ/.env.example"
  ! grep -qE '^KIT_QUEUE_REDIS_URL=' "$PROJ/.env.example"
}

@test "emit_env_example: postgres URL is commented out" {
  emit_env_example "$PROJ" myapp
  grep -q '^# KIT_QUEUE_POSTGRES_URL=postgres://\.\.\.$' "$PROJ/.env.example"
  ! grep -qE '^KIT_QUEUE_POSTGRES_URL=' "$PROJ/.env.example"
}

@test "emit_env_example: log defaults present" {
  emit_env_example "$PROJ" myapp
  grep -qE '^KIT_LOG_LEVEL=info([[:space:]]|$)' "$PROJ/.env.example"
  grep -qE '^KIT_LOG_FORMAT=json([[:space:]]|$)' "$PROJ/.env.example"
}

@test "emit_env_example: storage XDG vars are commented out" {
  emit_env_example "$PROJ" myapp
  grep -q '^# KIT_CONFIG_DIR=$' "$PROJ/.env.example"
  grep -q '^# KIT_DATA_DIR=$' "$PROJ/.env.example"
  grep -q '^# KIT_STATE_DIR=$' "$PROJ/.env.example"
}

# ----------------------------------------------------------
# Idempotency
# ----------------------------------------------------------

@test "emit_env_example: two emits produce a byte-identical file" {
  emit_env_example "$PROJ" myapp
  cp "$PROJ/.env.example" "$TMP/first"
  emit_env_example "$PROJ" myapp
  cmp "$TMP/first" "$PROJ/.env.example"
}

# ----------------------------------------------------------
# User content preservation
# ----------------------------------------------------------

@test "emit_env_example: user-added vars above markers survive re-emit" {
  local f="$PROJ/.env.example"
  emit_env_example "$PROJ" myapp
  # Prepend a user-owned line at the top of the file.
  { printf 'MY_VAR=foo\n\n'; cat "$f"; } > "$f.new"
  mv "$f.new" "$f"
  grep -q '^MY_VAR=foo$' "$f"

  # Re-emit; user var must still be present.
  emit_env_example "$PROJ" myapp
  grep -q '^MY_VAR=foo$' "$f"
}

# ----------------------------------------------------------
# Block separators
# ----------------------------------------------------------

@test "emit_env_example: blocks are separated by a single blank line" {
  emit_env_example "$PROJ" myapp
  local f="$PROJ/.env.example"
  # Each close marker (except the last) should be followed by
  # exactly one blank line and then the next open marker.
  local prev=""
  for label in telemetry storage queue log config; do
    if [[ -n "$prev" ]]; then
      # Find line number of previous close and next open
      local close_ln open_ln
      close_ln="$(grep -n "^# <<< kit-managed: ${prev} <<<\$" "$f" | head -1 | cut -d: -f1)"
      open_ln="$(grep -n "^# >>> kit-managed: ${label} >>>\$" "$f" | head -1 | cut -d: -f1)"
      [ "$((open_ln - close_ln))" -eq 2 ]
    fi
    prev="$label"
  done
}

# ----------------------------------------------------------
# Argument validation
# ----------------------------------------------------------

@test "emit_env_example: missing project-dir argument errors" {
  run emit_env_example "" myapp
  [ "$status" -ne 0 ]
}

@test "emit_env_example: missing project-name argument errors" {
  run emit_env_example "$PROJ" ""
  [ "$status" -ne 0 ]
}

@test "emit_env_example: nonexistent directory errors" {
  run emit_env_example "$TMP/does-not-exist" myapp
  [ "$status" -ne 0 ]
}
