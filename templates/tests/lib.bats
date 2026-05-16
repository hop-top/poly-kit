#!/usr/bin/env bats

setup() {
  TEMPLATES_DIR="${BATS_TEST_DIRNAME}/.."
  # shellcheck source=../lib.sh
  source "$TEMPLATES_DIR/lib.sh"
  TEST_DIR="$(mktemp -d)"
}

teardown() {
  rm -rf "$TEST_DIR"
}

# ==========================================================
# detect_languages
# ==========================================================

@test "detect_languages: empty dir returns empty array" {
  cd "$TEST_DIR"
  detect_languages
  [ "${#DETECTED_LANGS[@]}" -eq 0 ]
}

@test "detect_languages: go.mod detects go" {
  touch "$TEST_DIR/go.mod"
  cd "$TEST_DIR"
  detect_languages
  [ "${DETECTED_LANGS[*]}" = "go" ]
}

@test "detect_languages: go.mod + requirements.txt detects go py" {
  touch "$TEST_DIR/go.mod" "$TEST_DIR/requirements.txt"
  cd "$TEST_DIR"
  detect_languages
  [ "${DETECTED_LANGS[*]}" = "go py" ]
}

@test "detect_languages: pyproject + requirements deduplicates to py" {
  touch "$TEST_DIR/pyproject.toml" "$TEST_DIR/requirements.txt"
  cd "$TEST_DIR"
  detect_languages
  [ "${DETECTED_LANGS[*]}" = "py" ]
}

@test "detect_languages: package.json detects ts" {
  touch "$TEST_DIR/package.json"
  cd "$TEST_DIR"
  detect_languages
  [ "${DETECTED_LANGS[*]}" = "ts" ]
}

@test "detect_languages: all three markers detected" {
  touch "$TEST_DIR/go.mod" \
    "$TEST_DIR/package.json" \
    "$TEST_DIR/requirements.txt"
  cd "$TEST_DIR"
  detect_languages
  [ "${DETECTED_LANGS[*]}" = "go ts py" ]
}

@test "detect_languages: finds python in subdirectory" {
  mkdir -p "$TEST_DIR/processor"
  touch "$TEST_DIR/processor/requirements.txt"
  cd "$TEST_DIR"
  detect_languages
  [[ " ${DETECTED_LANGS[*]} " == *" py "* ]]
  local found=false
  for entry in "${DETECTED_LANG_PATHS[@]}"; do
    [[ "$entry" == "py:processor" ]] && found=true
  done
  [ "$found" = true ]
}

@test "detect_languages: finds ts in subdirectory" {
  mkdir -p "$TEST_DIR/webapp"
  touch "$TEST_DIR/webapp/package.json"
  cd "$TEST_DIR"
  detect_languages
  [[ " ${DETECTED_LANGS[*]} " == *" ts "* ]]
  local found=false
  for entry in "${DETECTED_LANG_PATHS[@]}"; do
    [[ "$entry" == "ts:webapp" ]] && found=true
  done
  [ "$found" = true ]
}

# ==========================================================
# detect_project_meta
# ==========================================================

@test "detect_project_meta: go.mod extracts APP_NAME and MODULE_PREFIX" {
  mkdir -p "$TEST_DIR"
  echo "module hop.top/myapp" > "$TEST_DIR/go.mod"
  cd "$TEST_DIR"
  git init -q
  git config user.email "test@test.com"
  git config user.name "Test"
  detect_project_meta
  [ "$APP_NAME" = "myapp" ]
  [ "$MODULE_PREFIX" = "hop.top" ]
}

@test "detect_project_meta: package.json extracts APP_NAME" {
  cat > "$TEST_DIR/package.json" <<'JSON'
{
  "name": "myapp",
  "version": "1.0.0"
}
JSON
  cd "$TEST_DIR"
  git init -q
  git config user.email "test@test.com"
  git config user.name "Test"
  detect_project_meta
  [ "$APP_NAME" = "myapp" ]
}

@test "detect_project_meta: fallback uses dirname" {
  cd "$TEST_DIR"
  git init -q
  git config user.email "test@test.com"
  git config user.name "Test"
  detect_project_meta
  [ "$APP_NAME" = "$(basename "$TEST_DIR")" ]
}

# ==========================================================
# sedi
# ==========================================================

@test "sedi replaces content in file" {
  echo "hello" > "$TEST_DIR/sedi_test.txt"
  sedi 's/hello/world/' "$TEST_DIR/sedi_test.txt"
  [ "$(cat "$TEST_DIR/sedi_test.txt")" = "world" ]
}

# ==========================================================
# replace_token
# ==========================================================

@test "replace_token substitutes token in project files" {
  mkdir -p "$TEST_DIR/proj"
  echo "app={{app_name}}" > "$TEST_DIR/proj/config.txt"
  PROJECT_DIR="$TEST_DIR/proj"
  replace_token "{{app_name}}" "myapp"
  [ "$(cat "$TEST_DIR/proj/config.txt")" = "app=myapp" ]
}

# ==========================================================
# file_exists_and_nonempty
# ==========================================================

@test "file_exists_and_nonempty: missing file returns 1" {
  run file_exists_and_nonempty "$TEST_DIR/no_such_file"
  [ "$status" -ne 0 ]
}

@test "file_exists_and_nonempty: empty file returns 1" {
  touch "$TEST_DIR/empty_file"
  run file_exists_and_nonempty "$TEST_DIR/empty_file"
  [ "$status" -ne 0 ]
}

@test "file_exists_and_nonempty: populated file returns 0" {
  echo "data" > "$TEST_DIR/nonempty_file"
  run file_exists_and_nonempty "$TEST_DIR/nonempty_file"
  [ "$status" -eq 0 ]
}

# ==========================================================
# ensure_dir
# ==========================================================

@test "ensure_dir creates non-existent directory" {
  local target="$TEST_DIR/new/nested/dir"
  ensure_dir "$target"
  [ -d "$target" ]
}

@test "ensure_dir on existing directory is no-op" {
  local target="$TEST_DIR/existing"
  mkdir -p "$target"
  run ensure_dir "$target"
  [ "$status" -eq 0 ]
  [ -d "$target" ]
}
