#!/usr/bin/env bats
# emit-devcontainer-json.bats — Tests for emit-devcontainer-json.sh
#
# Run with:
#   bats templates/shared/emit-devcontainer-json.bats

setup() {
  SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
  # shellcheck source=managed-block.sh
  source "$SCRIPT_DIR/managed-block.sh"
  # shellcheck source=emit-devcontainer-json.sh
  source "$SCRIPT_DIR/emit-devcontainer-json.sh"
  TMP="$(mktemp -d -t edj-bats.XXXXXX)"
}

teardown() {
  [[ -n "${TMP:-}" ]] && rm -rf "$TMP"
}

# Strip `//` line comments from a JSON-C file so plain JSON
# parsers (jq, python json) can consume it. Awk-only — works on
# macOS BSD and GNU.
_strip_jsonc_comments() {
  awk '
    {
      # Drop any "//..." run to EOL. Naive (does not consider
      # strings) but our emitted file has no `//` literals
      # inside strings, so this is safe for tests.
      sub(/[ \t]*\/\/.*$/, "", $0)
      print
    }
  ' "$1"
}

# Strip trailing commas before `}` or `]` so plain `jq` is happy
# with the otherwise-valid JSON-C body (jq does not accept
# JSON-C trailing commas; python -m json.tool would also reject
# them). We run this after comment-stripping.
_strip_trailing_commas() {
  # Repeat passes until stable (covers nested cases) — a single
  # sed-style pass would miss "foo,\n    ,\n]". Two passes is
  # enough for the shapes we emit.
  local f="$1" tmp1 tmp2
  tmp1="$(mktemp)"
  tmp2="$(mktemp)"
  cat "$f" > "$tmp1"
  for _ in 1 2 3; do
    awk '
      BEGIN { ORS = "" }
      {
        lines[NR] = $0
      }
      END {
        for (i = 1; i <= NR; i++) {
          line = lines[i]
          # find next non-blank/non-comment line
          j = i + 1
          while (j <= NR && lines[j] ~ /^[[:space:]]*$/) j++
          if (j <= NR && lines[j] ~ /^[[:space:]]*[\]}]/) {
            sub(/,[[:space:]]*$/, "", line)
          }
          print line "\n"
        }
      }
    ' "$tmp1" > "$tmp2"
    mv "$tmp2" "$tmp1"
    tmp2="$(mktemp)"
  done
  cat "$tmp1" > "$f"
  rm -f "$tmp1" "$tmp2"
}

# ----------------------------------------------------------
# Extension membership
# ----------------------------------------------------------

@test "go-only: includes golang.go, excludes rust-analyzer" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "go"
  local f="$out/.devcontainer/devcontainer.json"
  [ -f "$f" ]
  grep -qF '"golang.go"' "$f"
  ! grep -qF '"rust-lang.rust-analyzer"' "$f"
  ! grep -qF '"ms-python.python"' "$f"
  ! grep -qF '"dbaeumer.vscode-eslint"' "$f"
}

@test "ts,py polyglot: includes both ts and py extensions" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "ts,py"
  local f="$out/.devcontainer/devcontainer.json"
  grep -qF '"dbaeumer.vscode-eslint"' "$f"
  grep -qF '"esbenp.prettier-vscode"' "$f"
  grep -qF '"ms-python.python"' "$f"
  grep -qF '"ms-python.vscode-pylance"' "$f"
  grep -qF '"charliermarsh.ruff"' "$f"
  ! grep -qF '"golang.go"' "$f"
  ! grep -qF '"rust-lang.rust-analyzer"' "$f"
}

@test "all four langs: includes everything" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "go,ts,py,rs"
  local f="$out/.devcontainer/devcontainer.json"
  grep -qF '"golang.go"' "$f"
  grep -qF '"dbaeumer.vscode-eslint"' "$f"
  grep -qF '"ms-python.python"' "$f"
  grep -qF '"rust-lang.rust-analyzer"' "$f"
  grep -qF '"tamasfe.even-better-toml"' "$f"
}

@test "always-included extensions present regardless of lang" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "go"
  local f="$out/.devcontainer/devcontainer.json"
  grep -qF '"jdx.mise-vscode"' "$f"
  grep -qF '"EditorConfig.EditorConfig"' "$f"
}

