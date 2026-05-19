#!/usr/bin/env bash
# Cross-language telemetry contract harness (T-0709).
#
# Drives the py / ts / rs / php SDK record() paths against a shared
# deterministic fixture, captures each language's JSONL output, normalises
# out the per-language and per-run volatile fields (occurred_at, sdk_lang,
# sdk_version), and diffs against expected/envelope.json.
#
# Per-language runners are SKIPPED (with a printed reason) when the
# corresponding toolchain or SDK build artifact is unavailable. CI runs
# with all four; local devs may only have some.
#
# Usage:
#   ./run.sh                  # run every detected language
#   ./run.sh py ts            # restrict to a subset
#
# Exits non-zero if any runner that actually ran produced a diff. Skipped
# languages do NOT fail the harness.

set -euo pipefail

# Pin the orchestrator's real HOME so per-runner HOME rebinding can still
# locate the user's cargo / npm / pip / composer caches when needed.
REAL_HOME="${HOME:-}"

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)"
FIXTURES="${HERE}/fixtures"
EXPECTED="${HERE}/expected/envelope.json"
RUNNERS="${HERE}/runners"

# ---------------------------------------------------------------------------
# Color + log helpers (cheap; no tput dep so this runs in stripped CI shells).
# ---------------------------------------------------------------------------
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

# ---------------------------------------------------------------------------
# Temp dir + XDG layout. Single dir per run; cleaned via trap.
# ---------------------------------------------------------------------------
TMPDIR_HARNESS="$(mktemp -d -t kit-cross-lang.XXXXXX)"
trap 'rm -rf "${TMPDIR_HARNESS}"' EXIT
log "${C_DIM}temp dir: ${TMPDIR_HARNESS}${C_RESET}"

XDG_STATE_HOME="${TMPDIR_HARNESS}/state"
XDG_CONFIG_HOME="${TMPDIR_HARNESS}/config"
SINK_DIR="${TMPDIR_HARNESS}/sinks"
mkdir -p "${XDG_STATE_HOME}/kit/telemetry" "${XDG_CONFIG_HOME}/kit" "${SINK_DIR}"

# Seed install_id + consent into the per-run XDG layout. All four SDKs
# read these from XDG_STATE_HOME / XDG_CONFIG_HOME so a single layout
# serves the whole run.
cp "${FIXTURES}/install_id.bytes" "${XDG_STATE_HOME}/kit/telemetry/installation_id"
chmod 600 "${XDG_STATE_HOME}/kit/telemetry/installation_id"
cp "${FIXTURES}/consent.yaml" "${XDG_CONFIG_HOME}/kit/telemetry.yaml"

export XDG_STATE_HOME XDG_CONFIG_HOME
export KIT_TELEMETRY_MODE=full
export KIT_TELEMETRY_SINK=jsonl
# A captive HOME so the $HOME redactor rewrites our deterministic fixture
# path. The fixture uses "/test/home/project/data.json"; we materialise
# /test/home as a SUBDIR of the temp dir via symlink so that
#   (a) the redactor's home-prefix substring match still produces
#       "$HOME/project/data.json" byte-for-byte,
#   (b) tools that scribble into $HOME (cargo registries, pip caches,
#       pipx logs) have a writable target.
# We do this AFTER the precondition checks so check_py / check_php /
# check_rs can still consult their toolchains' real user-site packages
# / vendor dirs / cargo registry.
HARNESS_HOME="${TMPDIR_HARNESS}/home"
mkdir -p "${HARNESS_HOME}"

# Materialise a per-run input.json with the harness HOME baked into
# home_path, then point every runner at it via env.
HARNESS_INPUT="${TMPDIR_HARNESS}/input.json"
python3 - "${FIXTURES}/input.json" "${HARNESS_INPUT}" "${HARNESS_HOME}" <<'PY'
import json, sys
src, dst, home = sys.argv[1], sys.argv[2], sys.argv[3]
with open(src) as fh:
    payload = json.load(fh)
payload['attrs']['home_path'] = f"{home}/project/data.json"
with open(dst, 'w') as fh:
    json.dump(payload, fh, indent=2)
PY
export KIT_CROSS_LANG_INPUT="${HARNESS_INPUT}"

# Allow CLI args to subset the langs.
LANGS_REQUESTED=("$@")
SUPPORTED_LANGS=(py ts rs php)
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
# Normalisation: strip volatile fields the harness does NOT pin
# (occurred_at, sdk_lang, sdk_version + PHP-only aliases ts/sdk).
# Sorts keys for byte-equal comparison.
#
# The expected envelope deliberately omits sdk_lang / sdk_version /
# occurred_at because those vary across runs and across languages.
# ---------------------------------------------------------------------------
normalise_jsonl() {
  local in="$1" out="$2"
  # Read the FIRST JSONL line — every runner emits exactly one envelope
  # per run. Use python3 for portable JSON normalisation (jq isn't on
  # every dev box and js-yaml-shaped sorting in pure shell is a tarpit).
  python3 - "$in" "$out" <<'PY'
import json, sys
src, dst = sys.argv[1], sys.argv[2]
with open(src, 'r') as fh:
    line = fh.readline()
env = json.loads(line)

# Drop volatile / per-language-aliased fields.
for k in (
    'occurred_at',     # py/ts/rs canonical
    'ts',              # php alias for occurred_at
    'sdk_lang',        # py/ts/rs
    'sdk',             # php alias for sdk_lang
    'sdk_version',     # py/ts/rs
):
    env.pop(k, None)

# Normalise php's alternative install_id key.
if 'install_id' in env and 'installation_id' not in env:
    env['installation_id'] = env.pop('install_id')

with open(dst, 'w') as fh:
    json.dump(env, fh, sort_keys=True, indent=2)
    fh.write('\n')
PY
}

