#!/usr/bin/env bash
# branch-protection.sh — apply the default-main ruleset to a repo.
# Sourced by branch-protection-bulk.sh; also usable standalone.
#
# apply_branch_protection <owner/name>
#   stdout: "<STATUS>: <owner/name>[ (diagnostic)]"
#   STATUS: SET (created) | UPDATED (existing replaced) | FAILED
#   exit:   0 on SET/UPDATED, 1 on FAILED
#
# Body seam: the ruleset JSON comes from the function named by
# $BP_BODY_FN (default bp_build_body), called as `fn <owner/name>`,
# JSON on stdout.

BP_RULESET_NAME="hop-top-default-main"

# Default body builder: not provided yet.
bp_build_body() {
  echo "ruleset body builder not implemented" >&2
  return 1
}

# _bp_fail <slug> <diagnostic> — report FAILED, return 1
_bp_fail() {
  printf 'FAILED: %s (%s)\n' "$1" "$2"
  return 1
}

apply_branch_protection() {
  local repo_slug="${1:-}"
  [[ -n "$repo_slug" ]] || { _bp_fail "" "missing repo slug"; return 1; }

  # 0. admin pre-flight
  local admin
  if ! admin="$(gh api "repos/$repo_slug" --jq .permissions.admin 2>/dev/null)"; then
    _bp_fail "$repo_slug" "cannot read repo (admin check failed)"; return 1
  fi
  if [[ "$admin" != "true" ]]; then
    _bp_fail "$repo_slug" "insufficient permissions: admin required"; return 1
  fi

  # 1. existing ruleset id ("" if none)
  local ruleset_id
  if ! ruleset_id="$(gh api "repos/$repo_slug/rulesets" \
      --jq ".[] | select(.name == \"$BP_RULESET_NAME\") | .id" 2>/dev/null)"; then
    _bp_fail "$repo_slug" "ruleset lookup failed"; return 1
  fi

  # 2. ruleset body (injected seam)
  local body
  if ! body="$("${BP_BODY_FN:-bp_build_body}" "$repo_slug" 2>/dev/null)" || [[ -z "$body" ]]; then
    _bp_fail "$repo_slug" "ruleset body construction failed"; return 1
  fi

  # 3. POST (new) or PUT (existing); 4. report
  local method=POST path="repos/$repo_slug/rulesets" status=SET err
  if [[ -n "$ruleset_id" ]]; then
    method=PUT path="$path/$ruleset_id" status=UPDATED
  fi
  if ! err="$(printf '%s' "$body" | gh api -X "$method" "$path" --input - 2>&1 >/dev/null)"; then
    _bp_fail "$repo_slug" "gh api $method failed: ${err//$'\n'/ }"; return 1
  fi
  printf '%s: %s\n' "$status" "$repo_slug"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  set -euo pipefail
  apply_branch_protection "$@"
fi
