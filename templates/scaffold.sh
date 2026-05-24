#!/usr/bin/env bash
# shellcheck disable=SC1091
set -euo pipefail

# -------------------------------------------------------
# scaffold.sh — Create a new CLI project from templates
#
# Usage: scaffold.sh <name> [flags]
#
# Builds templates, creates forge repo (optional), clones,
# runs init.sh, wires issue tracking, syncs labels.
# -------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=setup-release-please.sh
source "$SCRIPT_DIR/setup-release-please.sh"

# shellcheck source=reserve-packages.sh
source "$SCRIPT_DIR/reserve-packages.sh"

# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

# shellcheck source=shared/managed-block.sh
source "$SCRIPT_DIR/shared/managed-block.sh"

# shellcheck source=shared/emit-mise.sh
source "$SCRIPT_DIR/shared/emit-mise.sh"

# shellcheck source=shared/emit-devcontainer-json.sh
source "$SCRIPT_DIR/shared/emit-devcontainer-json.sh"

# shellcheck source=shared/emit-docker-compose.sh
source "$SCRIPT_DIR/shared/emit-docker-compose.sh"

# shellcheck source=shared/emit-env-example.sh
source "$SCRIPT_DIR/shared/emit-env-example.sh"

# shellcheck source=shared/apply-services.sh
source "$SCRIPT_DIR/shared/apply-services.sh"

# --- Tool detection ------------------------------------

detect_tools

# --- Defaults ------------------------------------------

NAME=""
OUTPUT=""
LANG="go"
DESCRIPTION=""
LICENSE="apache"
AUTHOR="$(git config user.name 2>/dev/null || echo "")"
EMAIL="$(git config user.email 2>/dev/null || echo "")"
FORGE=""
PRIVATE=false
HOMEPAGE=""
ORG=""
MODULE_PREFIX=""
NO_TLC=false
NO_PUSH=false
NO_DEVCONTAINER=false
SERVICES=""
NO_SERVICES=false

# Auto-detect forge
if [ "$HAS_GH" = true ]; then
  FORGE="github"
elif [ "$HAS_GLAB" = true ]; then
  FORGE="gitlab"
fi

# --- Usage ---------------------------------------------

usage() {
  cat <<USAGE
Usage: scaffold.sh <name> [flags]

Create a new CLI project from templates.

Arguments:
  <name>                Project name (required)

Flags:
  --output DIR          Output directory (default: ./<name>)
  --lang LANG           Language: go, ts, py, rs, or comma-separated
                        for polyglot (default: go)
  --description TEXT    Project description
  --license LICENSE     License: apache, mit (default: apache)
  --author NAME         Author name (default: git user.name)
  --email EMAIL         Author email (default: git user.email)
  --forge FORGE         Forge: github, gitlab, none (default: auto)
  --private             Create private repo (default: public)
  --homepage URL        Project homepage URL
  --org ORG             Organization/group for forge repo
  --module-prefix PFX   Module prefix (e.g. github.com/user)
USAGE

  if [ "$HAS_TLC" = true ]; then
    echo "  --no-tlc              Skip tlc initialization"
  fi
  if [ -n "$FORGE" ] && [ "$FORGE" != "none" ]; then
    echo "  --no-push             Skip initial push to forge"
  fi

  cat <<USAGE
  --no-devcontainer     Skip .devcontainer scaffolding
  --services LIST       Comma-separated catalog services: postgres,redis,minio,mailpit,redpanda
  --no-services         Strip default telemetry services (rare)
  -h, --help            Show this help

Examples:
  scaffold.sh myapp
  scaffold.sh myapp --lang go,ts --forge github --org myorg
  scaffold.sh myapp --lang py --no-push
  scaffold.sh myapp --lang rs --forge github --org myorg
USAGE
  exit 0
}

# --- Arg parsing ---------------------------------------