# Pre-normalise the expected envelope once so we compare apples-to-apples.
NORM_EXPECTED="${TMPDIR_HARNESS}/expected.normalised.json"
python3 - "${EXPECTED}" "${NORM_EXPECTED}" <<'PY'
import json, sys
src, dst = sys.argv[1], sys.argv[2]
with open(src, 'r') as fh:
    env = json.load(fh)
with open(dst, 'w') as fh:
    json.dump(env, fh, sort_keys=True, indent=2)
    fh.write('\n')
PY

# ---------------------------------------------------------------------------
# Per-language runners.
# ---------------------------------------------------------------------------
declare -a RAN=()
declare -a PASSED=()
declare -a FAILED=()
declare -a SKIPPED=()

run_lang() {
  local lang="$1"
  local sink="${SINK_DIR}/${lang}.jsonl"
  rm -f "${sink}"
  export KIT_TELEMETRY_SINK_FILE="${sink}"
  RAN+=("${lang}")

  hd "${lang}"
  # Per-runner HOME swap. We rebind only inside the runner invocation so
  # the orchestrator's outer shell (which may still need user-site pkgs,
  # cargo registries, vendor dirs) keeps its real HOME.
  case "${lang}" in
    py)
      HOME="${HARNESS_HOME}" "${PY_INTERP:-python3}" "${RUNNERS}/py/record.py"
      ;;
    ts)
      HOME="${HARNESS_HOME}" node "${RUNNERS}/ts/record.cjs"
      ;;
    rs)
      # cargo needs a registry cache; point CARGO_HOME at the real one
      # so the rebound HOME inside the runner doesn't trigger a re-fetch.
      local real_cargo_home="${CARGO_HOME:-${REAL_HOME:-$HOME}/.cargo}"
      ( cd "${RUNNERS}/rs" && \
        HOME="${HARNESS_HOME}" CARGO_HOME="${real_cargo_home}" cargo run --quiet )
      ;;
    php)
      HOME="${HARNESS_HOME}" php "${RUNNERS}/php/record.php"
      ;;
  esac

  if [[ ! -s "${sink}" ]]; then
    fail "${lang}: runner produced no output at ${sink}"
    FAILED+=("${lang}")
    return 1
  fi

  local norm="${TMPDIR_HARNESS}/${lang}.normalised.json"
  normalise_jsonl "${sink}" "${norm}"

  if diff -u "${NORM_EXPECTED}" "${norm}"; then
    ok "${lang}: envelope matches expected (post-normalisation)"
    PASSED+=("${lang}")
    return 0
  else
    fail "${lang}: envelope diff — see above"
    FAILED+=("${lang}")
    return 1
  fi
}

# ---------------------------------------------------------------------------
# Skip-on-missing detection. Each lang has independent prerequisites; we
# verify them up-front so the per-runner failure message is about CONTENT
# (parity bug), not absence of toolchain.
# ---------------------------------------------------------------------------
PY_INTERP=""
check_py() {
  # Prefer the SDK's own .venv (most reliable: matches pyproject's
  # Python>=3.11 constraint and ships pyyaml + httpx).
  local venv="${HERE}/../../py/.venv/bin/python"
  if [[ -x "${venv}" ]]; then
    if "${venv}" -c 'import yaml' >/dev/null 2>&1; then
      PY_INTERP="${venv}"
      return 0
    fi
  fi
  # Fall back to system python3 if it has yaml AND meets the SDK's
  # >=3.11 floor.
  if command -v python3 >/dev/null 2>&1; then
    if python3 -c 'import sys; assert sys.version_info >= (3, 11); import yaml' >/dev/null 2>&1; then
      PY_INTERP="$(command -v python3)"
      return 0
    fi
  fi
  echo "no python3>=3.11 with pyyaml; run \`uv sync\` in hops/main/sdk/py/"
  return 1
}
check_ts() {
  if ! command -v node >/dev/null 2>&1; then
    echo "missing node"; return 1
  fi
  local dist="${HERE}/../../ts/dist/telemetry/index.js"
  if [[ ! -f "${dist}" ]]; then
    echo "missing ts dist bundle (run \`npm run build\` in hops/main/sdk/ts/)"; return 1
  fi
  return 0
}
check_rs() {
  if ! command -v cargo >/dev/null 2>&1; then
    echo "missing cargo"; return 1
  fi
  return 0
}
check_php() {
  if ! command -v php >/dev/null 2>&1; then
    echo "missing php"; return 1
  fi
  local autoload="${HERE}/../../experimental/php/vendor/autoload.php"
  if [[ ! -f "${autoload}" ]]; then
    echo "missing php vendor/autoload.php (run \`composer install\` in hops/main/sdk/experimental/php/)"; return 1
  fi
  return 0
}

for lang in "${SUPPORTED_LANGS[@]}"; do
  want "${lang}" || continue
  # Invoke check_<lang> in the CURRENT shell so it can export per-lang
  # state (e.g. PY_INTERP for the python runner). We capture stdout via
  # a tmp-file rather than $(...) to avoid the subshell that would
  # otherwise drop those variables.
  _check_msg="$(mktemp -t kit-cross-lang-check.XXXXXX)"
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

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
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

# Harness fails iff at least one runner ran AND any runner failed.
if [[ ${#FAILED[@]} -gt 0 ]]; then
  exit 1
fi
exit 0
