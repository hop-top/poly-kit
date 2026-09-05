#!/usr/bin/env bash
# Cross-language column-ordering conformance harness.
#
# Sibling to run.sh. Where run.sh pins the telemetry ENVELOPE, this pins
# COLUMN ORDER: the five settled rules covering ColumnSpec-driven default
# order, --cols precedence, header == key, empty-payload behavior, and
# priority's Go-only status.
#
# Each runner renders a shared fixture set through its own SDK, RE-PARSES the
# bytes it emitted, and reports the column sequence actually serialized.
# compare_order.py then diffs those observations against expected/ordering.json.
#
# WHY NOT REUSE run.sh's COMPARISON PATH
# --------------------------------------
# run.sh normalises with json.dump(..., sort_keys=True). That canonicalisation
# is right for telemetry envelopes and fatal here: key order is precisely what
# these fixtures assert, so sorting would make every runtime look identical
# and the suite would prove nothing. Observations are compared as ORDERED
# arrays and are never sorted.
#
# Usage:
#   ./run-order.sh              # every detected runtime
#   ./run-order.sh py ts        # restrict to a subset
#
# Exits non-zero if any runtime that actually ran produced a contract
# violation. Skipped runtimes do NOT fail the harness — but they are reported
# as skipped, never as passed: an unrun runtime is not a green runtime.

set -euo pipefail

REAL_HOME="${HOME:-}"

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)"
FIXTURE="${HERE}/fixtures/ordering.json"
EXPECTED="${HERE}/expected/ordering.json"
RUNNERS="${HERE}/runners"
COMPARE="${HERE}/compare_order.py"

if [[ -t 1 ]]; then
  C_GREEN=$'\033[32m'; C_RED=$'\033[31m'; C_YELLOW=$'\033[33m'
  C_BOLD=$'\033[1m'; C_DIM=$'\033[2m'; C_RESET=$'\033[0m'
else
  C_GREEN=''; C_RED=''; C_YELLOW=''; C_BOLD=''; C_DIM=''; C_RESET=''
fi

log()  { printf '%s\n' "$*"; }
ok()   { printf '%s[ok]%s %s\n'   "${C_GREEN}" "${C_RESET}" "$*"; }
fail() { printf '%s[fail]%s %s\n' "${C_RED}"   "${C_RESET}" "$*"; }
skip() { printf '%s[skip]%s %s\n' "${C_YELLOW}" "${C_RESET}" "$*"; }
hd()   { printf '%s== %s ==%s\n'  "${C_BOLD}"  "$*"           "${C_RESET}"; }

TMPDIR_HARNESS="$(mktemp -d -t kit-order-cross-lang.XXXXXX)"
trap 'rm -rf "${TMPDIR_HARNESS}"' EXIT
log "${C_DIM}temp dir: ${TMPDIR_HARNESS}${C_RESET}"

export KIT_CROSS_LANG_ORDER_FIXTURE="${FIXTURE}"

