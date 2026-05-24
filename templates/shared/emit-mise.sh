#!/usr/bin/env bash
# emit-mise.sh — Emit a kit-managed mise.toml into a project
#
# Reads `templates/shared/tool-versions.toml` (the central
# manifest) and writes a `mise.toml` to the given project
# directory, scoped to the project's selected languages.
#
# Public function:
#   emit_mise <project-dir> <lang-csv>
#     <project-dir>  Path to the project root that should
#                    receive (or refresh) a mise.toml.
#     <lang-csv>     Comma-separated lang list. Subset of
#                    "go,ts,py,rs". Order is ignored.
#
# Spec §3 shows per-line exemplar comments (e.g. `# if lang
# includes go`); the emitter does not reproduce those — tests
# assert on content, not on illustrative comments.
#
# What gets emitted (always inside kit-managed markers):
#
#   [tools]
#     - Runtimes from `[runtimes]` table, gated by <lang-csv>:
#         go     -> go
#         ts     -> node, pnpm
#         py     -> python, uv
#         rs     -> rust
#     - Workflow tools from `[workflow]` table:
#         golangci-lint -> gated by `go`
#         ruff          -> gated by `py`
#         lychee, hadolint, actionlint, shellcheck, shfmt,
#         "npm:release-please" -> always emitted
#
#   [env]
#     _.file = ".env"   (always)
#
#   [tasks.install]
#     description plus a `run = [ ... ]` array with one
#     command per selected lang:
#       go -> "go mod download"
#       ts -> "pnpm install"
#       py -> "uv sync"
#       rs -> "cargo fetch"
#
# Idempotent: re-running `emit_mise` with the same inputs
# produces a byte-identical file (delegates to mb_write).
#
# Parsing of tool-versions.toml uses a tiny awk state machine
# rather than a full TOML parser. The manifest shape is
# constrained (two flat tables, scalar string values) and
# committed to this repo, so awk is sufficient and avoids a
# runtime dependency.
#
# shellcheck disable=SC2034

# Guard against double-sourcing
[[ -n "${_KIT_EMIT_MISE_LOADED:-}" ]] && return 0
_KIT_EMIT_MISE_LOADED=1

# Resolve script dir once at source time so we can find the
# sibling manifest + managed-block library regardless of CWD.
_KIT_EMIT_MISE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source managed-block.sh on demand. The library is guarded
# against double-loading internally.
# shellcheck source=managed-block.sh
source "$_KIT_EMIT_MISE_DIR/managed-block.sh"

# ----------------------------------------------------------
# Internal: parse a TOML table from the manifest
# ----------------------------------------------------------
#
# Echoes "key=value" lines (one per pair) for the requested
# table. Values are emitted unquoted (we'll re-quote on
# render). Keys may be bare or double-quoted in the manifest
# (the `"npm:release-please"` key uses quoting).
#
# Arguments:
#   $1  path to tool-versions.toml
#   $2  table name (without brackets), e.g. "runtimes"

_mise_parse_table() {
  local manifest="$1" table="$2"
  # Portable awk (BSD + GNU): no `match(line, re, arr)` array
  # capture; we substring-extract by `=` split and trim/strip
  # quotes manually.
  awk -v want="$table" '
    function trim(s) {
      sub(/^[[:space:]]+/, "", s)
      sub(/[[:space:]]+$/, "", s)
      return s
    }
    # Detect section headers like [runtimes] or [workflow]
    /^\[[A-Za-z_][A-Za-z0-9_.-]*\]/ {
      sec = $0
      sub(/^\[/, "", sec)
      sub(/\].*$/, "", sec)
      in_section = (sec == want)
      next
    }
    !in_section { next }
    /^[[:space:]]*$/ { next }
    /^[[:space:]]*#/ { next }
    {
      line = $0
      # Strip trailing inline comment (anything from " #" on)
      sub(/[[:space:]]+#.*$/, "", line)
      # Need an "=" to be a key/value line.
      eq = index(line, "=")
      if (eq == 0) next
      k = trim(substr(line, 1, eq - 1))
      v = trim(substr(line, eq + 1))
      # Strip surrounding quotes from key (e.g. "npm:release-please")
      if (k ~ /^".*"$/) {
        k = substr(k, 2, length(k) - 2)
      }
      # Value must be a "..."-quoted scalar in this manifest.
      if (v !~ /^".*"$/) next
      v = substr(v, 2, length(v) - 2)
      print k "=" v
    }
  ' "$manifest"
}

# ----------------------------------------------------------
# Internal: should a tool be emitted given the lang set?
# ----------------------------------------------------------
#
# Returns 0 if the tool name should appear in the project's
# mise.toml given the comma-padded lang string (",go,ts,").
# Cross-cutting tools always return 0.

_mise_lang_gate() {
  local tool="$1" langs="$2"
  case "$tool" in
    # Runtimes
    go)     [[ "$langs" == *",go,"* ]] ;;
    node|pnpm)
            [[ "$langs" == *",ts,"* ]] ;;
    python|uv)
            [[ "$langs" == *",py,"* ]] ;;
    rust)   [[ "$langs" == *",rs,"* ]] ;;
    # Workflow tools that follow a lang
    golangci-lint)
            [[ "$langs" == *",go,"* ]] ;;
    ruff)   [[ "$langs" == *",py,"* ]] ;;
    # Everything else from the workflow table is always emitted
    *)      return 0 ;;
  esac
}

