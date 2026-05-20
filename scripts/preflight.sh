#!/usr/bin/env bash
# preflight.sh — Verify host toolchain matches the repo's declared minimums.
#
# Source of truth for each tool's requirement:
#
#   go        →  go.mod                     `go N.N[.N]` directive
#   node      →  sdk/ts/package.json        `engines.node`
#   pnpm      →  presence + `pnpm --version` (>= 9 by repo convention)
#   uv        →  presence (https://docs.astral.sh/uv)
#   python    →  sdk/py/pyproject.toml      `requires-python`
#   php       →  sdk/experimental/php/composer.json   `require.php`
#   composer  →  presence + `composer --version` (>= 2)
#   cargo     →  presence
#   bats      →  presence (templates/tests)
#   jq        →  presence (lint-config)
#   yq        →  presence (lint-config)
#   buf       →  presence (proto generation)
#   lychee    →  presence (lint-links)
#   markdownlint-cli2 → presence (lint-docs; uses npx)
#
# Detection ALSO covers the GOROOT/GOBIN-leak class of bugs that bit us
# during the cli-php landing: `go env GOROOT` must resolve to the same
# install as `which go`, and the on-disk `compile` binary version must
# match `go version` output.
#
# Usage:
#   scripts/preflight.sh           # check everything, exit 0/1
#   scripts/preflight.sh --quick   # skip slow checks (compile-version smoke)
#
# Output: green ✓ for satisfied, yellow ! for missing-but-optional,
# red ✗ for hard-fail. Each ✗ includes a "Run X to fix" hint.

set -u

QUICK=0
[[ "${1:-}" == "--quick" ]] && QUICK=1

# ANSI styling — disabled when NO_COLOR or non-TTY.
if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
  RED=$'\033[31m' GREEN=$'\033[32m' YELLOW=$'\033[33m' BOLD=$'\033[1m' DIM=$'\033[2m' OFF=$'\033[0m'
else
  RED='' GREEN='' YELLOW='' BOLD='' DIM='' OFF=''
fi

ok()    { printf '%s✓%s %-12s %s\n' "$GREEN" "$OFF" "$1" "$2"; }
warn()  { printf '%s!%s %-12s %s\n' "$YELLOW" "$OFF" "$1" "$2"; warnings=$((warnings+1)); }
fail()  { printf '%s✗%s %-12s %s\n%s    %sfix:%s %s%s\n' \
            "$RED" "$OFF" "$1" "$2" "$DIM" "$BOLD" "$OFF" "$3" "$OFF"; failures=$((failures+1)); }

failures=0
warnings=0

# Resolve repo root from this script's location so the file lookups
# below work regardless of CWD when make invokes us.
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd)

# ---------------------------------------------------------------------------
# Go
# ---------------------------------------------------------------------------
go_required=$(awk '/^go [0-9]/ { print $2; exit }' "$REPO_ROOT/go.mod" 2>/dev/null || true)
if ! command -v go >/dev/null 2>&1; then
  fail "go" "not installed (go.mod requires $go_required)" \
       "mise install go@${go_required}    # or: brew install go"
else
  go_have=$(go version 2>/dev/null | awk '{ print $3 }' | sed 's/^go//')
  if [[ -n "$go_required" && "$go_have" != "$go_required" ]]; then
    # Major.minor must match; patch may differ. go.mod accepts ≥ requirement.
    req_major_minor=${go_required%.*}
    have_major_minor=${go_have%.*}
    if [[ "$have_major_minor" != "$req_major_minor" ]]; then
      fail "go" "$go_have installed; go.mod requires $go_required" \
           "mise install go@${go_required}    # mise.toml is the right pin location"
    else
      ok "go" "$go_have (go.mod: $go_required)"
    fi
  else
    ok "go" "$go_have"
  fi
  # GOROOT-leak check: catches the cli-php-landing bug class.
  if [[ $QUICK -eq 0 ]]; then
    goroot=$(go env GOROOT 2>/dev/null || true)
    go_real=$(command -v go | xargs -I{} sh -c 'readlink -f "{}" 2>/dev/null || readlink "{}" || echo "{}"')
    # The real install dir is two levels up from the binary (bin/go → root).
    go_install=$(cd -- "$(dirname -- "$go_real")/.." && pwd 2>/dev/null || true)
    if [[ -n "$goroot" && -n "$go_install" && "$goroot" != "$go_install" ]]; then
      fail "goroot" "stale GOROOT=$goroot (binary at $go_install)" \
           "unset GOROOT GOBIN   # then re-open shell; do not export these"
    fi
    # compile-binary version sanity: catches mid-upgrade Go installs.
    arch_dir="$goroot/pkg/tool/$(go env GOOS)_$(go env GOARCH)"
    if [[ -x "$arch_dir/compile" ]]; then
      compile_ver=$("$arch_dir/compile" -V 2>/dev/null | awk '{ print $3 }' | sed 's/^go//' || true)
      if [[ -n "$compile_ver" && "$compile_ver" != "$go_have" ]]; then
        fail "compile" "go is $go_have but compile is $compile_ver" \
             "mise install   # re-install the toolchain so tool binaries match"
      fi
    fi
  fi
