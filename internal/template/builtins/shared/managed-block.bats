#!/usr/bin/env bats
# managed-block.bats — Tests for templates/shared/managed-block.sh
#
# Run with:
#   bats templates/shared/managed-block.bats
#
# Requires bats-core. No bats helpers — pure stdlib so tests
# can run anywhere (CI, devcontainer, dev laptop).

setup() {
  SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
  # shellcheck source=managed-block.sh
  source "$SCRIPT_DIR/managed-block.sh"
  TMP="$(mktemp -d -t mb-bats.XXXXXX)"
}

teardown() {
  [[ -n "${TMP:-}" ]] && rm -rf "$TMP"
}

# ----------------------------------------------------------
# mb_comment_char
# ----------------------------------------------------------

@test "mb_comment_char: TOML uses #" {
  run mb_comment_char foo.toml
  [ "$status" -eq 0 ]
  [ "$output" = "#" ]
}

@test "mb_comment_char: YAML uses #" {
  run mb_comment_char foo.yaml
  [ "$output" = "#" ]
}

@test "mb_comment_char: .env uses #" {
  run mb_comment_char .env
  [ "$output" = "#" ]
}

@test "mb_comment_char: devcontainer.json uses //" {
  run mb_comment_char .devcontainer/devcontainer.json
  [ "$output" = "//" ]
}

@test "mb_comment_char: .jsonc uses //" {
  run mb_comment_char tsconfig.jsonc
  [ "$output" = "//" ]
}

@test "mb_comment_char: .json uses //" {
  run mb_comment_char package.json
  [ "$output" = "//" ]
}

# ----------------------------------------------------------
# mb_write — creating a new block
# ----------------------------------------------------------

@test "mb_write: creates block at EOF when file has no markers" {
  local f="$TMP/mise.toml"
  printf 'go = "1.26"\nnode = "22"\n' | mb_write "$f"
  grep -q '^# >>> kit-managed >>>$' "$f"
  grep -q '^# <<< kit-managed <<<$' "$f"
  grep -q '^go = "1.26"$' "$f"
}

@test "mb_write: creates a new file when target does not exist" {
  local f="$TMP/new.toml"
  printf 'x=1\n' | mb_write "$f"
  [ -f "$f" ]
  grep -q '^x=1$' "$f"
}

# ----------------------------------------------------------
# Idempotency
# ----------------------------------------------------------

@test "mb_write: two writes of same content produce byte-identical output" {
  local f="$TMP/mise.toml"
  printf 'go = "1.26"\n' | mb_write "$f"
  cp "$f" "$f.bak"
  printf 'go = "1.26"\n' | mb_write "$f"
  cmp "$f" "$f.bak"
}

@test "mb_write: rewriting different content updates only the block" {
  local f="$TMP/mise.toml"
  cat > "$f" <<'EOF'
# user above
[tools]
my-tool = "1.0"

# >>> kit-managed >>>
go = "1.26"
# <<< kit-managed <<<

# user below
EOF
  # capture pre/post-block regions
  awk '/^# >>> kit-managed >>>$/{exit} {print}' "$f" > "$TMP/head.before"
  awk 'p; /^# <<< kit-managed <<<$/{p=1}' "$f" > "$TMP/tail.before"

  printf 'go = "1.27"\nnode = "22"\n' | mb_write "$f"

  awk '/^# >>> kit-managed >>>$/{exit} {print}' "$f" > "$TMP/head.after"
  awk 'p; /^# <<< kit-managed <<<$/{p=1}' "$f" > "$TMP/tail.after"
  cmp "$TMP/head.before" "$TMP/head.after"
  cmp "$TMP/tail.before" "$TMP/tail.after"
  # confirm block content is updated
  grep -q '^go = "1.27"$' "$f"
  grep -q '^node = "22"$' "$f"
  ! grep -q '^go = "1.26"$' "$f"
}

# ----------------------------------------------------------
# Labels
# ----------------------------------------------------------

@test "mb_write: labeled and unlabeled blocks coexist" {
  local f="$TMP/mise.toml"
  printf 'go = "1.26"\n' | mb_write "$f"
  printf 'OTEL_ENDPOINT=http://localhost:4318\n' | mb_write "$f" telemetry
  grep -q '^# >>> kit-managed >>>$' "$f"
  grep -q '^# >>> kit-managed: telemetry >>>$' "$f"
  grep -q '^go = "1.26"$' "$f"
  grep -q '^OTEL_ENDPOINT=' "$f"
}

@test "mb_read: unlabeled block returns only its content" {
  local f="$TMP/mise.toml"
  printf 'go = "1.26"\n' | mb_write "$f"
  printf 'OTEL=42\n' | mb_write "$f" telemetry
  run mb_read "$f"
  [ "$status" -eq 0 ]
  [ "$output" = 'go = "1.26"' ]
}

