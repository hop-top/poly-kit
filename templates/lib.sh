#!/usr/bin/env bash
# lib.sh — Shared utilities for kit template scripts
# Sourced by scaffold.sh, init.sh, conform.sh
# shellcheck disable=SC2034

# Guard against double-sourcing
[[ -n "${_KIT_LIB_LOADED:-}" ]] && return 0
_KIT_LIB_LOADED=1

# ==========================================================
# Tool detection
# ==========================================================

HAS_GH=false
HAS_GLAB=false
HAS_HOP=false
HAS_TLC=false

detect_tools() {
  command -v gh   >/dev/null 2>&1 && HAS_GH=true
  command -v glab >/dev/null 2>&1 && HAS_GLAB=true
  command -v git  >/dev/null 2>&1 && {
    git hop --version >/dev/null 2>&1 && HAS_HOP=true
  }
  command -v tlc  >/dev/null 2>&1 && HAS_TLC=true
}

# ==========================================================
# Argument helpers
# ==========================================================

require_arg() {
  if [ $# -lt 2 ] || [[ "$2" == --* ]]; then
    echo "Error: $1 requires a value" >&2
    exit 1
  fi
}

# ==========================================================
# Interactive prompts
# ==========================================================

prompt() {
  local var="$1" msg="$2" default="${3:-}"
  local input
  if [ -n "$default" ]; then
    printf "%s [%s]: " "$msg" "$default" >&2
  else
    printf "%s: " "$msg" >&2
  fi
  read -r input
  printf -v "$var" '%s' "${input:-$default}"
}

prompt_choice() {
  local var="$1" msg="$2" options="$3" default="$4"
  local input
  printf "%s (%s) [%s]: " "$msg" "$options" "$default" >&2
  read -r input
  printf -v "$var" '%s' "${input:-$default}"
}

prompt_multi() {
  local var="$1" msg="$2" options="$3" default="$4"
  local input
  printf "%s (%s) [%s]: " "$msg" "$options" "$default" >&2
  read -r input
  printf -v "$var" '%s' "${input:-$default}"
}

# ==========================================================
# Portable sed
# ==========================================================

# macOS vs Linux sed -i
sedi() {
  if [[ "$OSTYPE" == darwin* ]]; then
    sed -i '' "$@"
  else
    sed -i "$@"
  fi
}

# Escape string for use as sed replacement
sed_escape() {
  printf '%s' "$1" | sed 's/[&|\/\\]/\\&/g'
}

# ==========================================================
# Token replacement
# ==========================================================

# Replace token in all files under $PROJECT_DIR (must be set
# by caller). Skips .git, node_modules, vendor, and the
# calling script.
replace_token() {
  local token="$1" value
  value="$(sed_escape "$2")"
  local dir="${PROJECT_DIR:-.}"
  local files
  files="$(find "$dir" -type f \
    ! -path '*/.git/*' \
    ! -path '*/node_modules/*' \
    ! -path '*/vendor/*' \
    ! -name 'init.sh' \
    -exec grep -rl "$token" {} + 2>/dev/null || true)"
  [ -z "$files" ] && return 0
  while IFS= read -r f; do
    sedi "s|${token}|${value}|g" "$f"
  done <<< "$files"
}

# ==========================================================
# Language / project detection
# ==========================================================

# Scan cwd + one level deep for language markers.
# Populates DETECTED_LANGS (unique lang list) and
# DETECTED_LANG_PATHS (lang:path pairs, colon-separated).
detect_languages() {
  DETECTED_LANGS=()
  DETECTED_LANG_PATHS=()

  # Go: root only (go.mod at root is the convention)
  if [ -f "go.mod" ]; then
    DETECTED_LANGS+=("go")
    DETECTED_LANG_PATHS+=("go:.")
  fi

  # TypeScript: root or one level deep
  if [ -f "package.json" ]; then
    DETECTED_LANGS+=("ts")
    DETECTED_LANG_PATHS+=("ts:.")
  else
    for d in */; do
      if [ -f "${d}package.json" ]; then
        DETECTED_LANGS+=("ts")
        DETECTED_LANG_PATHS+=("ts:${d%/}")
        break
      fi
    done
  fi

  # Python: root or one level deep
  local _py_found=false
  for marker in pyproject.toml requirements.txt; do
    if [ -f "$marker" ]; then
      _py_found=true
      DETECTED_LANG_PATHS+=("py:.")
      break
    fi
  done
  if [ "$_py_found" = false ]; then
    for d in */; do
      for marker in pyproject.toml requirements.txt; do
        if [ -f "${d}${marker}" ]; then
          _py_found=true
          DETECTED_LANG_PATHS+=("py:${d%/}")
          break 2
        fi
      done
    done
  fi
  if [ "$_py_found" = true ]; then
    DETECTED_LANGS+=("py")
  fi
}

# Extract project metadata from cwd into shell variables:
#   APP_NAME, MODULE_PREFIX, AUTHOR, EMAIL
detect_project_meta() {
  # APP_NAME: go.mod module basename > package.json name > dirname
  if [ -f "go.mod" ]; then
    local mod
    mod="$(head -1 go.mod | awk '{print $2}')"
    APP_NAME="$(basename "$mod")"
    MODULE_PREFIX="$(dirname "$mod")"
  elif [ -f "package.json" ]; then
    APP_NAME="$(
      grep -m1 '"name"' package.json \
        | sed 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/'
    )"
    MODULE_PREFIX=""
  else
    APP_NAME="$(basename "$(pwd)")"
    MODULE_PREFIX=""
  fi

  AUTHOR="$(git config user.name 2>/dev/null || echo "")"
  EMAIL="$(git config user.email 2>/dev/null || echo "")"
}

# ==========================================================
# Filesystem helpers
# ==========================================================

ensure_dir() {
  mkdir -p "$1"
}

# Returns 0 if file exists and is non-empty, 1 otherwise
file_exists_and_nonempty() {
  [ -s "$1" ]
}