while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help)
      usage
      ;;
    --output)
      require_arg "$1" "${2:-}"
      OUTPUT="$2"; shift 2
      ;;
    --lang)
      require_arg "$1" "${2:-}"
      LANG="$2"; shift 2
      ;;
    --description)
      require_arg "$1" "${2:-}"
      DESCRIPTION="$2"; shift 2
      ;;
    --license)
      require_arg "$1" "${2:-}"
      LICENSE="$2"; shift 2
      ;;
    --author)
      require_arg "$1" "${2:-}"
      AUTHOR="$2"; shift 2
      ;;
    --email)
      require_arg "$1" "${2:-}"
      EMAIL="$2"; shift 2
      ;;
    --forge)
      require_arg "$1" "${2:-}"
      FORGE="$2"; shift 2
      ;;
    --private)
      PRIVATE=true; shift
      ;;
    --homepage)
      require_arg "$1" "${2:-}"
      HOMEPAGE="$2"; shift 2
      ;;
    --org)
      require_arg "$1" "${2:-}"
      ORG="$2"; shift 2
      ;;
    --module-prefix)
      require_arg "$1" "${2:-}"
      MODULE_PREFIX="$2"; shift 2
      ;;
    --no-tlc)
      if [ "$HAS_TLC" = true ]; then
        NO_TLC=true
      else
        echo "Error: --no-tlc requires tlc to be installed" >&2
        exit 1
      fi
      shift
      ;;
    --no-push)
      NO_PUSH=true; shift
      ;;
    --no-devcontainer)
      NO_DEVCONTAINER=true; shift
      ;;
    --services)
      require_arg "$1" "${2:-}"
      SERVICES="$2"; shift 2
      ;;
    --no-services)
      NO_SERVICES=true; shift
      ;;
    -*)
      echo "Error: unknown flag: $1" >&2
      exit 1
      ;;
    *)
      if [ -z "$NAME" ]; then
        NAME="$1"
      else
        echo "Error: unexpected argument: $1" >&2
        exit 1
      fi
      shift
      ;;
  esac
done

# --- Interactive prompts (if stdin is a tty) -----------

prompt_if_empty() {
  local var="$1" msg="$2" default="${3:-}"
  local cur="${!var}"
  if [ -n "$cur" ]; then return; fi
  if [ ! -t 0 ]; then return; fi
  local input
  if [ -n "$default" ]; then
    printf "%s [%s]: " "$msg" "$default" >&2
  else
    printf "%s: " "$msg" >&2
  fi
  read -r input
  printf -v "$var" '%s' "${input:-$default}"
}

prompt_if_empty NAME "Project name"
prompt_if_empty DESCRIPTION "Description"
prompt_if_empty AUTHOR "Author name"
prompt_if_empty EMAIL "Author email"

# --- Validation ----------------------------------------

if [ -z "$NAME" ]; then
  echo "Error: project name is required" >&2
  exit 1
fi

# Validate license
case "$LICENSE" in
  apache|Apache-2.0|mit|MIT) ;;
  *)
    echo "Error: invalid license: $LICENSE (must be apache or mit)" >&2
    exit 1
    ;;
esac

# Validate lang values
IFS=',' read -ra LANG_ARRAY <<< "$LANG"
for l in "${LANG_ARRAY[@]}"; do
  case "$l" in
    go|ts|py|rs) ;;
    *)
      echo "Error: invalid language: $l (must be go, ts, py, or rs)" >&2
      exit 1
      ;;
  esac
done

# Validate --services list against catalog
if [ -n "$SERVICES" ]; then
  IFS=',' read -ra _SVC_ARRAY <<< "$SERVICES"
  for s in "${_SVC_ARRAY[@]}"; do
    s_trimmed="$(echo "$s" | tr -d ' ')"
    [ -z "$s_trimmed" ] && continue
    case "$s_trimmed" in
      postgres|redis|minio|mailpit|redpanda) ;;
      *)
        echo "Error: unknown service: $s_trimmed (must be one of: postgres, redis, minio, mailpit, redpanda)" >&2
        exit 1
        ;;
    esac
  done
fi

if [ "$NO_SERVICES" = true ] && [ -n "$SERVICES" ]; then
  echo "Error: --no-services and --services are mutually exclusive" >&2
  exit 1
fi

# Determine if polyglot
IS_POLYGLOT=false
if [ "${#LANG_ARRAY[@]}" -gt 1 ]; then
  IS_POLYGLOT=true
fi

# Set output default
if [ -z "$OUTPUT" ]; then
  OUTPUT="./$NAME"
fi

# Check output doesn't already exist
if [ -e "$OUTPUT" ]; then
  echo "Error: output directory already exists: $OUTPUT" >&2
  exit 1
fi

# Validate forge
case "$FORGE" in
  github|gitlab|none|"") ;;
  *)
    echo "Error: invalid forge: $FORGE (must be github, gitlab, or none)" >&2
    exit 1
    ;;
esac

# Verify forge CLI + auth
if [ "$FORGE" = "github" ]; then
  if [ "$HAS_GH" != true ]; then
    echo "Error: gh CLI not found. Install: https://cli.github.com" >&2
    exit 1
  fi
  if ! gh auth status >/dev/null 2>&1; then
    echo "Error: gh not authenticated. Run: gh auth login" >&2
    exit 1
  fi
