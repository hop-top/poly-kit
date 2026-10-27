#!/usr/bin/env bats
# emit-mise.bats — Tests for templates/shared/emit-mise.sh
#
# Run with:
#   bats templates/shared/emit-mise.bats
#
# Requires bats-core. No bats helpers — pure stdlib so tests
# can run anywhere (CI, devcontainer, dev laptop).

setup() {
  SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
  # shellcheck source=emit-mise.sh
  source "$SCRIPT_DIR/emit-mise.sh"
  TMP="$(mktemp -d -t emit-mise-bats.XXXXXX)"
  PROJ="$TMP/proj"
  mkdir -p "$PROJ"
}

# expect_pin <key>: assert the emitted mise.toml pins <key> to the
# value the sibling tool-versions.toml declares. The manifest is the
# source of truth, so pins are read from it rather than hardcoded;
# a bump there needs no test edit. The lookup is a plain grep/sed
# independent of emit-mise.sh's awk parser, and fails when the key
# is missing from the manifest so an empty expectation never passes.
expect_pin() {
  local key="$1" quoted want
  quoted="$key"
  [[ "$key" == *:* ]] && quoted="\"$key\""
  want="$(grep -E "^${quoted}[[:space:]]*=" "$SCRIPT_DIR/tool-versions.toml" |
    head -n1 | sed -E 's/^[^=]*=[[:space:]]*"([^"]*)".*/\1/')"
  if [[ -z "$want" ]]; then
    echo "tool-versions.toml has no pin for $key" >&2
    return 1
  fi
  grep -qxF "$quoted = \"$want\"" "$PROJ/mise.toml" || {
    echo "mise.toml lacks: $quoted = \"$want\"" >&2
    return 1
  }
}

teardown() {
  [[ -n "${TMP:-}" ]] && rm -rf "$TMP"
}

# ----------------------------------------------------------
# go-only: runtimes + workflow tools gated to go family
# ----------------------------------------------------------

@test "emit_mise go: includes go runtime + golangci-lint" {
  emit_mise "$PROJ" "go"
  expect_pin go
  expect_pin golangci-lint
}

@test "emit_mise go: includes cross-cutting workflow tools" {
  emit_mise "$PROJ" "go"
  expect_pin lychee
  expect_pin hadolint
  expect_pin actionlint
  expect_pin shellcheck
  expect_pin shfmt
  expect_pin npm:release-please
}

@test "emit_mise: kit's own CI tools (buf, markdownlint-cli2) stay out of scaffolds" {
  emit_mise "$PROJ" "go,ts,py,rs"
  ! grep -qE '^buf = '                     "$PROJ/mise.toml" || false
  ! grep -qE '^"npm:markdownlint-cli2" = ' "$PROJ/mise.toml" || false
}

@test "emit_mise go: excludes node, python, rust, ruff" {
  emit_mise "$PROJ" "go"
  ! grep -qE '^node = '   "$PROJ/mise.toml" || false
  ! grep -qE '^pnpm = '   "$PROJ/mise.toml" || false
  ! grep -qE '^python = ' "$PROJ/mise.toml" || false
  ! grep -qE '^uv = '     "$PROJ/mise.toml" || false
  ! grep -qE '^rust = '   "$PROJ/mise.toml" || false
  ! grep -qE '^ruff = '   "$PROJ/mise.toml" || false
}

# ----------------------------------------------------------
# All four langs
# ----------------------------------------------------------

@test "emit_mise go,ts,py,rs: all four runtimes present" {
  emit_mise "$PROJ" "go,ts,py,rs"
  grep -qE '^go = '     "$PROJ/mise.toml"
  grep -qE '^node = '   "$PROJ/mise.toml"
  grep -qE '^pnpm = '   "$PROJ/mise.toml"
  grep -qE '^python = ' "$PROJ/mise.toml"
  grep -qE '^uv = '     "$PROJ/mise.toml"
  grep -qE '^rust = '   "$PROJ/mise.toml"
}

@test "emit_mise go,ts,py,rs: both lang-gated workflow tools" {
  emit_mise "$PROJ" "go,ts,py,rs"
  grep -qE '^golangci-lint = ' "$PROJ/mise.toml"
  grep -qE '^ruff = '          "$PROJ/mise.toml"
}

@test "emit_mise go,ts,py,rs: install task lists all four commands" {
  emit_mise "$PROJ" "go,ts,py,rs"
  grep -qF '"go mod download"' "$PROJ/mise.toml"
  grep -qF '"pnpm install"'    "$PROJ/mise.toml"
  grep -qF '"uv sync"'         "$PROJ/mise.toml"
  grep -qF '"cargo fetch"'     "$PROJ/mise.toml"
}

# ----------------------------------------------------------
# py-only
# ----------------------------------------------------------

