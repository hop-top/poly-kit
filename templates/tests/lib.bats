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
# detect_tools
# ==========================================================

@test "detect_tools: succeeds when no optional tool is installed" {
  # scaffold.sh runs under set -e; a non-zero return here killed it
  # silently on any host without tlc (CI runners).
  mkdir -p "$TEST_DIR/bin"
  ln -s "$(command -v git)" "$TEST_DIR/bin/git"
  PATH="$TEST_DIR/bin" run detect_tools
  [ "$status" -eq 0 ]
}

@test "detect_tools: scaffold-style set -e caller survives missing tlc" {
  mkdir -p "$TEST_DIR/bin"
  ln -s "$(command -v dirname)" "$TEST_DIR/bin/dirname"
  run env PATH="$TEST_DIR/bin" /bin/bash -c \
    "set -e; source '$TEMPLATES_DIR/lib.sh'; detect_tools; echo reached"
  [ "$status" -eq 0 ]
  [ "$output" = "reached" ]
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
# strip_template_comments
# ==========================================================
# kit init renders every template file through Go text/template, where
# {{/* ... */}} is a comment and {{- / -}} trim adjacent whitespace
# (newlines included). The shell path must drop the same text.

@test "strip_template_comments drops {{/* */}} in place" {
  mkdir -p "$TEST_DIR/proj"
  printf '  "kit": "^1.2.3",{{/* x-release-please-version */}}\n  "b": 1\n' >"$TEST_DIR/proj/package.json"
  PROJECT_DIR="$TEST_DIR/proj"
  strip_template_comments
  [ "$(cat "$TEST_DIR/proj/package.json")" = "$(printf '  "kit": "^1.2.3",\n  "b": 1')" ]
}

@test "strip_template_comments: {{- trims preceding whitespace" {
  mkdir -p "$TEST_DIR/proj"
  printf 'require (\n\thop.top/kit v0.5.0 {{- /* x-release-please-version */}}\n)\n' >"$TEST_DIR/proj/go.mod"
  PROJECT_DIR="$TEST_DIR/proj"
  strip_template_comments
  [ "$(cat "$TEST_DIR/proj/go.mod")" = "$(printf 'require (\n\thop.top/kit v0.5.0\n)')" ]
}

@test "strip_template_comments: -}} trims following whitespace across lines" {
  mkdir -p "$TEST_DIR/proj"
  printf 'a {{/* c */ -}}\n\n  b\n' >"$TEST_DIR/proj/f.txt"
  PROJECT_DIR="$TEST_DIR/proj"
  strip_template_comments
  [ "$(cat "$TEST_DIR/proj/f.txt")" = "a b" ]
}

@test "strip_template_comments leaves non-comment actions and lone /* alone" {
  mkdir -p "$TEST_DIR/proj"
  printf '{{ .Version }} /* c */ {{`{{x}}`}} {{ /* not a comment */ }}\n' >"$TEST_DIR/proj/f.txt"
  PROJECT_DIR="$TEST_DIR/proj"
  strip_template_comments
  [ "$(cat "$TEST_DIR/proj/f.txt")" = '{{ .Version }} /* c */ {{`{{x}}`}} {{ /* not a comment */ }}' ]
}

@test "strip_template_comments: no marker in any shipped template survives" {
  out="$TEST_DIR/proj"
  mkdir -p "$out"
  for t in cli-go cli-ts cli-rs cli-php; do
    cp -R "$TEMPLATES_DIR/$t" "$out/$t"
  done
  PROJECT_DIR="$out"
  strip_template_comments
  ! grep -rqF 'x-release-please-version */' "$out"
  # The rendered package.json bodies parse once tokens are filled in.
  sed 's/{{\.[A-Za-z]*}}/x/g' "$out/cli-ts/package.json.tmpl" >"$out/p.json"
  if command -v node >/dev/null 2>&1; then
    node -e 'JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"))' "$out/p.json"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$out/p.json"
  fi
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

# ==========================================================
# dependabot github-actions entry + pinned CI workflows
# ==========================================================

LINT_PINNED="${BATS_TEST_DIRNAME}/../../scripts/lint-actions-pinned.sh"

@test "dependabot_actions_entry matches kit init's shared/ci/dependabot.yml" {
  local shared entry
  shared="$(cat "$TEMPLATES_DIR/shared/ci/dependabot.yml")"
  entry="$(dependabot_actions_entry)"
  [[ "$entry" == *"package-ecosystem: github-actions"* ]]
  [[ "$shared" == *"$entry" ]]
}

@test "compose_polyglot_dependabot appends the github-actions entry" {
  compose_polyglot_dependabot "$TEST_DIR" go ts
  local out="$TEST_DIR/.github/dependabot.yml"
  grep -q 'package-ecosystem: gomod' "$out"
  grep -q 'package-ecosystem: npm' "$out"
  [[ "$(cat "$out")" == *"$(dependabot_actions_entry)" ]]
}

@test "copy_polyglot_ci_workflows output pins every uses: ref" {
  copy_polyglot_ci_workflows "$TEST_DIR" go ts py rs php
  run "$LINT_PINNED" "$TEST_DIR"/.github/workflows/*.yml
  [ "$status" -eq 0 ] || { echo "$output"; false; }
}

@test "build.sh single-lang dist: pinned ci.yml + github-actions dependabot" {
  cp -R "$TEMPLATES_DIR" "$TEST_DIR/templates"
  rm -rf "$TEST_DIR/templates/dist"
  bash "$TEST_DIR/templates/build.sh" >/dev/null
  local lang dist
  for lang in go ts py rs php; do
    dist="$TEST_DIR/templates/dist/cli-template-$lang"
    run "$LINT_PINNED" "$dist/.github/workflows/ci.yml"
    [ "$status" -eq 0 ] || { echo "$lang: $output"; false; }
    [[ "$(cat "$dist/.github/dependabot.yml")" == *"$(dependabot_actions_entry)" ]] \
      || { echo "$lang: dependabot.yml lacks github-actions entry"; false; }
  done
}

@test "conform _dependabot_block github-actions emits the shared entry" {
  # shellcheck source=../conform-actions.sh
  source "$TEMPLATES_DIR/conform-actions.sh"
  run _dependabot_block github-actions
  [ "$status" -eq 0 ]
  [ "$output" = "$(printf '\n%s' "$(dependabot_actions_entry)")" ]
}
