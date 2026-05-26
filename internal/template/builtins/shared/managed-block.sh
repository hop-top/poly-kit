#!/usr/bin/env bash
# managed-block.sh — Idempotent marker-delimited block editor
#
# Library for reading, writing, and removing content between
# `kit-managed` marker comments inside text files. Used by
# scaffold.sh emitters (mise.toml, devcontainer.json,
# docker-compose.yml, .env.example, otel-config.yaml) and the
# future `kit init` updater.
#
# Marker syntax (comment char varies per file format):
#
#   # >>> kit-managed >>>          # unlabeled (TOML, YAML, .env, sh)
#   # <<< kit-managed <<<
#
#   # >>> kit-managed: telemetry >>>   # labeled
#   # <<< kit-managed: telemetry <<<
#
#   // >>> kit-managed >>>         # JSON-C / devcontainer.json
#   // <<< kit-managed <<<
#
# Public functions:
#   mb_comment_char  <file>                  → echoes '#' or '//'
#   mb_read          <file> [<label>]        → echoes block content
#   mb_write         <file> [<label>]        ← reads stdin
#   mb_remove        <file> [<label>]
#   mb_has           <file> [<label>]        → exit 0/1
#
# Portability: pure bash + POSIX awk/grep/mktemp/mv/cmp. Works
# on macOS (BSD) and Linux (GNU). No `sed -i` (incompatible
# between BSDs and GNU); we always write via a temp file and
# atomic `mv`. Idempotent: writing the same content twice
# produces byte-identical files.
#
# shellcheck disable=SC2034

# Guard against double-sourcing
[[ -n "${_KIT_MANAGED_BLOCK_LOADED:-}" ]] && return 0
_KIT_MANAGED_BLOCK_LOADED=1

# ----------------------------------------------------------
# Comment-character detection
# ----------------------------------------------------------

# Echo the comment marker prefix for a given file path.
# Mapping:
#   *.json | *.jsonc | devcontainer.json → //
#   everything else                      → #
mb_comment_char() {
  local file="$1"
  local base
  base="$(basename "$file")"
  case "$base" in
    devcontainer.json) printf '//' ;;
    *.jsonc)           printf '//' ;;
    *.json)            printf '//' ;;
    *)                 printf '#'  ;;
  esac
}

# ----------------------------------------------------------
# Internal: build the open/close marker lines for a file+label
# ----------------------------------------------------------

# Sets _MB_OPEN and _MB_CLOSE shell vars (literal marker
# lines, no trailing newline) based on file and optional
# label. Label may be empty for the unlabeled block.
_mb_markers() {
  local file="$1" label="${2:-}"
  local cc
  cc="$(mb_comment_char "$file")"
  if [[ -n "$label" ]]; then
    _MB_OPEN="${cc} >>> kit-managed: ${label} >>>"
    _MB_CLOSE="${cc} <<< kit-managed: ${label} <<<"
  else
    _MB_OPEN="${cc} >>> kit-managed >>>"
    _MB_CLOSE="${cc} <<< kit-managed <<<"
  fi
}

# ----------------------------------------------------------
# mb_has — exit 0 if the block exists in <file>
# ----------------------------------------------------------

mb_has() {
  local file="$1" label="${2:-}"
  [[ -f "$file" ]] || return 1
  _mb_markers "$file" "$label"
  # Use awk with literal string compare (avoids regex escaping
  # of `<`, `>`, `/`, `:`). Exit 0 if both markers present and
  # open appears before close.
  awk -v openm="$_MB_OPEN" -v closem="$_MB_CLOSE" '
    index($0, openm) && !o { o = NR }
    o && index($0, closem) && NR > o { c = NR; exit }
    END { exit !(o && c) }
  ' "$file"
}

# ----------------------------------------------------------
# mb_read — echo the current content between markers
# ----------------------------------------------------------
#
# Excludes marker lines themselves. Exits non-zero if no
# block found. Multi-block files: returns the first matching
# block.

mb_read() {
  local file="$1" label="${2:-}"
  if [[ ! -f "$file" ]]; then
    echo "mb_read: file not found: $file" >&2
    return 1
  fi
  _mb_markers "$file" "$label"
  awk -v openm="$_MB_OPEN" -v closem="$_MB_CLOSE" '
    !inside && index($0, openm) { inside = 1; found = 1; next }
    inside && index($0, closem) { inside = 0; exit }
    inside { print }
    END { exit !found }
  ' "$file"
}

