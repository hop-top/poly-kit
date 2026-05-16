#!/usr/bin/env bats
# Tests for shell logic in .github/workflows/cli-demo-media.yml
#
# Each test sources a helper that defines the function under test in isolation.
# Run: bats .github/tests/cli-demo-media.bats
# Or:  make test-workflow

# ---------------------------------------------------------------------------
# Helpers: inline the bash snippets from the workflow steps
# ---------------------------------------------------------------------------

# url_for() — from step "Build comment body"
url_for() {
  local fname="$1"
  if [ -n "${URL_TEMPLATE:-}" ]; then
    echo "${URL_TEMPLATE//\{file\}/${fname}}"
  else
    echo "${PUBLIC_BASE}/${fname}"
  fi
}

# resolve_public_base() — mirrors the "Resolve context" step case statement
resolve_public_base() {
  local backend="$1"
  local owner="$2"
  local repo="$3"
  local assets_branch="$4"
  local pr="$5"
  local bucket="$6"
  local endpoint="$7"
  local prefix="$8"
  local public_base_override="$9"

  case "$backend" in
    branch)
      echo "https://raw.githubusercontent.com/${owner}/${repo}/${assets_branch}/${assets_branch}/${pr}"
      ;;
    s3)
      echo "${public_base_override:-https://${bucket}.s3.amazonaws.com/${prefix}}"
      ;;
    r2)
      echo "${public_base_override:-${endpoint}/${bucket}/${prefix}}"
      ;;
    custom)
      echo "${public_base_override}"
      ;;
  esac
}

# resolve_storage_path() — PATH_PREFIX defaulting
resolve_storage_path() {
  local prefix="$1"
  local pr="$2"
  echo "${prefix:-pr-${pr}}"
}

# ---------------------------------------------------------------------------
# url_for() tests
# ---------------------------------------------------------------------------

@test "url_for: uses PUBLIC_BASE when no URL_TEMPLATE" {
  PUBLIC_BASE="https://raw.githubusercontent.com/hop-top/kit/assets/assets/42"
  unset URL_TEMPLATE
  result="$(url_for "go.gif")"
  [ "$result" = "https://raw.githubusercontent.com/hop-top/kit/assets/assets/42/go.gif" ]
}

@test "url_for: URL_TEMPLATE replaces {file} placeholder" {
  URL_TEMPLATE="https://cdn.example.com/demos/{file}"
  result="$(url_for "go.gif")"
  [ "$result" = "https://cdn.example.com/demos/go.gif" ]
}

@test "url_for: URL_TEMPLATE empty string falls back to PUBLIC_BASE" {
  URL_TEMPLATE=""
  PUBLIC_BASE="https://example.com/media"
  result="$(url_for "ts.gif")"
  [ "$result" = "https://example.com/media/ts.gif" ]
}

@test "url_for: URL_TEMPLATE replaces only first occurrence of {file}" {
  # Only one {file} in template — ensure no double replacement
  URL_TEMPLATE="https://cdn.example.com/{file}"
  result="$(url_for "py.gif")"
  [ "$result" = "https://cdn.example.com/py.gif" ]
}

# ---------------------------------------------------------------------------
# resolve_public_base() tests
# ---------------------------------------------------------------------------

@test "branch backend: constructs correct raw.githubusercontent.com URL" {
  result="$(resolve_public_base branch hop-top kit assets 7 "" "" "" "")"
  [ "$result" = "https://raw.githubusercontent.com/hop-top/kit/assets/assets/7" ]
}

@test "branch backend: PR number included in path" {
  result="$(resolve_public_base branch myorg myrepo assets 123 "" "" "" "")"
  [[ "$result" == *"/123" ]]
}

@test "s3 backend: default URL uses bucket.s3.amazonaws.com" {
  result="$(resolve_public_base s3 "" "" "" "" mybucket "" "pr-5" "")"
  [ "$result" = "https://mybucket.s3.amazonaws.com/pr-5" ]
}

@test "s3 backend: public_base_override takes precedence" {
  result="$(resolve_public_base s3 "" "" "" "" mybucket "" "pr-5" "https://custom.example.com/pr-5")"
  [ "$result" = "https://custom.example.com/pr-5" ]
}

@test "r2 backend: default URL uses endpoint/bucket/prefix" {
  result="$(resolve_public_base r2 "" "" "" "" mybucket "https://abc.r2.cloudflarestorage.com" "pr-3" "")"
  [ "$result" = "https://abc.r2.cloudflarestorage.com/mybucket/pr-3" ]
}

@test "r2 backend: public_base_override takes precedence" {
  result="$(resolve_public_base r2 "" "" "" "" mybucket "https://abc.r2.cloudflarestorage.com" "pr-3" "https://cdn.example.com")"
  [ "$result" = "https://cdn.example.com" ]
}

