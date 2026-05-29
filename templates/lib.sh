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

  # Rust: root or one level deep
  local _rs_found=false
  if [ -f "Cargo.toml" ]; then
    _rs_found=true
    DETECTED_LANG_PATHS+=("rs:.")
  else
    for d in */; do
      if [ -f "${d}Cargo.toml" ]; then
        _rs_found=true
        DETECTED_LANG_PATHS+=("rs:${d%/}")
        break
      fi
    done
  fi
  if [ "$_rs_found" = true ]; then
    DETECTED_LANGS+=("rs")
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

# ==========================================================
# Template directory resolution
# ==========================================================

# Resolve `templates/` dir (parent of this lib.sh). build.sh and
# scaffold.sh both live at `templates/`, so KIT_TEMPLATES_DIR
# points at their common parent.
#
# When init.sh runs in the rendered project, lib.sh is copied to
# the project's parent (see scaffold.sh `_lib_tmp`); the resolved
# KIT_TEMPLATES_DIR won't contain a `shared/` subtree, but init.sh
# never calls the composition helpers below, so that's safe.
KIT_TEMPLATES_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KIT_TEMPLATES_SHARED="$KIT_TEMPLATES_DIR/shared"

# ==========================================================
# Polyglot composition helpers (build-time + scaffold-time)
# ==========================================================
#
# These functions are the SOT for composing per-lang shared
# infrastructure. build.sh uses them to build single-lang
# dists; scaffold.sh uses them to overlay polyglot content on
# top of `cli-template-base` based on user-selected --langs
# subset.
#
# Marker syntax mirrors `templates/shared/managed-block.sh`
# (labeled variant: lines 75-76). Composed files survive a
# re-scaffold / `kit init --update` because the markers delimit
# the kit-managed region.

# compose_gitignore <dest> <lang...>
# Merges common + per-lang gitignore files into dest/.gitignore.
# Tolerates zero lang args (common only).
compose_gitignore() {
  local dest="$1"; shift
  local parts=("$KIT_TEMPLATES_SHARED/gitignore/common.gitignore")
  for lang in "$@"; do
    parts+=("$KIT_TEMPLATES_SHARED/gitignore/${lang}.gitignore")
  done
  {
    printf '# >>> kit-managed: gitignore >>>\n'
    cat "${parts[@]}"
    printf '# <<< kit-managed: gitignore <<<\n'
  } > "$dest/.gitignore"
}

# compose_gitattributes <dest> <lang...>
# Merges common + per-lang gitattributes files into
# dest/.gitattributes. Tolerates zero lang args.
compose_gitattributes() {
  local dest="$1"; shift
  local parts=("$KIT_TEMPLATES_SHARED/gitattributes/common.gitattributes")
  for lang in "$@"; do
    parts+=("$KIT_TEMPLATES_SHARED/gitattributes/${lang}.gitattributes")
  done
  {
    printf '# >>> kit-managed: gitattributes >>>\n'
    cat "${parts[@]}"
    printf '# <<< kit-managed: gitattributes <<<\n'
  } > "$dest/.gitattributes"
}

# copy_shared <dest>
# Copies community files, docs, devcontainer artifacts, init.sh.
copy_shared() {
  local dest="$1"
  local src="$KIT_TEMPLATES_SHARED"

  cp "$src/LICENSE-MIT.tmpl" "$dest/LICENSE-MIT"
  cp "$src/LICENSE-Apache-2.0.tmpl" "$dest/LICENSE-Apache-2.0"
  cp "$src/SECURITY.md.tmpl" "$dest/SECURITY.md"
  cp "$src/CONTRIBUTING.md.tmpl" "$dest/CONTRIBUTING.md"
  cp "$src/RELEASING.md" "$dest/"
  cp "$src/init.sh" "$dest/"
  chmod +x "$dest/init.sh"

  # Docs
  cp -r "$src/docs" "$dest/"
  mkdir -p "$dest/docs/stories" "$dest/docs/personas"
  touch "$dest/docs/stories/.gitkeep"
  touch "$dest/docs/personas/.gitkeep"

  # Scripts
  mkdir -p "$dest/scripts"
  cp "$src/scripts/"* "$dest/scripts/"
  chmod +x "$dest/scripts/"*

  # Devcontainer — emitted at scaffold time by
  # templates/shared/emit-devcontainer-json.sh +
  # emit-docker-compose.sh; no template files copied here.
}

# dependabot_ecosystem <lang>
# Echoes the dependabot package-ecosystem string for a lang
# (gomod/npm/pip/cargo/composer). Empty string for unknown.
dependabot_ecosystem() {
  case "$1" in
    go)  printf 'gomod' ;;
    ts)  printf 'npm' ;;
    py)  printf 'pip' ;;
    rs)  printf 'cargo' ;;
    php) printf 'composer' ;;
    *)   printf '' ;;
  esac
}

# ci_workflow_src <lang>
# Echoes the absolute path to the per-lang CI workflow template,
# preferring `.yml` over `.yml.tmpl`.
ci_workflow_src() {
  local lang="$1"
  local src="$KIT_TEMPLATES_SHARED/ci/ci-${lang}.yml"
  [ -f "$src" ] || src="${src}.tmpl"
  printf '%s' "$src"
}

