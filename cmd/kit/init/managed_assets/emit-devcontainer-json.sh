#!/usr/bin/env bash
# emit-devcontainer-json.sh — Emit compose-mode .devcontainer/devcontainer.json
#
# Public function:
#   emit_devcontainer_json <project-dir> <project-name> <lang-csv>
#
# Writes <project-dir>/.devcontainer/devcontainer.json as JSON-C
# (JSON with `//` line comments) per the spec at
# `.tlc/tracks/scaffold-emits-mise-toml-devcontainer-compose/spec.md` §5.
#
# The file is composed in two parts:
#
#   1. A static scaffold (name, dockerComposeFile, service,
#      workspaceFolder, remoteUser, features, customizations
#      shell, forwardPorts) written on first emit only.
#   2. Kit-managed blocks (postCreate/postStart commands, the
#      per-language VS Code extension list) maintained via the
#      managed-block library so they remain idempotently
#      refreshable by `kit init`.
#
# JSON-C is the devcontainer.json native format — comments
# starting with `//` and trailing commas are allowed by the
# devcontainers spec, so the emitted file parses with any
# JSON-C-aware loader (VS Code, devcontainers CLI). For plain
# `jq` validation in tests, strip `//` line comments first.
#
# Dependencies: templates/shared/managed-block.sh must be
# sourced by the caller before invoking emit_devcontainer_json.
#
# shellcheck disable=SC2034

# Guard against double-sourcing.
[[ -n "${_KIT_EMIT_DEVCONTAINER_JSON_LOADED:-}" ]] && return 0
_KIT_EMIT_DEVCONTAINER_JSON_LOADED=1

# ----------------------------------------------------------
# Extension map — per spec §5 and task brief.
# ----------------------------------------------------------
#
# Always emitted (kit conventions):
#   - jdx.mise-vscode          — surface mise tasks in VS Code
#   - EditorConfig.EditorConfig — respect project .editorconfig
#
# Per-language additions, gated by --lang csv membership:
#   go → golang.go
#   ts → dbaeumer.vscode-eslint, esbenp.prettier-vscode
#   py → ms-python.python, ms-python.vscode-pylance, charliermarsh.ruff
#   rs → rust-lang.rust-analyzer, tamasfe.even-better-toml

# Echo each VS Code extension id for a comma-separated <lang-csv>
# argument. Always-on extensions are emitted first, then any
# per-language extensions in the canonical order go → ts → py →
# rs (independent of --lang argument order, so two scaffolds with
# the same lang set produce byte-identical files).
_edj_extensions() {
  local lang_csv="$1"
  # Always-included extensions.
  printf '%s\n' 'jdx.mise-vscode'
  printf '%s\n' 'EditorConfig.EditorConfig'
  # Membership tests on the csv. Wrap in commas to make
  # boundary-checked substring search safe (",go," matches but
  # ",rs," does not match ",go,").
  local csv=",${lang_csv},"
  if [[ "$csv" == *,go,* ]]; then
    printf '%s\n' 'golang.go'
  fi
  if [[ "$csv" == *,ts,* ]]; then
    printf '%s\n' 'dbaeumer.vscode-eslint'
    printf '%s\n' 'esbenp.prettier-vscode'
  fi
  if [[ "$csv" == *,py,* ]]; then
    printf '%s\n' 'ms-python.python'
    printf '%s\n' 'ms-python.vscode-pylance'
    printf '%s\n' 'charliermarsh.ruff'
  fi
  if [[ "$csv" == *,rs,* ]]; then
    printf '%s\n' 'rust-lang.rust-analyzer'
    printf '%s\n' 'tamasfe.even-better-toml'
  fi
}

# Emit the JSON-encoded extension list lines (8-space indent),
# one per line, each ending with a trailing comma so the
# managed-block content is uniform regardless of how `kit init`
# later replaces it. Trailing commas inside a JSON array are
# legal in JSON-C (devcontainer.json) and tolerated by jq when
# the // comments are stripped before parsing.
_edj_extensions_block() {
  local lang_csv="$1"
  local ext
  while IFS= read -r ext; do
    [[ -z "$ext" ]] && continue
    printf '        "%s",\n' "$ext"
  done < <(_edj_extensions "$lang_csv")
}

# ----------------------------------------------------------
# Static scaffold writer — writes the outer JSON-C only if the
# file does not yet exist. Subsequent calls leave user edits
# outside the managed blocks intact.
# ----------------------------------------------------------

_edj_write_scaffold() {
  local file="$1" name="$2"
  if [[ -f "$file" ]]; then
    return 0
  fi
  mkdir -p "$(dirname "$file")"
  cat > "$file" <<JSONC
{
  "name": "${name}",
  "dockerComposeFile": "docker-compose.yml",
  "service": "devcontainer",
  "workspaceFolder": "/workspace",
  "remoteUser": "dev",

  "features": {
    "ghcr.io/devcontainers/features/common-utils:2": {
      "username": "dev",
      "installZsh": true
    },
    "ghcr.io/jdx/mise/features/mise:1": {}
  },

  // >>> kit-managed: lifecycle >>>
  // <<< kit-managed: lifecycle <<<

  "customizations": {
    "vscode": {
      "extensions": [
        // >>> kit-managed: extensions >>>
        // <<< kit-managed: extensions <<<
      ]
    }
  },

  "forwardPorts": [16686, 4318]
}
JSONC
}

# ----------------------------------------------------------
# Public: emit_devcontainer_json <project-dir> <name> <lang-csv>
# ----------------------------------------------------------

emit_devcontainer_json() {
  local project_dir="$1" name="$2" lang_csv="$3"

  if [[ -z "${project_dir:-}" || -z "${name:-}" || -z "${lang_csv:-}" ]]; then
    echo "emit_devcontainer_json: usage: emit_devcontainer_json <project-dir> <project-name> <lang-csv>" >&2
    return 2
  fi

  # Sanity: managed-block.sh must be loaded already.
  if ! declare -F mb_write >/dev/null 2>&1; then
    echo "emit_devcontainer_json: managed-block.sh must be sourced first" >&2
    return 2
  fi

  local file="${project_dir}/.devcontainer/devcontainer.json"
  _edj_write_scaffold "$file" "$name"

  # Lifecycle commands — mise trust to accept the project's
  # mise.toml without an interactive prompt, then install pinned
  # tools, then orchestrate ecosystem package managers via the
  # mise `install` task. postStartCommand keeps the container's
  # tools in sync after host-side bumps to mise.toml.
  mb_write "$file" lifecycle <<'LIFECYCLE'
  "postCreateCommand": "mise trust && mise install && mise run install",
  "postStartCommand": "mise install --quiet",
LIFECYCLE

  # Per-language extension list. Builds deterministically from
  # lang-csv so two emits with the same inputs are byte-identical.
  local ext_payload
  ext_payload="$(_edj_extensions_block "$lang_csv")"
  printf '%s' "$ext_payload" | mb_write "$file" extensions
}