fi

# ---------------------------------------------------------------------------
# Node + pnpm
# ---------------------------------------------------------------------------
node_required=$(grep -oE '"node":\s*"[^"]+"' "$REPO_ROOT/sdk/ts/package.json" 2>/dev/null \
                | head -1 | sed -E 's/.*"node":\s*"([^"]+)".*/\1/' || true)
if ! command -v node >/dev/null 2>&1; then
  fail "node" "not installed (sdk/ts wants node $node_required)" \
       "mise install node@20    # or: brew install node"
else
  node_have=$(node --version 2>/dev/null | sed 's/^v//')
  node_major=${node_have%%.*}
  if [[ -n "$node_required" && "$node_required" =~ \>=([0-9]+) && "$node_major" -lt "${BASH_REMATCH[1]}" ]]; then
    fail "node" "v$node_have installed; sdk/ts wants $node_required" \
         "mise install node@${BASH_REMATCH[1]}"
  else
    ok "node" "v$node_have (sdk/ts: $node_required)"
  fi
fi

if ! command -v pnpm >/dev/null 2>&1; then
  fail "pnpm" "not installed (pnpm-workspace.yaml + sdk/ts use pnpm)" \
       "corepack enable && corepack prepare pnpm@latest --activate"
else
  pnpm_have=$(pnpm --version 2>/dev/null)
  ok "pnpm" "$pnpm_have"
fi

# ---------------------------------------------------------------------------
# uv + Python
# ---------------------------------------------------------------------------
py_required=$(grep -oE 'requires-python\s*=\s*"[^"]+"' "$REPO_ROOT/sdk/py/pyproject.toml" 2>/dev/null \
              | sed -E 's/.*"([^"]+)".*/\1/' || true)
if ! command -v python3 >/dev/null 2>&1; then
  fail "python" "python3 not on PATH (sdk/py requires $py_required)" \
       "mise install python@3.11    # or: brew install python@3.11"
else
  py_have=$(python3 --version 2>/dev/null | awk '{ print $2 }')
  py_major_minor=$(echo "$py_have" | awk -F. '{ print $1"."$2 }')
  if [[ -n "$py_required" && "$py_required" =~ \>=([0-9]+\.[0-9]+) ]]; then
    req_mm="${BASH_REMATCH[1]}"
    if [[ "$(printf '%s\n%s\n' "$req_mm" "$py_major_minor" | sort -V | head -1)" != "$req_mm" ]]; then
      fail "python" "$py_have installed; sdk/py wants $py_required" \
           "mise install python@$req_mm"
    else
      ok "python" "$py_have (sdk/py: $py_required)"
    fi
  else
    ok "python" "$py_have"
  fi
fi

if ! command -v uv >/dev/null 2>&1; then
  fail "uv" "not installed (sdk/py + engine/sdk/py-kit-engine use uv)" \
       "curl -LsSf https://astral.sh/uv/install.sh | sh    # or: brew install uv"
else
  uv_have=$(uv --version 2>/dev/null | awk '{ print $2 }')
  ok "uv" "$uv_have"
fi