# ----------------------------------------------------------
# emit_mise <project-dir> <lang-csv>
# ----------------------------------------------------------

emit_mise() {
  local project_dir="$1" lang_csv="$2"

  if [[ -z "$project_dir" ]]; then
    echo "emit_mise: missing <project-dir>" >&2
    return 2
  fi
  if [[ -z "$lang_csv" ]]; then
    echo "emit_mise: missing <lang-csv>" >&2
    return 2
  fi
  if [[ ! -d "$project_dir" ]]; then
    echo "emit_mise: not a directory: $project_dir" >&2
    return 1
  fi

  local manifest="$_KIT_EMIT_MISE_DIR/tool-versions.toml"
  if [[ ! -f "$manifest" ]]; then
    echo "emit_mise: manifest not found: $manifest" >&2
    return 1
  fi

  # Normalize lang list: lower, strip spaces, surround in
  # commas so substring tests like *",go,"* work.
  local langs
  langs="$(printf '%s' "$lang_csv" \
    | tr '[:upper:]' '[:lower:]' \
    | tr -d ' ')"
  langs=",${langs},"

  local out="$project_dir/mise.toml"

  # If the file does not already exist, lay down the kit-
  # managed boilerplate header (the prose comment block
  # above the markers per spec §3). On subsequent runs we
  # leave whatever is above the markers untouched so users
  # can add their own tools there.
  if [[ ! -f "$out" ]]; then
    cat > "$out" <<'EOF'
# Managed by `kit init` — tools below the kit-managed marker are tracked
# against the kit tool-versions manifest. Add your own tools above the
# marker. Run `kit init --update` to refresh managed tools.

EOF
  fi

  # ------------------------------------------------------
  # Compose the managed payload in a temp file, then hand
  # it to mb_write. Building in a string would work too but
  # awk pipelines stream nicely into a file.
  # ------------------------------------------------------

  local payload
  payload="$(mktemp "${TMPDIR:-/tmp}/kit-mise.XXXXXX")" || return 1

  {
    printf '[tools]\n'

    # Runtimes first, in manifest order, gated by lang.
    while IFS='=' read -r k v; do
      [[ -z "$k" ]] && continue
      _mise_lang_gate "$k" "$langs" || continue
      _mise_emit_tool_line "$k" "$v"
    done < <(_mise_parse_table "$manifest" runtimes)

    # Then workflow tools (cross-cutting + lang-gated).
    while IFS='=' read -r k v; do
      [[ -z "$k" ]] && continue
      _mise_lang_gate "$k" "$langs" || continue
      _mise_emit_tool_line "$k" "$v"
    done < <(_mise_parse_table "$manifest" workflow)

    printf '\n[env]\n'
    printf '_.file = ".env"\n'

    printf '\n[tasks.install]\n'
    printf 'description = "Install ecosystem dependencies (orchestrated by mise)"\n'
    printf 'run = [\n'
    # Lang-ordered install commands. Order: go, ts, py, rs
    # to match scaffold's canonical ordering. We build the
    # list inline (no nested function) to keep the function
    # call graph flat — nested functions interact poorly with
    # `set -T` (functrace) used by some test harnesses.
    local first=1
    local pair
    for pair in "go|go mod download" \
                "ts|pnpm install" \
                "py|uv sync" \
                "rs|cargo fetch"; do
      local lang_flag="${pair%%|*}"
      local cmd="${pair#*|}"
      [[ "$langs" == *",${lang_flag},"* ]] || continue
      if [[ "$first" -eq 1 ]]; then
        first=0
      else
        printf ',\n'
      fi
      printf '  "%s"' "$cmd"
    done
    # Final newline before the closing bracket.
    [[ "$first" -eq 0 ]] && printf '\n'
    printf ']\n'
  } > "$payload"

  mb_write "$out" < "$payload"
  local rc=$?
  rm -f "$payload"
  return $rc
}

# ----------------------------------------------------------
# Internal: render one `[tools]` line.
# ----------------------------------------------------------
#
# Quote keys that contain ":" (e.g. "npm:release-please").
# Bare keys go through unquoted to keep the file diff-clean
# against the spec exemplar.

_mise_emit_tool_line() {
  local k="$1" v="$2"
  if [[ "$k" == *:* ]]; then
    printf '"%s" = "%s"\n' "$k" "$v"
  else
    printf '%s = "%s"\n' "$k" "$v"
  fi
}
