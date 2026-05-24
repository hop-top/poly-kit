#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2329
set -euo pipefail

# -------------------------------------------------------
# conform.sh — Bring an existing repo to kit standards
#
# Usage: conform.sh [flags]
#
# Idempotent. Safe actions are applied automatically.
# Edge cases are flagged in a report with LLM-ready
# prompts.
# -------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

# shellcheck source=setup-release-please.sh
source "$SCRIPT_DIR/setup-release-please.sh"

# shellcheck source=conform-actions.sh
source "$SCRIPT_DIR/conform-actions.sh"

# --- Defaults ------------------------------------------

PROJECT_PATH="."
DRY_RUN=false
REPORT_FILE="conform-report.md"
TRACK_ID="kit-conform"
NO_TLC=false
NO_MANAGED_REFRESH=false

# --- Usage ---------------------------------------------

usage() {
  cat <<USAGE
Usage: conform.sh [flags]

Bring an existing repo up to kit standards.

Idempotent. Safe actions are applied automatically.
Edge cases are flagged in a report with LLM-ready prompts.

Managed-block refresh (mise.toml, .devcontainer/*,
.env.example kit-adapter blocks) is delegated to
\`kit init --update\`. Additive-merge checks (license
headers, missing-file copies, Makefile and .gitignore
extensions) run after the managed refresh.

Flags:
  --path DIR             Project directory (default: .)
  --dry-run              Report only, no file changes;
                         runs \`kit init --check\` to
                         preview managed-block drift
  --report FILE          Output report path
                         (default: conform-report.md)
  --track-id ID          tlc track ID (default: kit-conform)
  --no-tlc               Skip tlc track creation
  --no-managed-refresh   Skip the \`kit init --update\`
                         step (only run additive-merge
                         checks)
  -h, --help             Show this help
USAGE
  exit 0
}

# --- Arg parsing ---------------------------------------

while [ $# -gt 0 ]; do
  case "$1" in
    --path)
      require_arg "$1" "${2:-}"
      PROJECT_PATH="$2"; shift 2 ;;
    --dry-run)
      DRY_RUN=true; shift ;;
    --report)
      require_arg "$1" "${2:-}"
      REPORT_FILE="$2"; shift 2 ;;
    --track-id)
      require_arg "$1" "${2:-}"
      TRACK_ID="$2"; shift 2 ;;
    --no-tlc)
      NO_TLC=true; shift ;;
    --no-managed-refresh)
      NO_MANAGED_REFRESH=true; shift ;;
    -h|--help)
      usage ;;
    *)
      echo "Error: unknown flag '$1'" >&2
      echo "Run 'conform.sh --help' for usage." >&2
      exit 1 ;;
  esac
done

# --- Navigate to project -------------------------------

cd "$PROJECT_PATH" || {
  echo "Error: cannot cd to '$PROJECT_PATH'" >&2
  exit 1
}

# --- Detection -----------------------------------------
#
# `detect_tools` may return non-zero when none of its
# `command -v` probes succeed (e.g. minimal CI image).
# That's informational, not fatal — disarm `set -e` for
# the call.

detect_tools || true
detect_languages
detect_project_meta

echo "Project: $APP_NAME"
echo "Languages: ${DETECTED_LANGS[*]:-none}"

# --- Tracking arrays -----------------------------------

APPLIED=()
SKIPPED=()
REVIEW=()
MANAGED_TOUCHED=()
MANAGED_STATUS="skipped"
MANAGED_NOTE=""
TASKS_YAML=""

# --- Kit binary detection ------------------------------
#
# Detection ladder for `kit init --update|--check`:
#   1. `command -v kit`        — installed binary on PATH
#   2. $KIT_BIN env var        — explicit override
#   3. in-repo `go build`      — poly-kit dev workflow
#   4. skip with warning       — downstream project without kit
#
# Resolved value (empty when skipped) is exported as KIT_BIN
# so subshells can re-use it.

find_kit_binary() {
  # 1. installed kit on PATH
  if command -v kit >/dev/null 2>&1; then
    KIT_BIN="$(command -v kit)"
    return 0
  fi
  # 2. caller-provided override
  if [ -n "${KIT_BIN:-}" ] && [ -x "$KIT_BIN" ]; then
    return 0
  fi
  # 3. in-repo build (we're running against poly-kit itself)
  if [ -f "$SCRIPT_DIR/scaffold.sh" ] \
     && [ -d "$SCRIPT_DIR/../cmd/kit" ] \
     && command -v go >/dev/null 2>&1; then
    local tmp
    tmp="$(mktemp -d)"
    if (cd "$SCRIPT_DIR/.." && \
        go build -buildvcs=false -o "$tmp/kit" ./cmd/kit) \
       >/dev/null 2>&1; then
      KIT_BIN="$tmp/kit"
      return 0
    fi
  fi
  # 4. no kit available
  KIT_BIN=""
  return 1
}

# --- Managed-block refresh -----------------------------
#
# Delegates mise.toml / .devcontainer/* / .env.example
# refresh to `kit init --update` (or `--check` under
# --dry-run). Parses stdout's "  - <path>" lines so the
# report can list which managed files were touched.

run_managed_refresh() {
  if [ "$NO_MANAGED_REFRESH" = true ]; then
    MANAGED_STATUS="skipped"
    MANAGED_NOTE="--no-managed-refresh"
    echo "Managed refresh: skipped (--no-managed-refresh)"
    return 0
  fi

  if ! find_kit_binary; then
    MANAGED_STATUS="skipped"
    MANAGED_NOTE="kit binary not found"
    echo "Warning: kit binary not found on PATH and no" >&2
    echo "  in-repo go source detected; skipping managed" >&2
    echo "  refresh. Set \$KIT_BIN or install kit." >&2
    return 0
  fi

  local verb="--update"
  [ "$DRY_RUN" = true ] && verb="--check"
  echo "Managed refresh: $KIT_BIN init $verb"

  local kit_out
  if kit_out="$("$KIT_BIN" init "$verb" --quiet 2>&1)"; then
    MANAGED_STATUS="ok"
  else
    local rc=$?
    # --check exits non-zero on drift — that's informational
    # in --dry-run mode, not a hard failure.
    if [ "$DRY_RUN" = true ]; then
      MANAGED_STATUS="drift"
    else
      MANAGED_STATUS="error"
      MANAGED_NOTE="kit init $verb exit=$rc"
      echo "Warning: kit init $verb failed (exit $rc)" >&2
      printf '  %s\n' "${kit_out//$'\n'/$'\n'  }" >&2
      return 0
    fi
  fi

  # Indent kit's output so it nests visually under the
  # conform.sh log.
  printf '  %s\n' "${kit_out//$'\n'/$'\n'  }"

  # Parse "  - <path>" lines emitted by managed.go to
  # collect the touched managed files.
  local line path
  while IFS= read -r line; do
    case "$line" in
      "  - "*)
        path="${line#  - }"
        MANAGED_TOUCHED+=("$path")
        ;;
    esac
  done <<<"$kit_out"
}

run_managed_refresh

# --- Tracking helpers ----------------------------------

# Add an applied item to tracking + YAML
log_applied() {
  local title="$1" desc="$2"
  APPLIED+=("$title")
  TASKS_YAML+="  - title: \"$title\"
    description: |
      $desc
    effort: XS
    priority: P2
    tags: [status:applied]
"
}

# Add a skipped item
log_skipped() {
  local title="$1" desc="$2"
  SKIPPED+=("$title")
  TASKS_YAML+="  - title: \"$title\"
    description: |
      $desc
    effort: XS
    priority: P3
    tags: [status:skipped]
"
}

# Add a review item with LLM prompt
log_review() {
  local title="$1" desc="$2"
  local effort="${3:-M}" priority="${4:-P1}"
  REVIEW+=("$title")
  TASKS_YAML+="  - title: \"$title\"
    description: |
      $desc
    effort: $effort
    priority: $priority
    tags: [status:review]
"
}

# --- Checklist steps -----------------------------------

run_copy_actions
run_generated_actions
run_merge_actions
run_review_checks

# --- Report --------------------------------------------

write_report() {
  local out="$1"
  cat > "$out" <<EOF
---
title: "kit conformance: $APP_NAME"
tracks:
  - $TRACK_ID
tasks:
$TASKS_YAML---

# Kit Conformance Report: $APP_NAME

## Summary

| Category        | Count |
|-----------------|-------|
| Applied         | ${#APPLIED[@]} |
| Skipped         | ${#SKIPPED[@]} |
| Review          | ${#REVIEW[@]} |
| Managed (kit)   | ${#MANAGED_TOUCHED[@]} |

## Managed Blocks (kit init)

Status: $MANAGED_STATUS$([ -n "$MANAGED_NOTE" ] && echo " — $MANAGED_NOTE")

$(if [ ${#MANAGED_TOUCHED[@]} -gt 0 ]; then
  for item in "${MANAGED_TOUCHED[@]}"; do echo "- $item"; done
else
  echo "_none_"
fi)

## Applied

$(if [ ${#APPLIED[@]} -gt 0 ]; then
  for item in "${APPLIED[@]}"; do echo "- $item"; done
else
  echo "_none_"
fi)

## Skipped (already conformant)

$(if [ ${#SKIPPED[@]} -gt 0 ]; then
  for item in "${SKIPPED[@]}"; do echo "- $item"; done
else
  echo "_none_"
fi)

## Needs Review

$(if [ ${#REVIEW[@]} -gt 0 ]; then
  for item in "${REVIEW[@]}"; do echo "- $item"; done
else
  echo "_none_"
fi)
EOF
  echo "Report written to $out"
}

# --- tlc integration -----------------------------------

integrate_tlc() {
  if [ "$HAS_TLC" != true ]; then
    echo "tlc not found; skipping track creation."
    return 0
  fi
  if [ "$NO_TLC" = true ]; then
    echo "tlc integration disabled (--no-tlc)."
    return 0
  fi
  if [ "$DRY_RUN" = true ]; then
    echo "Dry run; skipping tlc integration."
    return 0
  fi

  echo "Creating tlc track '$TRACK_ID'..."
  tlc track create \
    "kit conformance: $APP_NAME" \
    --type refactor \
    --id "$TRACK_ID" 2>/dev/null || true

  local track_dir=".tlc/tracks/$TRACK_ID"
  mkdir -p "$track_dir"
  cp "$REPORT_FILE" "$track_dir/plan.md"

  tlc track update "$TRACK_ID" \
    --add-plan "$track_dir/plan.md" 2>/dev/null || {
    echo "Warning: failed to ingest plan into track." >&2
  }

  echo "Track '$TRACK_ID' created and plan ingested."
}

# --- Execute -------------------------------------------

if [ "$DRY_RUN" = true ]; then
  echo ""
  echo "[dry-run] No files modified."
fi

write_report "$REPORT_FILE"
integrate_tlc

# --- Summary -------------------------------------------

echo ""
echo "=== Conformance Summary ==="
echo "  Applied:       ${#APPLIED[@]}"
echo "  Skipped:       ${#SKIPPED[@]}"
echo "  Review:        ${#REVIEW[@]}"
echo "  Managed (kit): ${#MANAGED_TOUCHED[@]} ($MANAGED_STATUS)"

if [ ${#REVIEW[@]} -gt 0 ]; then
  echo ""
  echo "Review items remain; see $REPORT_FILE"
  exit 2
fi

# Under --dry-run, propagate `kit init --check` drift as
# a non-zero exit so CI gates / pre-merge hooks notice
# un-refreshed managed blocks without having to grep the
# report. exit 3 is distinct from exit 2 (review items)
# so callers can tell the failure modes apart.
if [ "$DRY_RUN" = true ] && [ "$MANAGED_STATUS" = "drift" ]; then
  echo ""
  echo "Managed-block drift detected; see $REPORT_FILE"
  exit 3
fi

exit 0
