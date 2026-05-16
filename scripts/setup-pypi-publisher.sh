#!/usr/bin/env bash
set -euo pipefail

# setup-pypi-publisher.sh — Create a PyPI pending trusted publisher
# using ibr with browser session cookies.
#
# Usage:
#   ./scripts/setup-pypi-publisher.sh <project> <owner> <repo> [options]
#   ./scripts/setup-pypi-publisher.sh hop-top-kit hop-top kit
#   ./scripts/setup-pypi-publisher.sh hop-top-kit hop-top kit --workflow ci.yml
#   ./scripts/setup-pypi-publisher.sh hop-top-kit hop-top kit --dry-run
#
# Options:
#   --workflow <name>   GitHub workflow filename [default: release-please.yml]
#   --env <name>        GitHub environment name [default: pypi]
#   --browser <name>    Browser for cookie import [default: chrome]
#   --dry-run           Print ibr prompt without executing
#
# Environment:
#   BROWSER_PROFILE     Browser profile for cookies [default: Profile 1]
#
# Prerequisites: log in to pypi.org in the specified browser.
# Requires: ibr

die() { echo "error: $*" >&2; exit 1; }
usage() {
  sed -n '/^# Usage:/,/^# Requires:/p' "$0" | sed 's/^# \?//'
  exit 1
}

command -v ibr >/dev/null 2>&1 || die "ibr is required"

# Positional args
[[ $# -lt 3 ]] && usage
PYPI_PROJECT="$1"; shift
GH_OWNER="$1"; shift
GH_REPO="$1"; shift

# Defaults
GH_WORKFLOW="release-please.yml"
GH_ENV="pypi"
BROWSER="chrome"
DRY_RUN=false

# Options
while [[ $# -gt 0 ]]; do
  case "$1" in
    --workflow)  GH_WORKFLOW="$2"; shift 2 ;;
    --env)       GH_ENV="$2"; shift 2 ;;
    --browser)   BROWSER="$2"; shift 2 ;;
    --dry-run)   DRY_RUN=true; shift ;;
    *)           die "unknown option: $1" ;;
  esac
done

PROMPT="$(cat <<EOF
url: https://pypi.org/manage/account/publishing/
instructions:
  - scroll down to the "Create a new pending publisher" section
  - fill the "PyPI Project Name" field with: ${PYPI_PROJECT}
  - fill the "Owner" field with: ${GH_OWNER}
  - fill the "Repository name" field with: ${GH_REPO}
  - fill the "Workflow name" field with: ${GH_WORKFLOW}
  - fill the "Environment name" field with: ${GH_ENV}
  - click the submit button to create the pending publisher
  - extract any success or error messages shown on the page
EOF
)"

if $DRY_RUN; then
  echo "[dry-run] Would run ibr with prompt:"
  echo "$PROMPT"
  exit 0
fi

echo "Creating PyPI pending trusted publisher for ${PYPI_PROJECT}..."
echo "  Owner: ${GH_OWNER}  Repo: ${GH_REPO}  Workflow: ${GH_WORKFLOW}  Env: ${GH_ENV}"
BROWSER_HEADLESS=false BROWSER_PROFILE="${BROWSER_PROFILE:-Profile 1}" \
  ibr --cookies "${BROWSER}:pypi.org" --annotate "$PROMPT"