# ---------------------------------------------------------------------------
# PHP + Composer
# ---------------------------------------------------------------------------
php_required=$(grep -oE '"php":\s*"[^"]+"' "$REPO_ROOT/sdk/experimental/php/composer.json" 2>/dev/null \
               | sed -E 's/.*"([^"]+)".*/\1/' || true)
if ! command -v php >/dev/null 2>&1; then
  fail "php" "not installed (sdk/experimental/php requires $php_required)" \
       "mise install php@8.3    # or: brew install php@8.3"
else
  php_have=$(php -v 2>/dev/null | head -1 | awk '{ print $2 }')
  php_major_minor=$(echo "$php_have" | awk -F. '{ print $1"."$2 }')
  # composer caret "^8.3" → require >= 8.3 < 9.0
  if [[ "$php_required" =~ \^([0-9]+\.[0-9]+) ]]; then
    req_mm="${BASH_REMATCH[1]}"
    if [[ "$(printf '%s\n%s\n' "$req_mm" "$php_major_minor" | sort -V | head -1)" != "$req_mm" ]]; then
      fail "php" "$php_have installed; composer.json wants $php_required" \
           "mise install php@$req_mm"
    else
      ok "php" "$php_have (composer.json: $php_required)"
    fi
  else
    ok "php" "$php_have"
  fi
fi

if ! command -v composer >/dev/null 2>&1; then
  fail "composer" "not installed (sdk/experimental/php uses composer)" \
       "brew install composer    # or: https://getcomposer.org/download/"
else
  composer_have=$(composer --version --no-ansi 2>/dev/null | awk '{ print $3 }')
  ok "composer" "$composer_have"
fi

# ---------------------------------------------------------------------------
# Rust (cargo). No version pin; check presence only.
# ---------------------------------------------------------------------------
if ! command -v cargo >/dev/null 2>&1; then
  fail "cargo" "not installed (sdk/experimental/rs uses cargo)" \
       "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh"
else
  cargo_have=$(cargo --version 2>/dev/null | awk '{ print $2 }')
  ok "cargo" "$cargo_have"
fi

# ---------------------------------------------------------------------------
# Tooling utilities — presence + version only. Optional ones use warn().
# ---------------------------------------------------------------------------
check_present() {  # tool, install-hint
  local tool="$1" hint="$2"
  if ! command -v "$tool" >/dev/null 2>&1; then
    fail "$tool" "not installed" "$hint"
  else
    local ver
    ver=$("$tool" --version 2>/dev/null | head -1 || true)
    ok "$tool" "$ver"
  fi
}
check_optional() {  # tool, what-it-enables, install-hint
  local tool="$1" purpose="$2" hint="$3"
  if ! command -v "$tool" >/dev/null 2>&1; then
    warn "$tool" "not installed ($purpose); $hint"
  else
    local ver
    ver=$("$tool" --version 2>/dev/null | head -1 || true)
    ok "$tool" "$ver"
  fi
}

check_present jq "brew install jq"
check_optional yq "lint-config uses yq as a fallback to jq" "brew install yq"
check_present bats "brew install bats-core"
check_optional buf "needed for 'make proto'; skip unless touching contracts/proto" "brew install buf"
check_optional lychee "needed for 'make lint-links'" "brew install lychee   # or: cargo install lychee"

# markdownlint-cli2 is invoked via npx — just verify npx is reachable.
if ! command -v npx >/dev/null 2>&1; then
  warn "npx" "not on PATH (needed for 'make lint-docs'); ships with node"
else
  ok "npx" "$(npx --version 2>/dev/null)"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo
if [[ $failures -gt 0 ]]; then
  printf '%s%d issue(s)%s • %d warning(s)\n' "$RED" "$failures" "$OFF" "$warnings"
  printf '%sFix the items above before running %smake%s.%s\n' "$RED" "$BOLD" "$OFF" "$OFF"
  exit 1
fi
if [[ $warnings -gt 0 ]]; then
  printf '%spreflight ok%s • %d optional tool(s) missing\n' "$GREEN" "$OFF" "$warnings"
else
  printf '%spreflight ok%s\n' "$GREEN" "$OFF"
fi
exit 0
