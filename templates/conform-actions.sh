#!/usr/bin/env bash
# conform-actions.sh — Action functions for conform.sh
# Sourced by conform.sh; do not run directly.
#
# Expects: SCRIPT_DIR, DRY_RUN, APP_NAME, MODULE_PREFIX,
#   EMAIL, DETECTED_LANGS, log_applied, log_skipped,
#   log_review, ensure_dir, sedi, HAS_GO, HAS_PY, HAS_TS

# Guard against double-sourcing
[[ -n "${_KIT_CONFORM_ACTIONS_LOADED:-}" ]] && return 0
_KIT_CONFORM_ACTIONS_LOADED=1

# ==========================================================
# Language helpers
# ==========================================================

has_lang() {
  local needle="$1"
  local l
  for l in "${DETECTED_LANGS[@]}"; do
    [[ "$l" == "$needle" ]] && return 0
  done
  return 1
}

# ==========================================================
# T-0794: Safe copy-from-template actions
# ==========================================================

# Copy a template file if target is missing.
# Usage: copy_template_if_missing <target> <source> [tok=val ...]
copy_template_if_missing() {
  local target="$1" source="$2"
  shift 2

  if [ -f "$target" ]; then
    log_skipped "$target" "Already exists"
    return 0
  fi

  if [ "$DRY_RUN" = true ]; then
    log_applied "$target" "Would copy from $source (dry run)"
    return 0
  fi

  if [ ! -f "$source" ]; then
    log_skipped "$target" "Template source missing: $source"
    return 0
  fi

  ensure_dir "$(dirname "$target")"
  cp "$source" "$target"

  # Replace tokens: each remaining arg is token=value
  local pair tok val
  for pair in "$@"; do
    tok="${pair%%=*}"
    val="${pair#*=}"
    sedi "s|${tok}|${val}|g" "$target"
  done

  log_applied "$target" "Copied from $source"
}

run_copy_actions() {
  local tpl="$SCRIPT_DIR"

  # --- Go-only files ---
  if has_lang go; then
    copy_template_if_missing \
      ".golangci.yml" \
      "$tpl/cli-go/.golangci.yml"

    copy_template_if_missing \
      ".trivyignore" \
      "$tpl/cli-go/.trivyignore"

    copy_template_if_missing \
      ".goreleaser.yaml" \
      "$tpl/cli-go/.goreleaser.yml" \
      "{{app_name}}=$APP_NAME" \
      "{{module_prefix}}=$MODULE_PREFIX"

    copy_template_if_missing \
      ".github/workflows/ci.yml" \
      "$tpl/shared/ci/ci-go.yml" \
      "{{app_name}}=$APP_NAME"
  fi

  # --- Python-only files ---
  if has_lang py; then
    # resolve python subdir
    local _py_dir="."
    for _e in "${DETECTED_LANG_PATHS[@]}"; do
      case "$_e" in py:*) _py_dir="${_e#py:}" ;; esac
    done
    if [ "$_py_dir" = "." ]; then
      copy_template_if_missing \
        ".github/workflows/ci-py.yml" \
        "$tpl/shared/ci/ci-py.yml"
    else
      # subdir Python: CI paths need customization
      if [ ! -f ".github/workflows/ci-py.yml" ]; then
        log_review ".github/workflows/ci-py.yml" \
          "Python detected in $_py_dir/ (not root).