# overlay_lang_dir <dest> <lang> <src_root>
# Copies cli-<lang>/* into $dest/<lang>/, EXCLUDING the root-
# level community files that are already provided by the base
# template (CHANGELOG.md, README.md, .gitignore, .gitattributes,
# .github/). Mirrors the old polyglot-build behavior.
overlay_lang_dir() {
  local dest="$1" lang="$2" src_root="$3"
  local src="$src_root/cli-${lang}"
  local langdir="$dest/$lang"
  mkdir -p "$langdir"
  if [ ! -d "$src" ]; then
    echo "Warning: $src not found, skipping" >&2
    return
  fi
  for entry in "$src"/* "$src"/.*; do
    local base
    base="$(basename "$entry")"
    case "$base" in
      .|..|CHANGELOG.md|README.md|.gitignore|.gitattributes|.github)
        continue
        ;;
      *)
        cp -r "$entry" "$langdir/"
        ;;
    esac
  done
}

# compose_polyglot_dependabot <dest> <lang...>
# Emits a polyglot dependabot.yml with one entry per lang, watching
# each `/<lang>` subdirectory.
compose_polyglot_dependabot() {
  local dest="$1"; shift
  mkdir -p "$dest/.github"
  local out="$dest/.github/dependabot.yml"
  {
    printf 'version: 2\n'
    printf 'updates:\n'
    local first=true lang ecosystem
    for lang in "$@"; do
      ecosystem="$(dependabot_ecosystem "$lang")"
      if [ -z "$ecosystem" ]; then
        echo "Warning: no dependabot ecosystem for lang=$lang" >&2
        continue
      fi
      if [ "$first" = true ]; then
        first=false
      else
        printf '\n'
      fi
      printf '  - package-ecosystem: %s\n' "$ecosystem"
      printf '    directory: "/%s"\n' "$lang"
      printf '    schedule:\n'
      printf '      interval: weekly\n'
      printf '    commit-message:\n'
      printf '      prefix: "build(deps):"\n'
    done
  } > "$out"
}

# copy_polyglot_ci_workflows <dest> <lang...>
# Copies per-lang CI workflow into $dest/.github/workflows/ci-<lang>.yml.
copy_polyglot_ci_workflows() {
  local dest="$1"; shift
  mkdir -p "$dest/.github/workflows"
  local lang src
  for lang in "$@"; do
    src="$(ci_workflow_src "$lang")"
    if [ ! -f "$src" ]; then
      echo "Warning: CI template missing for lang=$lang ($src)" >&2
      continue
    fi
    cp "$src" "$dest/.github/workflows/ci-${lang}.yml"
  done
}

# compose_polyglot_makefile <dest> <lang...>
# Emits a root Makefile that delegates targets to per-lang
# subdirs ONLY for the langs in LANG_ARRAY.
compose_polyglot_makefile() {
  local dest="$1"; shift
  local out="$dest/Makefile"
  {
    printf '.PHONY: build test lint links check setup\n\n'
    printf 'check: lint test links\n\n'
    printf 'build test lint setup:\n'
    local lang
    for lang in "$@"; do
      # shellcheck disable=SC2016
      # Intentional literal `$(MAKE)` + `$@` for the make recipe.
      printf '\t$(MAKE) -C %s $@\n' "$lang"
    done
    printf '\n'
    printf 'links:\n'
    printf '\t@if command -v lychee >/dev/null 2>&1; then \\\n'
    printf '\t\tlychee --no-progress .; \\\n'
    printf '\telse \\\n'
    printf '\t\techo "lychee not installed; skipping link check"; \\\n'
    printf '\tfi\n'
  } > "$out"
}

# compose_polyglot_readme_structure <readme_path> <lang...>
# Inserts a `## Structure` section (listing only the chosen lang
# subdirs) into the rendered README, immediately before the
# existing `## Development` heading. Idempotent: a re-invocation
# is wrapped in kit-managed markers so subsequent invocations
# replace the block in place.
# _lang_label <lang> — echo a human label for a lang code.
_lang_label() {
  case "$1" in
    go)  printf 'Go CLI source' ;;
    ts)  printf 'TypeScript CLI source' ;;
    py)  printf 'Python CLI source' ;;
    rs)  printf 'Rust CLI source' ;;
    php) printf 'PHP CLI source' ;;
    *)   printf '%s source' "$1" ;;
  esac
}

# _emit_structure_block <lang...> — emit the structure block
# (markers + heading + bullets) to stdout. Split out from
# compose_polyglot_readme_structure so the `case` statement
# isn't trapped inside `$(...)` (bash parser bug with `)` in
# case patterns inside command substitution).
_emit_structure_block() {
  local lang
  printf '# >>> kit-managed: structure >>>\n'
  printf '## Structure\n\n'
  for lang in "$@"; do
    # shellcheck disable=SC2016
    # Markdown literal backticks inside the format string.
    printf -- '- `%s/` — %s\n' "$lang" "$(_lang_label "$lang")"
  done
  printf '\n'
  printf '# <<< kit-managed: structure <<<\n'
}

compose_polyglot_readme_structure() {
  local readme="$1"; shift
  [ -f "$readme" ] || return 0

  # Write the new block to a temp file so awk can `getline` it
  # verbatim — `-v block="..."` rejects multi-line values on BSD
  # + POSIX awks.
  local block_file tmp
  block_file="$(mktemp)"
  _emit_structure_block "$@" > "$block_file"
  tmp="$(mktemp)"

  # Strip any existing managed-structure block, then insert the
  # new block before the existing `## Development` heading.
  # Falls back to appending at end-of-file when `## Development`
  # is absent.
  awk -v blockfile="$block_file" '
    function emit_block(    line) {
      while ((getline line < blockfile) > 0) print line
      close(blockfile)
    }
    BEGIN { in_block = 0; injected = 0 }
    /^# >>> kit-managed: structure >>>/ { in_block = 1; next }
    in_block && /^# <<< kit-managed: structure <<</ { in_block = 0; next }
    in_block { next }
    /^## Development$/ && !injected {
      emit_block()
      injected = 1
    }
    { print }
    END {
      if (!injected) {
        print ""
        emit_block()
      }
    }
  ' "$readme" > "$tmp"
  mv "$tmp" "$readme"
  rm -f "$block_file"
}
