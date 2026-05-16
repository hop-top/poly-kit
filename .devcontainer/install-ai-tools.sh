#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AI_DIR="$SCRIPT_DIR/ai"

declare -A TOOLS=(
  [1]="claude:Claude Code:$AI_DIR/claude.sh"
  [2]="copilot:GitHub Copilot CLI:$AI_DIR/copilot.sh"
  [3]="codex:Codex CLI:$AI_DIR/codex.sh"
  [4]="gemini:Gemini CLI:$AI_DIR/gemini.sh"
  [5]="continue:Continue.dev:$AI_DIR/continue.sh"
  [6]="crush:Crush:$AI_DIR/crush.sh"
  [7]="opencode:OpenCode:$AI_DIR/opencode.sh"
  [8]="vibe:Vibe:$AI_DIR/vibe.sh"
)

run_tool() {
  local entry="${TOOLS[$1]}"
  local script="${entry##*:}"
  local label="${entry#*:}"
  label="${label%:*}"
  echo ""
  echo "── Installing $label ──"
  bash "$script"
}

# --auto mode: read .ai-tools file
if [[ "${1:-}" == "--auto" ]]; then
  TOOLS_FILE="${2:-.ai-tools}"
  if [[ ! -f "$TOOLS_FILE" ]]; then
    echo "Error: $TOOLS_FILE not found."
    exit 1
  fi
  while IFS= read -r name || [[ -n "$name" ]]; do
    name="$(echo "$name" | xargs)"
    [[ -z "$name" || "$name" == \#* ]] && continue
    matched=false
    for key in "${!TOOLS[@]}"; do
      entry="${TOOLS[$key]}"
      slug="${entry%%:*}"
      if [[ "$slug" == "$name" ]]; then
        matched=true
        run_tool "$key"
        break
      fi
    done
    if [[ "$matched" == false ]]; then
      echo "Warning: Unknown tool in $TOOLS_FILE: $name (skipping)"
    fi
  done < "$TOOLS_FILE"
  echo ""
  echo "Done (auto mode)."
  exit 0
fi

# Interactive mode
echo "Select AI tools to install:"
echo ""
for i in $(seq 1 8); do
  entry="${TOOLS[$i]}"
  label="${entry#*:}"
  label="${label%:*}"
  slug="${entry%%:*}"
  echo "  [$i] $label ($slug)"
done
echo ""
read -rp "Enter numbers (space-separated), or 'a' for all: " input

if [[ "$input" == "a" ]]; then
  selections=$(seq 1 8)
else
  selections=$input
fi

for num in $selections; do
  num="$(echo "$num" | xargs)"
  if [[ -n "${TOOLS[$num]:-}" ]]; then
    run_tool "$num"
  else
    echo "Unknown selection: $num (skipping)"
  fi
done

echo ""
echo "Done."