# ----------------------------------------------------------
# Static fields
# ----------------------------------------------------------

@test "emits required compose-mode fields" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "go"
  local f="$out/.devcontainer/devcontainer.json"
  grep -qF '"dockerComposeFile": "docker-compose.yml"' "$f"
  grep -qF '"service": "devcontainer"' "$f"
  grep -qF '"forwardPorts": [16686, 4318]' "$f"
}

@test "project name interpolated into name field" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "my-special-app" "go"
  local f="$out/.devcontainer/devcontainer.json"
  grep -qF '"name": "my-special-app"' "$f"
}

@test "emits managed lifecycle commands" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "go"
  local f="$out/.devcontainer/devcontainer.json"
  grep -qF 'mise trust && mise install && mise run install' "$f"
  grep -qF 'mise install --quiet' "$f"
}

# ----------------------------------------------------------
# JSON-C validity (with jq if available, plain stdlib otherwise)
# ----------------------------------------------------------

@test "emitted file is valid JSON-C (parses after stripping comments + trailing commas)" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "go,ts"
  local f="$out/.devcontainer/devcontainer.json"
  local cleaned="$TMP/cleaned.json"
  _strip_jsonc_comments "$f" > "$cleaned"
  _strip_trailing_commas "$cleaned"
  if command -v jq >/dev/null 2>&1; then
    run jq '.' "$cleaned"
    [ "$status" -eq 0 ]
  elif command -v python3 >/dev/null 2>&1; then
    run python3 -m json.tool "$cleaned"
    [ "$status" -eq 0 ]
  else
    skip "neither jq nor python3 available for JSON validation"
  fi
}

# ----------------------------------------------------------
# Idempotency
# ----------------------------------------------------------

@test "two consecutive emits produce a byte-identical file" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "go,ts"
  local f="$out/.devcontainer/devcontainer.json"
  cp "$f" "$f.bak"
  emit_devcontainer_json "$out" "myapp" "go,ts"
  cmp "$f" "$f.bak"
}

@test "idempotency holds across all langs" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "go,ts,py,rs"
  local f="$out/.devcontainer/devcontainer.json"
  cp "$f" "$f.bak"
  emit_devcontainer_json "$out" "myapp" "go,ts,py,rs"
  cmp "$f" "$f.bak"
}

@test "lang order does not affect output" {
  local a="$TMP/a" b="$TMP/b"
  mkdir -p "$a" "$b"
  emit_devcontainer_json "$a" "myapp" "go,ts"
  emit_devcontainer_json "$b" "myapp" "ts,go"
  cmp "$a/.devcontainer/devcontainer.json" "$b/.devcontainer/devcontainer.json"
}

# ----------------------------------------------------------
# Managed-block round trip
# ----------------------------------------------------------

@test "extension list lives inside a kit-managed block" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "go"
  local f="$out/.devcontainer/devcontainer.json"
  grep -qF '// >>> kit-managed: extensions >>>' "$f"
  grep -qF '// <<< kit-managed: extensions <<<' "$f"
}

@test "lifecycle commands live inside a kit-managed block" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "go"
  local f="$out/.devcontainer/devcontainer.json"
  grep -qF '// >>> kit-managed: lifecycle >>>' "$f"
  grep -qF '// <<< kit-managed: lifecycle <<<' "$f"
}

@test "re-emit with different langs updates managed extensions block" {
  local out="$TMP/proj"
  mkdir -p "$out"
  emit_devcontainer_json "$out" "myapp" "go"
  local f="$out/.devcontainer/devcontainer.json"
  grep -qF '"golang.go"' "$f"
  ! grep -qF '"rust-lang.rust-analyzer"' "$f"

  emit_devcontainer_json "$out" "myapp" "rs"
  ! grep -qF '"golang.go"' "$f"
  grep -qF '"rust-lang.rust-analyzer"' "$f"
}

# ----------------------------------------------------------
# Argument validation
# ----------------------------------------------------------

@test "errors when project-dir argument is missing" {
  run emit_devcontainer_json
  [ "$status" -ne 0 ]
}

@test "errors when lang-csv argument is missing" {
  run emit_devcontainer_json "$TMP/x" "name"
  [ "$status" -ne 0 ]
}