CI template assumes root-level Python.
Action: copy ci-py.yml and update paths
filter to $_py_dir/**.py and working-directory
to $_py_dir/." S P1
      else
        log_skipped ".github/workflows/ci-py.yml" \
          "exists"
      fi
    fi
  fi

  # --- Shared files ---
  copy_template_if_missing \
    "CONTRIBUTING.md" \
    "$tpl/shared/CONTRIBUTING.md" \
    "{{app_name}}=$APP_NAME"

  copy_template_if_missing \
    "SECURITY.md" \
    "$tpl/shared/SECURITY.md" \
    "{{author_email}}=$EMAIL"

  copy_template_if_missing \
    "docs/RELEASING.md" \
    "$tpl/shared/RELEASING.md" \
    "{{app_name}}=$APP_NAME" \
    "{{module_prefix}}=$MODULE_PREFIX"

  # CHANGELOG.md (go-gated, template may not exist yet)
  if has_lang go; then
    copy_template_if_missing \
      "CHANGELOG.md" \
      "$tpl/cli-go/CHANGELOG.md" \
      "{{app_name}}=$APP_NAME"
  fi

  # promote-release script (template may not exist yet)
  copy_template_if_missing \
    "scripts/promote-release.sh" \
    "$tpl/shared/scripts/promote-release.sh"

  # --- Go version package ---
  if has_lang go && [ ! -d "internal/version" ]; then
    if [ "$DRY_RUN" = true ]; then
      log_applied "internal/version/version.go" \
        "Would generate version package (dry run)"
    else
      ensure_dir "internal/version"
      cat > "internal/version/version.go" <<'GOVEOF'
package version

var (
	ver    = "dev"
	commit = "none"
	date   = "unknown"
)

func Version() string { return ver }
func Commit() string  { return commit }
func Date() string    { return date }
GOVEOF
      log_applied "internal/version/version.go" \
        "Generated version package"
    fi
  fi
}

# ==========================================================
# T-0795: Safe generated actions
# ==========================================================

# Build a dependabot ecosystem block
_dependabot_block() {
  local ecosystem="$1"
  cat <<DEOF

  - package-ecosystem: $ecosystem
    directory: "/"
    schedule:
      interval: weekly
    commit-message:
      prefix: "build(deps):"
DEOF
}

run_generated_actions() {
  # --- dependabot.yml ---
  local dbot=".github/dependabot.yml"
  if [ ! -f "$dbot" ]; then
    if [ "$DRY_RUN" = true ]; then
      log_applied "$dbot" \
        "Would generate dependabot config (dry run)"
    else
      ensure_dir ".github"
      {
        echo "version: 2"
        echo "updates:"
      } > "$dbot"

      has_lang go && _dependabot_block "gomod" >> "$dbot"
      has_lang py && _dependabot_block "pip"   >> "$dbot"
      has_lang ts && _dependabot_block "npm"   >> "$dbot"

      # always include github-actions
      _dependabot_block "github-actions" >> "$dbot"

      log_applied "$dbot" \
        "Generated from detected languages"
    fi
  else
    # Check for missing ecosystems
    local missing=()
    has_lang go \
      && ! grep -q "gomod" "$dbot" \
      && missing+=("gomod")
    has_lang py \
      && ! grep -q "pip" "$dbot" \
      && missing+=("pip")
    has_lang ts \
      && ! grep -q "npm" "$dbot" \
      && missing+=("npm")
    ! grep -q "github-actions" "$dbot" \
      && missing+=("github-actions")

    if [ ${#missing[@]} -gt 0 ]; then
      if [ "$DRY_RUN" = true ]; then
        log_applied "$dbot" \
          "Would append: ${missing[*]} (dry run)"
      else
        for eco in "${missing[@]}"; do
          _dependabot_block "$eco" >> "$dbot"
        done
        log_applied "$dbot" \
          "Appended ecosystems: ${missing[*]}"
      fi
    else
      log_skipped "$dbot" "All ecosystems present"
    fi
  fi

  # --- Release-please ---
  if [ ! -f ".release-please-manifest.json" ]; then
    if [ "$DRY_RUN" = true ]; then
      log_applied "release-please" \
        "Would generate release-please config (dry run)"
    else
      NAME="${APP_NAME}" \
        setup_release_please "." "${DETECTED_LANGS[@]}"
      log_applied "release-please" \
        "Generated release-please config"
    fi
  else
    log_skipped "release-please" "Manifest already exists"
  fi
}

# ==========================================================
# T-0796: Additive merge actions
# ==========================================================

run_merge_actions() {
  _merge_makefile
  _merge_gitignore
}

_merge_makefile() {
  local mkfile="Makefile"
  local marker="# --- kit conform targets ---"

  if [ ! -f "$mkfile" ]; then
    if [ "$DRY_RUN" = true ]; then
      log_applied "$mkfile" \
        "Would create with kit targets (dry run)"
      return 0
    fi
    cat > "$mkfile" <<'MKEOF'
APP_NAME := $(shell basename $(CURDIR))

# --- kit conform targets ---

build:
	mkdir -p bin
	go build -o bin/$(APP_NAME) ./cmd/$(APP_NAME)

clean:
	rm -rf bin/ dist/

release:
	goreleaser release --clean

promote:
	@scripts/promote-release.sh

promote-alpha promote-beta promote-rc promote-release:
	@scripts/promote-release.sh $(subst promote-,,$@)

links:
	@if command -v lychee >/dev/null 2>&1; then \
		lychee --no-progress .; \
	else \
		echo "lychee not installed; skipping link check"; \
	fi
MKEOF
    log_applied "$mkfile" "Created with kit targets"
    return 0
  fi

  # Marker present => already merged
  if grep -qF "$marker" "$mkfile"; then
    log_skipped "$mkfile" "Kit targets already present"
    return 0
  fi

  # Check which targets are missing
  local targets=(
    build clean release promote
    promote-alpha promote-beta promote-rc
    promote-release links
  )
  local missing=()
  for t in "${targets[@]}"; do
    if ! grep -qE "^${t}:" "$mkfile"; then
      missing+=("$t")
    fi
  done

  if [ ${#missing[@]} -eq 0 ]; then
    log_skipped "$mkfile" "All kit targets exist"
    return 0
  fi

  if [ "$DRY_RUN" = true ]; then
    log_applied "$mkfile" \
      "Would append missing: ${missing[*]} (dry run)"
    return 0
  fi

  {
    echo ""
    echo "$marker"
  } >> "$mkfile"

  _append_kit_targets "${missing[@]}" >> "$mkfile"
  log_applied "$mkfile" "Appended missing: ${missing[*]}"
}

# Emit kit target blocks for targets listed in args
_append_kit_targets() {
  local want_list=" $* "

  _wants() { [[ "$want_list" == *" $1 "* ]]; }

  _wants build && cat <<'EOF'

build:
	mkdir -p bin
	go build -o bin/$(APP_NAME) ./cmd/$(APP_NAME)
EOF
  _wants clean && cat <<'EOF'

clean:
	rm -rf bin/ dist/
EOF
  _wants release && cat <<'EOF'

release:
	goreleaser release --clean
EOF
  _wants promote && cat <<'EOF'

promote:
	@scripts/promote-release.sh
EOF
  _wants promote-alpha && cat <<'EOF'

promote-alpha promote-beta promote-rc promote-release:
	@scripts/promote-release.sh $(subst promote-,,$@)
EOF
  _wants links && cat <<'EOF'

links:
	@if command -v lychee >/dev/null 2>&1; then \
		lychee --no-progress .; \
	else \
		echo "lychee not installed; skipping link check"; \
	fi
EOF
}

_merge_gitignore() {
  local gi=".gitignore"
  local marker="# kit conform"
  local entries=("bin/" "dist/" "*.db" \
    "coverage.out" "coverage_e2e.out")

  if [ -f "$gi" ] && grep -qF "$marker" "$gi"; then
    log_skipped "$gi" "Kit entries already present"
    return 0
  fi

  local missing=()
  for e in "${entries[@]}"; do
    if [ ! -f "$gi" ] \
        || ! grep -qxF "$e" "$gi"; then
      missing+=("$e")
    fi
  done

  if [ ${#missing[@]} -eq 0 ]; then
    log_skipped "$gi" "All kit entries present"
    return 0
  fi

  if [ "$DRY_RUN" = true ]; then
    log_applied "$gi" \
      "Would append: ${missing[*]} (dry run)"
    return 0
  fi

  {
    echo ""
    echo "$marker"
    for e in "${missing[@]}"; do
      echo "$e"
    done
  } >> "$gi"

  log_applied "$gi" "Appended: ${missing[*]}"
}

# ==========================================================
# T-0797: LLM review item detection
# ==========================================================

run_review_checks() {
  _review_agents_md
  _review_go_replace
  _review_makefile_conflicts
  _review_py_requirements
}

_review_agents_md() {
  [ -f "AGENTS.md" ] && return 0
  local ep="" pkgs="" pyd="" td="" docs=""
  has_lang go && {
    ep="$(find . -path '*/cmd/*/main.go' \
      2>/dev/null | head -10 | tr '\n' ',')"
    pkgs="$(find . -path './internal/*' -type d \
      -maxdepth 2 2>/dev/null | head -10 | tr '\n' ',')"
  }
  has_lang py && pyd="$(find . -name '__init__.py' \
    ! -path '*/.git/*' ! -path '*/node_modules/*' \
    -exec dirname {} \; 2>/dev/null \
    | head -10 | tr '\n' ',')"
  td="$(find . -type d -name '*test*' \
    ! -path '*/.git/*' ! -path '*/node_modules/*' \
    2>/dev/null | head -10 | tr '\n' ',')"
  [ -d "docs" ] && docs="$(find ./docs -type f \
    2>/dev/null | head -10 | tr '\n' ',')"
  log_review "AGENTS.md" \
    "Context: ${DETECTED_LANGS[*]:-unknown} project $APP_NAME.
Entry points: ${ep:-none}
Packages: ${pkgs:-none}
Python dirs: ${pyd:-none}
Test dirs: ${td:-none}
Docs: ${docs:-none}
Current state: missing
Expected: project-specific agent instructions
Action: create AGENTS.md with build, structure,
conventions, test commands." M P1
}

_review_go_replace() {
  has_lang go || return 0
  [ -f "go.mod" ] || return 0

  local replaces
  replaces="$(grep '^replace' go.mod 2>/dev/null || true)"
  [ -z "$replaces" ] && return 0

  local prompt
  prompt="go.mod contains replace directives:

$replaces

Review each directive. Local path replaces should
be removed before release. Remote replaces may be
intentional forks — confirm or remove."

  log_review "go.mod replace directives" "$prompt" S P2
}

_review_makefile_conflicts() {
  [ -f "Makefile" ] || return 0

  local kit_targets=(
    build clean release promote links
  )
  local marker="# --- kit conform targets ---"

  # Only check if marker NOT present (pre-existing targets)
  grep -qF "$marker" "Makefile" && return 0

  for t in "${kit_targets[@]}"; do
    if grep -qE "^${t}:" "Makefile"; then
      local existing
      existing="$(sed -n "/^${t}:/,/^[^\t]/p" \
        Makefile | head -5)"
      local prompt
      prompt="Makefile target '$t' already exists with body:

$existing

Kit expects a standard '$t' target. Compare and
decide whether to keep existing, replace, or merge."

      log_review "Makefile: $t conflict" "$prompt" S P2
    fi
  done
}

_review_py_requirements() {
  has_lang py || return 0

  # resolve python subdir from DETECTED_LANG_PATHS
  local py_dir="."
  for entry in "${DETECTED_LANG_PATHS[@]}"; do
    case "$entry" in py:*)
      py_dir="${entry#py:}" ;;
    esac
  done

  local req="$py_dir/requirements.txt"
  [ -f "$req" ] || return 0
  [ -f "$py_dir/pyproject.toml" ] && return 0
  log_review "requirements.txt -> pyproject.toml" \
    "Python detected in $py_dir/ via requirements.txt
but no pyproject.toml found. First 30 lines:
$(head -30 "$req")
Action: create $py_dir/pyproject.toml with [project]
deps. Consider uv for dependency management." M P2
}