@test "emit_mise py: includes python, uv, ruff" {
  emit_mise "$PROJ" "py"
  expect_pin python
  expect_pin uv
  expect_pin ruff
}

@test "emit_mise py: excludes golangci-lint and other runtimes" {
  emit_mise "$PROJ" "py"
  ! grep -qE '^golangci-lint = ' "$PROJ/mise.toml" || false
  ! grep -qE '^go = '            "$PROJ/mise.toml" || false
  ! grep -qE '^node = '          "$PROJ/mise.toml" || false
  ! grep -qE '^rust = '          "$PROJ/mise.toml" || false
}

@test "emit_mise py: install task lists only uv sync" {
  emit_mise "$PROJ" "py"
  grep -qF '"uv sync"' "$PROJ/mise.toml"
  ! grep -qF '"go mod download"' "$PROJ/mise.toml" || false
  ! grep -qF '"pnpm install"'    "$PROJ/mise.toml" || false
  ! grep -qF '"cargo fetch"'     "$PROJ/mise.toml" || false
}

# ----------------------------------------------------------
# Idempotency
# ----------------------------------------------------------

@test "emit_mise: two runs with same inputs produce byte-identical files" {
  emit_mise "$PROJ" "go"
  cp "$PROJ/mise.toml" "$PROJ/mise.toml.bak"
  emit_mise "$PROJ" "go"
  cmp "$PROJ/mise.toml" "$PROJ/mise.toml.bak"
}

@test "emit_mise: idempotent for go,ts,py,rs as well" {
  emit_mise "$PROJ" "go,ts,py,rs"
  cp "$PROJ/mise.toml" "$PROJ/mise.toml.bak"
  emit_mise "$PROJ" "go,ts,py,rs"
  cmp "$PROJ/mise.toml" "$PROJ/mise.toml.bak"
}

# ----------------------------------------------------------
# Managed-block markers + tasks.install structure
# ----------------------------------------------------------

@test "emit_mise: file has kit-managed open + close markers" {
  emit_mise "$PROJ" "go"
  grep -qE '^# >>> kit-managed >>>$' "$PROJ/mise.toml"
  grep -qE '^# <<< kit-managed <<<$' "$PROJ/mise.toml"
}

@test "emit_mise: file has [tools], [env], [tasks.install] sections" {
  emit_mise "$PROJ" "go"
  grep -qE '^\[tools\]$'          "$PROJ/mise.toml"
  grep -qE '^\[env\]$'            "$PROJ/mise.toml"
  grep -qE '^\[tasks\.install\]$' "$PROJ/mise.toml"
}

@test "emit_mise: env section pulls from .env" {
  emit_mise "$PROJ" "go"
  grep -qE '^_\.file = "\.env"$' "$PROJ/mise.toml"
}

@test "emit_mise: ts-only install command is pnpm install" {
  emit_mise "$PROJ" "ts"
  grep -qF '"pnpm install"' "$PROJ/mise.toml"
  ! grep -qF '"go mod download"' "$PROJ/mise.toml" || false
  ! grep -qF '"uv sync"'         "$PROJ/mise.toml" || false
  ! grep -qF '"cargo fetch"'     "$PROJ/mise.toml" || false
}

@test "emit_mise: rs-only install command is cargo fetch" {
  emit_mise "$PROJ" "rs"
  grep -qF '"cargo fetch"' "$PROJ/mise.toml"
  ! grep -qF '"go mod download"' "$PROJ/mise.toml" || false
  ! grep -qF '"pnpm install"'    "$PROJ/mise.toml" || false
  ! grep -qF '"uv sync"'         "$PROJ/mise.toml" || false
}

# ----------------------------------------------------------
# Header preservation (kit-managed prose above the markers)
# ----------------------------------------------------------

@test "emit_mise: emits kit-managed prose header above markers on first run" {
  emit_mise "$PROJ" "go"
  # Header line must appear before the open marker.
  head_line="$(grep -n '^# Managed by' "$PROJ/mise.toml" | head -1 | cut -d: -f1)"
  open_line="$(grep -n '^# >>> kit-managed >>>$' "$PROJ/mise.toml" | head -1 | cut -d: -f1)"
  [ -n "$head_line" ]
  [ -n "$open_line" ]
  [ "$head_line" -lt "$open_line" ]
}

# ----------------------------------------------------------
# Error paths
# ----------------------------------------------------------

@test "emit_mise: errors when project-dir missing" {
  run emit_mise "" "go"
  [ "$status" -ne 0 ]
}

@test "emit_mise: errors when lang-csv missing" {
  run emit_mise "$PROJ" ""
  [ "$status" -ne 0 ]
}

@test "emit_mise: errors when project-dir is not a directory" {
  run emit_mise "$TMP/does-not-exist" "go"
  [ "$status" -ne 0 ]
}