elif [ "$FORGE" = "gitlab" ]; then
  if [ "$HAS_GLAB" != true ]; then
    echo "Error: glab CLI not found. Install: https://gitlab.com/gitlab-org/cli" >&2
    exit 1
  fi
  if ! glab auth status >/dev/null 2>&1; then
    echo "Error: glab not authenticated. Run: glab auth login" >&2
    exit 1
  fi
fi

# Default module prefix from forge + org
if [ -z "$MODULE_PREFIX" ]; then
  if [ "$FORGE" = "github" ] && [ -n "$ORG" ]; then
    MODULE_PREFIX="github.com/$ORG"
  elif [ "$FORGE" = "gitlab" ] && [ -n "$ORG" ]; then
    MODULE_PREFIX="gitlab.com/$ORG"
  fi
fi

echo "Validated: $NAME (lang=$LANG forge=${FORGE:-none})"

# --- Template assembly ---------------------------------

TEMPLATES_DIR="$SCRIPT_DIR"
DIST="$TEMPLATES_DIR/dist"

echo "Building templates..."
bash "$TEMPLATES_DIR/build.sh"

if [ "$IS_POLYGLOT" = true ]; then
  TEMPLATE_SRC="$DIST/cli-template-polyglot"
else
  TEMPLATE_SRC="$DIST/cli-template-${LANG_ARRAY[0]}"
fi

if [ ! -d "$TEMPLATE_SRC" ]; then
  echo "Error: template not found: $TEMPLATE_SRC" >&2
  exit 3
fi

echo "Using template: $(basename "$TEMPLATE_SRC")"

# --- Forge repo creation -------------------------------

REPO_URL=""

if [ -n "$FORGE" ] && [ "$FORGE" != "none" ]; then
  echo "Creating forge repository..."

  REPO_NAME="$NAME"
  if [ -n "$ORG" ]; then
    REPO_NAME="$ORG/$NAME"
  fi

  VISIBILITY="--public"
  if [ "$PRIVATE" = true ]; then
    VISIBILITY="--private"
  fi

  if [ "$FORGE" = "github" ]; then
    GH_ARGS=("repo" "create" "$REPO_NAME" "$VISIBILITY")
    [ -n "$DESCRIPTION" ] && GH_ARGS+=("--description" "$DESCRIPTION")
    [ -n "$HOMEPAGE" ] && GH_ARGS+=("--homepage" "$HOMEPAGE")

    if ! REPO_URL="$(gh "${GH_ARGS[@]}" 2>&1)"; then
      if echo "$REPO_URL" | grep -qi "already exists"; then
        echo "Error: repository already exists: $REPO_NAME" >&2
        exit 2
      fi
      echo "Error: failed to create repository: $REPO_URL" >&2
      exit 1
    fi
    echo "Created: $REPO_URL"

  elif [ "$FORGE" = "gitlab" ]; then
    GLAB_ARGS=("project" "create" "$NAME")
    [ -n "$ORG" ] && GLAB_ARGS+=("--group" "$ORG")
    [ -n "$DESCRIPTION" ] && GLAB_ARGS+=("--description" "$DESCRIPTION")
    if [ "$PRIVATE" = true ]; then
      GLAB_ARGS+=("--visibility" "private")
    else
      GLAB_ARGS+=("--visibility" "public")
    fi

    if ! REPO_URL="$(glab "${GLAB_ARGS[@]}" 2>&1)"; then
      if echo "$REPO_URL" | grep -qi "already exists"; then
        echo "Error: repository already exists: $REPO_NAME" >&2
        exit 2
      fi
      echo "Error: failed to create repository: $REPO_URL" >&2
      exit 1
    fi
    echo "Created: $REPO_URL"
  fi
fi

# --- Clone + populate + init ---------------------------

echo "Setting up project directory..."

# Detect if we're inside a git hop hub
IN_HUB=false
if [ "$HAS_HOP" = true ]; then
  git hop status >/dev/null 2>&1 && IN_HUB=true
fi

if [ -n "$REPO_URL" ] && [ "$IN_HUB" = true ] && [ "$HAS_HOP" = true ]; then
  # Use git hop to add worktree from remote
  /usr/bin/git hop add "$NAME" 2>/dev/null || {
    git clone "$REPO_URL" "$OUTPUT"
  }
  # hop puts it under hops/<name>; adjust OUTPUT if needed
  if [ -d "hops/$NAME" ] && [ ! -d "$OUTPUT" ]; then
    OUTPUT="hops/$NAME"
  fi