@test "mb_read: labeled block returns only its content" {
  local f="$TMP/mise.toml"
  printf 'go = "1.26"\n' | mb_write "$f"
  printf 'OTEL=42\n' | mb_write "$f" telemetry
  run mb_read "$f" telemetry
  [ "$status" -eq 0 ]
  [ "$output" = 'OTEL=42' ]
}

@test "mb_read: errors when file does not exist" {
  run mb_read "$TMP/nope.toml"
  [ "$status" -ne 0 ]
}

@test "mb_read: non-zero when block label not present" {
  local f="$TMP/mise.toml"
  printf 'x=1\n' | mb_write "$f"
  run mb_read "$f" telemetry
  [ "$status" -ne 0 ]
}

# ----------------------------------------------------------
# mb_remove
# ----------------------------------------------------------

@test "mb_remove: labeled block removal leaves other blocks intact" {
  local f="$TMP/mise.toml"
  printf 'go = "1.26"\n' | mb_write "$f"
  printf 'OTEL=42\n' | mb_write "$f" telemetry
  mb_remove "$f" telemetry
  ! grep -q 'kit-managed: telemetry' "$f"
  grep -q '^# >>> kit-managed >>>$' "$f"
  grep -q '^go = "1.26"$' "$f"
}

@test "mb_remove: no-op when block does not exist" {
  local f="$TMP/mise.toml"
  printf 'go = "1.26"\n' | mb_write "$f"
  cp "$f" "$f.bak"
  mb_remove "$f" telemetry
  cmp "$f" "$f.bak"
}

@test "mb_remove: no-op when file does not exist" {
  run mb_remove "$TMP/nope.toml"
  [ "$status" -eq 0 ]
}

# ----------------------------------------------------------
# mb_has
# ----------------------------------------------------------

@test "mb_has: returns 0 when block exists" {
  local f="$TMP/mise.toml"
  printf 'go = "1.26"\n' | mb_write "$f"
  mb_has "$f"
}

@test "mb_has: returns non-zero when block missing" {
  local f="$TMP/empty.toml"
  printf 'irrelevant\n' > "$f"
  run mb_has "$f"
  [ "$status" -ne 0 ]
}

@test "mb_has: distinguishes labeled vs unlabeled" {
  local f="$TMP/mise.toml"
  printf 'go = "1.26"\n' | mb_write "$f"
  mb_has "$f"
  run mb_has "$f" telemetry
  [ "$status" -ne 0 ]
}

# ----------------------------------------------------------
# JSON-C
# ----------------------------------------------------------

@test "JSON-C: // markers are used for devcontainer.json" {
  local f="$TMP/devcontainer.json"
  printf '{\n  "name": "demo"\n}\n' > "$f"
  printf '  "postCreateCommand": "mise install"\n' | mb_write "$f"
  grep -q '^// >>> kit-managed >>>$' "$f"
  grep -q '^// <<< kit-managed <<<$' "$f"
}

@test "JSON-C: labeled block uses // prefix" {
  local f="$TMP/devcontainer.json"
  printf '{}\n' > "$f"
  printf '"x": 1\n' | mb_write "$f" extensions
  grep -q '^// >>> kit-managed: extensions >>>$' "$f"
  grep -q '^// <<< kit-managed: extensions <<<$' "$f"
}

@test "JSON-C: read returns block content" {
  local f="$TMP/devcontainer.json"
  printf '{}\n' > "$f"
  printf 'PAYLOAD\n' | mb_write "$f"
  run mb_read "$f"
  [ "$status" -eq 0 ]
  [ "$output" = "PAYLOAD" ]
}

# ----------------------------------------------------------
# User-content preservation across edits
# ----------------------------------------------------------

@test "user content above markers preserved across edits" {
  local f="$TMP/keep.toml"
  cat > "$f" <<'EOF'
# Pinned by hand. Do not touch.
[user.section]
key = "value"

EOF
  printf 'managed = true\n' | mb_write "$f"
  printf 'managed = "updated"\n' | mb_write "$f"

  # First three lines must be byte-identical to original.
  head -3 "$f" > "$TMP/head"
  cat > "$TMP/expected" <<'EOF'
# Pinned by hand. Do not touch.
[user.section]
key = "value"
EOF
  cmp "$TMP/head" "$TMP/expected"
}

@test "trailing user content preserved across edits" {
  local f="$TMP/keep.toml"
  cat > "$f" <<'EOF'
# >>> kit-managed >>>
initial = true
# <<< kit-managed <<<

# Footer note kept by user
[trailer]
ok = 1
EOF
  printf 'changed = true\n' | mb_write "$f"
  grep -q '^# Footer note kept by user$' "$f"
  grep -q '^\[trailer\]$' "$f"
  grep -q '^ok = 1$' "$f"
}
