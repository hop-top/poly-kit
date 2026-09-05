#!/usr/bin/env bash
# Cross-language compliance conformance harness.
#
# Sibling to run.sh and run-order.sh. Where run.sh pins the telemetry
# ENVELOPE and run-order.sh pins COLUMN ORDER, this pins the COMPLIANCE
# VERDICT: the score, the denominator, and every factor's status when the
# three ports check the same opt-in toolspec.
#
# The fixture opts into telemetry (`telemetry.enabled: true`), so a
# conforming port must run its F13 "Consenting Telemetry" check and report
# a denominator of 13. A port that never ported F13 reports 12 and fails
# here even if all twelve of its other factors agree.
#
# WHY THE VERDICT AND NOT THE BYTES
# ---------------------------------
# Report bytes are not comparable across ports and never were: Go marshals
# a struct, TS an interface, Python a dataclass, each with its own elision
# rules for empty details/suggestion fields. None of that is contractual.
# The score, the denominator, and the per-factor verdicts are. Only the
# static pass runs — runtime checks execute a binary no two ports could
# agree on, and F13 is a static check in every port regardless.
#
# Usage:
#   ./run-compliance.sh          # every detected runtime
#   ./run-compliance.sh go py    # restrict to a subset
#
# Exits non-zero if any runtime that actually ran disagreed with the
# contract. Skipped runtimes do NOT fail the harness — but they are
# reported as skipped, never as passed: an unrun runtime is not a green
# runtime.

set -euo pipefail

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)"
FIXTURE="${HERE}/fixtures/compliance.toolspec.yaml"
EXPECTED="${HERE}/expected/compliance.json"
RUNNERS="${HERE}/runners"
COMPARE="${HERE}/compare_compliance.py"
TS_SDK="${HERE}/../../ts"

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

TMPDIR_HARNESS="$(mktemp -d -t kit-compliance-cross-lang.XXXXXX)"
trap 'rm -rf "${TMPDIR_HARNESS}"' EXIT
log "${C_DIM}temp dir: ${TMPDIR_HARNESS}${C_RESET}"

export KIT_CROSS_LANG_COMPLIANCE_FIXTURE="${FIXTURE}"

LANGS_REQUESTED=("$@")
SUPPORTED_LANGS=(go ts py)
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
# (a parity violation), never absence of toolchain.
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

TS_BUNDLE=""
check_ts() {
  if ! command -v node >/dev/null 2>&1; then
    echo "missing node"; return 1
  fi
  # Unlike the ordering harness there is no dist/ bundle to consume:
  # compliance is not in the package exports map and tsup does not build
  # it. Bundle the working-tree source with the SDK's own esbuild, which
  # also removes any chance of testing a stale artifact.
  local esbuild="${TS_SDK}/node_modules/.bin/esbuild"
  if [[ ! -x "${esbuild}" ]]; then
    echo "missing esbuild (run \`pnpm install\` in sdk/ts/)"; return 1
  fi
  if [[ ! -f "${TS_SDK}/node_modules/js-yaml/package.json" ]]; then
    echo "missing js-yaml (run \`pnpm install\` in sdk/ts/)"; return 1
  fi
  TS_BUNDLE="${TMPDIR_HARNESS}/compliance.bundle.cjs"
  if ! ( cd "${TS_SDK}" && "${esbuild}" src/compliance.ts \
           --bundle --platform=node --format=cjs \
           --outfile="${TS_BUNDLE}" --log-level=error ); then
    echo "esbuild failed to bundle sdk/ts/src/compliance.ts"
    return 1
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
  local obs="${TMPDIR_HARNESS}/${lang}.compliance.json"
  rm -f "${obs}"
  export KIT_CROSS_LANG_COMPLIANCE_OUT="${obs}"
  RAN+=("${lang}")

  hd "${lang}"
  case "${lang}" in
    go)
      ( cd "${HERE}" && go run "${RUNNERS}/go/compliance" )
      ;;
    ts)
      KIT_CROSS_LANG_COMPLIANCE_TS_BUNDLE="${TS_BUNDLE}" \
        node "${RUNNERS}/ts/compliance.cjs"
      ;;
    py)
      "${PY_INTERP:-python3}" "${RUNNERS}/py/compliance.py"
      ;;
  esac

  if [[ ! -s "${obs}" ]]; then
    fail "${lang}: runner produced no observation at ${obs}"
    FAILED+=("${lang}")
    return 1
  fi

  if python3 "${COMPARE}" "${lang}" "${obs}" "${EXPECTED}"; then
    ok "${lang}: compliance verdict matches the contract"
    PASSED+=("${lang}")
    return 0
  else
    fail "${lang}: parity violation — see above"
    FAILED+=("${lang}")
    return 1
  fi
}

for lang in "${SUPPORTED_LANGS[@]}"; do
  want "${lang}" || continue
  # Invoke check_<lang> in the CURRENT shell so it can export per-lang
  # state (PY_INTERP, TS_BUNDLE). Capture stdout via a tmp file rather
  # than $(...) to avoid the subshell that would drop those variables.
  _check_msg="$(mktemp -t kit-compliance-check.XXXXXX)"
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