elif [ -n "$REPO_URL" ]; then
  git clone "$REPO_URL" "$OUTPUT"
else
  mkdir -p "$OUTPUT"
  git -C "$OUTPUT" init
fi

# Populate: copy template files into project dir
echo "Copying template files..."
cp -r "$TEMPLATE_SRC/"* "$OUTPUT/" 2>/dev/null || true
# Copy hidden files
for f in "$TEMPLATE_SRC"/.*; do
  case "$(basename "$f")" in
    .|..) continue ;;
    *)    cp -r "$f" "$OUTPUT/" ;;
  esac
done

# Map license flag to init.sh value
INIT_LICENSE="MIT"
case "$LICENSE" in
  apache|Apache-2.0) INIT_LICENSE="Apache-2.0" ;;
  mit|MIT)           INIT_LICENSE="MIT" ;;
esac

# Run init.sh with env var overrides
echo "Running init.sh..."
export INIT_APP_NAME="$NAME"
export INIT_DESCRIPTION="$DESCRIPTION"
export INIT_AUTHOR_NAME="$AUTHOR"
export INIT_AUTHOR_EMAIL="$EMAIL"
export INIT_LICENSE
export INIT_MODULE_PREFIX="$MODULE_PREFIX"
if [ "$IS_POLYGLOT" = true ]; then
  export INIT_LANGUAGES="$LANG"
fi

# init.sh sources ../lib.sh relative to its location
_lib_tmp="$(cd "$OUTPUT" && cd .. && pwd)/lib.sh"
cp "$SCRIPT_DIR/lib.sh" "$_lib_tmp"
(cd "$OUTPUT" && bash init.sh)
rm -f "$_lib_tmp"

# --- Devcontainer / compose emission --------------------

if [ "$NO_DEVCONTAINER" = false ]; then
  echo "Emitting .devcontainer/devcontainer.json..."
  emit_devcontainer_json "$OUTPUT" "$NAME" "$LANG"
  echo "Emitting .devcontainer/docker-compose.yml..."
  emit_docker_compose "$OUTPUT" "$NAME"
fi

# --- Post-clone setup ----------------------------------

# Emit kit-managed mise.toml from the central tool-versions
# manifest, scoped to the project's selected langs.
echo "Emitting mise.toml..."
emit_mise "$OUTPUT" "$LANG"

# Emit .env.example with kit-adapter env vars (telemetry,
# storage, queue, log, config).
echo "Emitting .env.example..."
emit_env_example "$OUTPUT" "$NAME"

# --- Services catalog ----------------------------------

if [ "$NO_DEVCONTAINER" = false ]; then
  if [ "$NO_SERVICES" = true ]; then
    echo "Stripping default telemetry services..."
    apply_no_services "$OUTPUT"
  elif [ -n "$SERVICES" ]; then
    echo "Applying services: $SERVICES..."
    apply_services "$OUTPUT" "$NAME" "$SERVICES"
  fi
fi

# a. tlc init
if [ "$HAS_TLC" = true ] && [ "$NO_TLC" = false ]; then
  echo "Initializing tlc..."
  (cd "$OUTPUT" && tlc init 2>/dev/null) || true
fi

# b. Issue tracker selection (interactive)
if [ "$HAS_TLC" = true ] && [ "$NO_TLC" = false ] && [ -t 0 ]; then
  TRACKERS=()
  TRACKER_LABELS=()

  # Always offer local
  TRACKERS+=("local")
  TRACKER_LABELS+=("local")

  # Detect authed services
  if [ "$HAS_GH" = true ] && gh auth status >/dev/null 2>&1; then
    TRACKERS+=("github")
    TRACKER_LABELS+=("github")
  fi
  if [ "$HAS_GLAB" = true ] && glab auth status >/dev/null 2>&1; then
    TRACKERS+=("gitlab")
    TRACKER_LABELS+=("gitlab")
  fi
  if command -v linear >/dev/null 2>&1; then
    TRACKERS+=("linear")
    TRACKER_LABELS+=("linear")
  fi

  echo ""
  echo "Available issue trackers:"
  for i in "${!TRACKER_LABELS[@]}"; do
    echo "  $((i + 1)). ${TRACKER_LABELS[$i]}"
  done

  printf "Select trackers (comma-separated numbers) [1]: " >&2
  read -r tracker_input
  tracker_input="${tracker_input:-1}"

  IFS=',' read -ra SELECTED_NUMS <<< "$tracker_input"
  SELECTED_TRACKERS=()
  for num in "${SELECTED_NUMS[@]}"; do
    num="$(echo "$num" | tr -d ' ')"
    [[ "$num" =~ ^[0-9]+$ ]] || continue
    idx=$((num - 1))
    if [ "$idx" -ge 0 ] && [ "$idx" -lt "${#TRACKERS[@]}" ]; then
      SELECTED_TRACKERS+=("${TRACKERS[$idx]}")
    fi
  done

  # Configure tlc for selected trackers
  for tracker in "${SELECTED_TRACKERS[@]}"; do
    echo "  Configuring tracker: $tracker"
    (cd "$OUTPUT" && tlc config set "trackers.$tracker.enabled" true 2>/dev/null) || true
  done