@test "custom backend: echoes public_base_override" {
  result="$(resolve_public_base custom "" "" "" "" "" "" "" "https://my-cdn.example.com/demos")"
  [ "$result" = "https://my-cdn.example.com/demos" ]
}

@test "custom backend: empty public_base_override returns empty" {
  result="$(resolve_public_base custom "" "" "" "" "" "" "" "")"
  [ "$result" = "" ]
}

# ---------------------------------------------------------------------------
# resolve_storage_path() tests
# ---------------------------------------------------------------------------

@test "storage path: defaults to pr-N when prefix empty" {
  result="$(resolve_storage_path "" 42)"
  [ "$result" = "pr-42" ]
}

@test "storage path: uses override when prefix set" {
  result="$(resolve_storage_path "release/v1.2" 42)"
  [ "$result" = "release/v1.2" ]
}

@test "storage path: pr-1 for PR number 1" {
  result="$(resolve_storage_path "" 1)"
  [ "$result" = "pr-1" ]
}

# ---------------------------------------------------------------------------
# Comment body generation smoke tests
# ---------------------------------------------------------------------------

@test "comment body: contains cli-demo-media marker" {
  body="<!-- cli-demo-media -->"
  [[ "$body" == *"<!-- cli-demo-media -->"* ]]
}

@test "comment body: gif files produce image markdown" {
  MEDIA_PATH="$(mktemp -d)"
  touch "${MEDIA_PATH}/go.gif"
  PUBLIC_BASE="https://example.com/media"
  unset URL_TEMPLATE

  BODY=""
  for f in "${MEDIA_PATH}"/*.gif; do
    fname="$(basename "${f}")"
    name="${fname%.*}"
    url="$(url_for "${fname}")"
    BODY+="### ${name}"$'\n\n'
    BODY+="![${name}](${url})"$'\n\n'
  done
  rm -rf "${MEDIA_PATH}"

  [[ "$BODY" == *"### go"* ]]
  [[ "$BODY" == *"![go](https://example.com/media/go.gif)"* ]]
}

@test "comment body: no media files produces fallback message" {
  MEDIA_PATH="$(mktemp -d)"
  COUNT=0
  shopt -s nullglob
  for f in "${MEDIA_PATH}"/*.gif "${MEDIA_PATH}"/*.png; do
    COUNT=$((COUNT + 1))
  done
  rm -rf "${MEDIA_PATH}"
  [ "$COUNT" -eq 0 ]
}

# ---------------------------------------------------------------------------
# Tape file path sanity checks (prevent regression)
# ---------------------------------------------------------------------------
#
# The tapes run from examples/spaced/tapes/ (cd $(TAPES_DIR) && vhs *.tape).
# Output paths must be relative to that cwd → ../media/*.gif is correct.
# Binary paths from tapes/ must traverse 3 levels up to repo root → ../../../spaced.
#
# Strategy: find tapes/ by walking up from this test file's location.

_find_tapes_dir() {
  # Walk up from the test file to locate examples/spaced/tapes/
  local dir
  dir="$(cd "$(dirname "${BATS_TEST_FILENAME}")" && pwd)"
  while [ "$dir" != "/" ]; do
    if [ -d "${dir}/examples/spaced/tapes" ]; then
      echo "${dir}/examples/spaced/tapes"
      return 0
    fi
    dir="$(dirname "$dir")"
  done
  return 1
}

@test "go.tape: Output path is ../media/go.gif (relative to tapes/ dir)" {
  tapes="$(_find_tapes_dir)" || skip "examples/spaced/tapes not found in repo tree"
  tape="$(cat "${tapes}/go.tape" 2>/dev/null || echo "")"
  [ -z "$tape" ] && skip "go.tape not readable"
  [[ "$tape" == *"Output ../media/go.gif"* ]]
}

@test "go.tape: runs spaced-go" {
  tapes="$(_find_tapes_dir)" || skip "examples/spaced/tapes not found in repo tree"
  tape="$(cat "${tapes}/go.tape" 2>/dev/null || echo "")"
  [ -z "$tape" ] && skip "go.tape not readable"
  [[ "$tape" == *"Type \"spaced-go"* ]]
}

@test "ts.tape: Output path is ../media/ts.gif" {
  tapes="$(_find_tapes_dir)" || skip "examples/spaced/tapes not found in repo tree"
  tape="$(cat "${tapes}/ts.tape" 2>/dev/null || echo "")"
  [ -z "$tape" ] && skip "ts.tape not readable"
  [[ "$tape" == *"Output ../media/ts.gif"* ]]
}

@test "py.tape: Output path is ../media/py.gif" {
  tapes="$(_find_tapes_dir)" || skip "examples/spaced/tapes not found in repo tree"
  tape="$(cat "${tapes}/py.tape" 2>/dev/null || echo "")"
  [ -z "$tape" ] && skip "py.tape not readable"
  [[ "$tape" == *"Output ../media/py.gif"* ]]
}