LANGS_REQUESTED=("$@")
SUPPORTED_LANGS=(go py ts rs php)
want() {
  local lang="$1"
  if [[ ${#LANGS_REQUESTED[@]} -eq 0 ]]; then
    return 0
  fi
  for l in "${LANGS_REQUESTED[@]}"; do
    [[ "$l" == "$lang" ]] && return 0
  done
  return 1
}

# ---------------------------------------------------------------------------
# Preconditions. Verified up front so a per-runner failure is about CONTENT
# (a contract violation), never absence of toolchain.
# ---------------------------------------------------------------------------
PY_INTERP=""
check_py() {
  local venv="${HERE}/../../py/.venv/bin/python"
  if [[ -x "${venv}" ]] && "${venv}" -c 'import yaml' >/dev/null 2>&1; then
    PY_INTERP="${venv}"
    return 0
  fi
  if command -v python3 >/dev/null 2>&1 &&
     python3 -c 'import sys; assert sys.version_info >= (3, 11); import yaml' >/dev/null 2>&1; then
    PY_INTERP="$(command -v python3)"
    return 0
  fi
  echo "no python3>=3.11 with pyyaml; run \`uv sync\` in sdk/py/"
  return 1
}
check_ts() {
  if ! command -v node >/dev/null 2>&1; then
    echo "missing node"; return 1
  fi
  local dist="${HERE}/../../ts/dist/output.js"
  if [[ ! -f "${dist}" ]]; then
    echo "missing ts dist bundle (run \`pnpm build\` in sdk/ts/)"; return 1
  fi
  # The output barrel must actually export the ordering surface; a stale
  # bundle predating the ordering work would otherwise fail confusingly.
  if ! node -e "
    const m = require('${dist}');
    if (typeof m.resolveEffectiveCols !== 'function' || typeof m.columnName !== 'function') {
      process.exit(1);
    }" >/dev/null 2>&1; then
    echo "stale ts dist bundle (missing resolveEffectiveCols/columnName; run \`pnpm build\` in sdk/ts/)"
    return 1
  fi
  return 0
}
check_rs() {
  command -v cargo >/dev/null 2>&1 || { echo "missing cargo"; return 1; }
  return 0
}
check_php() {
  command -v php >/dev/null 2>&1 || { echo "missing php"; return 1; }
  local autoload="${HERE}/../../experimental/php/vendor/autoload.php"
  if [[ ! -f "${autoload}" ]]; then
    echo "missing php vendor/autoload.php (run \`composer install\` in sdk/experimental/php/)"; return 1
  fi
  return 0
}
check_go() {
  command -v go >/dev/null 2>&1 || { echo "missing go"; return 1; }
  return 0
}

declare -a RAN=()
declare -a PASSED=()
declare -a FAILED=()
declare -a SKIPPED=()

run_lang() {
  local lang="$1"
  local obs="${TMPDIR_HARNESS}/${lang}.order.jsonl"
  rm -f "${obs}"
  export KIT_CROSS_LANG_ORDER_OUT="${obs}"
  RAN+=("${lang}")

  hd "${lang}"
  case "${lang}" in
    py)
      "${PY_INTERP:-python3}" "${RUNNERS}/py/order.py"
      ;;
    ts)
      node "${RUNNERS}/ts/order.cjs"
      ;;
    rs)
      local real_cargo_home="${CARGO_HOME:-${REAL_HOME:-$HOME}/.cargo}"
      ( cd "${RUNNERS}/rs" && CARGO_HOME="${real_cargo_home}" cargo run --quiet --bin order )
      ;;
    php)
      php "${RUNNERS}/php/order.php"
      ;;
    go)
      ( cd "${HERE}" && go run "${RUNNERS}/go/order" )
      ;;
  esac

  if [[ ! -s "${obs}" ]]; then
    fail "${lang}: runner produced no observations at ${obs}"
    FAILED+=("${lang}")
    return 1
  fi

  if python3 "${COMPARE}" "${lang}" "${obs}" "${EXPECTED}"; then
    ok "${lang}: column ordering matches the contract"
    PASSED+=("${lang}")
    return 0
  else
    fail "${lang}: contract violation — see above"
    FAILED+=("${lang}")
    return 1
  fi
}

for lang in "${SUPPORTED_LANGS[@]}"; do
  want "${lang}" || continue
  _check_msg="$(mktemp -t kit-order-check.XXXXXX)"
  if "check_${lang}" >"${_check_msg}" 2>&1; then
    rm -f "${_check_msg}"
    if ! run_lang "${lang}"; then
      : # already accounted for in FAILED
    fi
  else
    reason="$(cat "${_check_msg}")"
    rm -f "${_check_msg}"
    skip "${lang}: ${reason}"
    SKIPPED+=("${lang}: ${reason}")
  fi
done

hd "summary"
log "ran: ${#RAN[@]}  passed: ${#PASSED[@]}  failed: ${#FAILED[@]}  skipped: ${#SKIPPED[@]}"
if [[ ${#PASSED[@]} -gt 0 ]]; then
  ok "passed: ${PASSED[*]}"
fi
if [[ ${#FAILED[@]} -gt 0 ]]; then
  fail "failed: ${FAILED[*]}"
fi
if [[ ${#SKIPPED[@]} -gt 0 ]]; then
  for s in "${SKIPPED[@]}"; do
    skip "${s}"
  done
fi

if [[ ${#FAILED[@]} -gt 0 ]]; then
  exit 1
fi
exit 0