fi

# c. Forge repo settings (GitHub)
if [ "$FORGE" = "github" ] && [ -n "$REPO_URL" ] && [ -t 0 ]; then
  REPO_FULL="$REPO_NAME"

  # Disable issues if external tracker selected
  HAS_EXTERNAL=false
  for t in "${SELECTED_TRACKERS[@]:-}"; do
    case "$t" in
      github|gitlab|linear) HAS_EXTERNAL=true ;;
    esac
  done

  if [ "$HAS_EXTERNAL" = true ]; then
    printf "Disable GitHub Issues (using external tracker)? [Y/n]: " >&2
    read -r disable_issues
    if [ "${disable_issues:-Y}" != "n" ] && [ "${disable_issues:-Y}" != "N" ]; then
      gh api -X PATCH "repos/$REPO_FULL" \
        -f has_issues=false >/dev/null 2>&1 || true
      echo "  Disabled GitHub Issues"
    fi
  fi

  printf "Disable GitHub Wiki? [Y/n]: " >&2
  read -r disable_wiki
  if [ "${disable_wiki:-Y}" != "n" ] && [ "${disable_wiki:-Y}" != "N" ]; then
    gh api -X PATCH "repos/$REPO_FULL" \
      -f has_wiki=false >/dev/null 2>&1 || true
    echo "  Disabled GitHub Wiki"
  fi
fi

# d. tlc label sync
if [ "$HAS_TLC" = true ] && [ "$NO_TLC" = false ]; then
  echo "Syncing labels..."
  (cd "$OUTPUT" && tlc label sync 2>/dev/null) || true
fi

# e. Generate .github/copilot-instructions.md
echo "Generating copilot instructions..."
mkdir -p "$OUTPUT/.github"

{
  echo "# Copilot Instructions"
  echo ""
  echo "## Project"
  echo ""
  echo "- Name: $NAME"
  [ -n "$DESCRIPTION" ] && echo "- Description: $DESCRIPTION"
  echo "- License: $INIT_LICENSE"
  echo ""
  echo "## Languages"
  echo ""
  for l in "${LANG_ARRAY[@]}"; do
    case "$l" in
      go)
        echo "- Go: use standard library where possible, \`golangci-lint\` for linting"
        ;;
      ts)
        echo "- TypeScript: strict mode, ESLint + Prettier, vitest for testing"
        ;;
      py)
        echo "- Python: type hints required, ruff for linting, pytest for testing"
        ;;
      rs)
        echo "- Rust: stable toolchain, \`cargo fmt\` + \`cargo clippy -D warnings\`, \`cargo test --all-features\` for testing"
        ;;
    esac
  done
  echo ""
  echo "## Build"
  echo ""
  echo "- Build system: Make"
  echo "- Run \`make check\` before committing"
  echo "- Commits: Conventional Commits (feat|fix|refactor|...)"
} > "$OUTPUT/.github/copilot-instructions.md"

# f. release-please config
setup_release_please "$OUTPUT" "${LANG_ARRAY[@]}"

# --- First commit + push -------------------------------

# Stage copilot instructions (init.sh already committed template files)
(cd "$OUTPUT" && git add -A && git commit -m "feat: scaffold $NAME" --allow-empty) || true

if [ "$NO_PUSH" = false ] && [ -n "$REPO_URL" ]; then
  echo "Pushing to remote..."
  (cd "$OUTPUT" && git push -u origin main 2>/dev/null) || \
  (cd "$OUTPUT" && git push -u origin "$(git branch --show-current)" 2>/dev/null) || true
fi

# --- Package name reservation ----------------------------

if [ "$NO_PUSH" = false ]; then
  reserve_package_names
fi

echo ""
echo "Project ready at $OUTPUT"