# ----------------------------------------------------------
# mb_write — replace or append the block. Reads stdin.
# ----------------------------------------------------------
#
# - If the marker pair exists, replaces content between them
#   (preserving everything else byte-for-byte).
# - If markers are absent, appends a new block at EOF
#   (preceded by a single blank-line separator if the file is
#   non-empty and does not already end with a blank line).
# - Idempotent: same input + same file → byte-identical output
#   (we compare via `cmp -s` before atomic mv).

mb_write() {
  local file="$1" label="${2:-}"
  if [[ -z "$file" ]]; then
    echo "mb_write: missing file argument" >&2
    return 2
  fi
  _mb_markers "$file" "$label"

  # Slurp stdin into a temp file (preserves trailing newline
  # semantics; we control normalization below).
  local stdin_tmp
  stdin_tmp="$(mktemp "${TMPDIR:-/tmp}/mb-stdin.XXXXXX")" || return 1
  cat > "$stdin_tmp"

  local out_tmp
  out_tmp="$(mktemp "${TMPDIR:-/tmp}/mb-out.XXXXXX")" || {
    rm -f "$stdin_tmp"; return 1;
  }

  if [[ -f "$file" ]] && mb_has "$file" "$label"; then
    # Replace existing block content in place.
    awk \
      -v openm="$_MB_OPEN" \
      -v closem="$_MB_CLOSE" \
      -v body="$stdin_tmp" \
      '
      !replaced && index($0, openm) {
        print
        while ((getline line < body) > 0) print line
        close(body)
        in_block = 1
        replaced = 1
        next
      }
      in_block && index($0, closem) {
        print
        in_block = 0
        next
      }
      in_block { next }
      { print }
      ' "$file" > "$out_tmp"
  else
    # Append a new block at EOF.
    if [[ -f "$file" ]] && [[ -s "$file" ]]; then
      cat "$file" > "$out_tmp"
      # Normalize so the existing content ends in exactly one
      # newline, then add one blank-line separator before the
      # marker block.
      local last_byte
      last_byte="$(tail -c 1 "$file" | LC_ALL=C od -An -tu1 | tr -d ' \n')"
      if [[ "$last_byte" != "10" ]]; then
        printf '\n' >> "$out_tmp"
      fi
      # Blank-line separator unless the file already ends in
      # one (i.e. the last line is blank → already two NLs at
      # end after our normalization).
      local last_line
      last_line="$(tail -n 1 "$file" || true)"
      if [[ -n "$last_line" ]]; then
        printf '\n' >> "$out_tmp"
      fi
    else
      : > "$out_tmp"
    fi
    {
      printf '%s\n' "$_MB_OPEN"
      cat "$stdin_tmp"
      # Ensure stdin payload ends in newline before close marker.
      if [[ -s "$stdin_tmp" ]]; then
        local lb
        lb="$(tail -c 1 "$stdin_tmp" | LC_ALL=C od -An -tu1 | tr -d ' \n')"
        if [[ "$lb" != "10" ]]; then
          printf '\n'
        fi
      fi
      printf '%s\n' "$_MB_CLOSE"
    } >> "$out_tmp"
  fi

  rm -f "$stdin_tmp"

  # Idempotent commit: only replace if bytes differ.
  if [[ -f "$file" ]] && cmp -s "$file" "$out_tmp"; then
    rm -f "$out_tmp"
    return 0
  fi
  mv "$out_tmp" "$file"
}

# ----------------------------------------------------------
# mb_remove — delete the block (markers + content)
# ----------------------------------------------------------
#
# No-op if no such block. Trims at most one trailing blank
# line immediately above the removed block to avoid leaving
# orphan separators.

mb_remove() {
  local file="$1" label="${2:-}"
  [[ -f "$file" ]] || return 0
  mb_has "$file" "$label" || return 0
  _mb_markers "$file" "$label"

  local out_tmp
  out_tmp="$(mktemp "${TMPDIR:-/tmp}/mb-out.XXXXXX")" || return 1

  awk \
    -v openm="$_MB_OPEN" \
    -v closem="$_MB_CLOSE" \
    '
    {
      lines[NR] = $0
    }
    index($0, openm) && !o { o = NR }
    o && index($0, closem) && NR > o && !c { c = NR }
    END {
      # If a single blank line precedes the open marker, drop
      # it too so we do not leave a dangling separator.
      drop_from = o
      if (o > 1 && lines[o-1] == "") drop_from = o - 1
      for (i = 1; i <= NR; i++) {
        if (i >= drop_from && i <= c) continue
        print lines[i]
      }
    }
  ' "$file" > "$out_tmp"

  if cmp -s "$file" "$out_tmp"; then
    rm -f "$out_tmp"
    return 0
  fi
  mv "$out_tmp" "$file"
}
