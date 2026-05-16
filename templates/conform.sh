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

# --- Usage ---------------------------------------------

usage() {
  cat <<USAGE
Usage: conform.sh [flags]

Bring an existing repo up to kit standards.

Idempotent. Safe actions are applied automatically.
Edge cases are flagged in a report with LLM-ready prompts.

Flags:
  --path DIR          Project directory (default: .)
  --dry-run           Report only, no file changes
  --report FILE       Output report path
                      (default: conform-report.md)
  --track-id ID       tlc track ID (default: kit-conform)
  --no-tlc            Skip tlc track creation
  -h, --help          Show this help
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

detect_tools
detect_languages
detect_project_meta

echo "Project: $APP_NAME"
echo "Languages: ${DETECTED_LANGS[*]:-none}"

# --- Tracking arrays -----------------------------------

APPLIED=()
SKIPPED=()
REVIEW=()
TASKS_YAML=""

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

| Category | Count |
|----------|-------|
| Applied  | ${#APPLIED[@]} |
| Skipped  | ${#SKIPPED[@]} |
| Review   | ${#REVIEW[@]} |

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
echo "  Applied: ${#APPLIED[@]}"
echo "  Skipped: ${#SKIPPED[@]}"
echo "  Review:  ${#REVIEW[@]}"

if [ ${#REVIEW[@]} -gt 0 ]; then
  echo ""
  echo "Review items remain; see $REPORT_FILE"
  exit 2
fi

exit 0
