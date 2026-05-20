#!/usr/bin/env bash
# reserve-packages.sh — Publish placeholder packages to npm/PyPI
# Sourced by scaffold.sh; expects OUTPUT, IS_POLYGLOT, LANG_ARRAY to be set.

# Prompt user for y/n confirmation; auto-accepts default when stdin is not a tty.
_reserve_prompt_yn() {
  local msg="$1" default="${2:-y}"
  if [ ! -t 0 ]; then
    REPLY="$default"
    return
  fi
  local hint="Y/n"
  [ "$default" = "n" ] && hint="y/N"
  printf "%s [%s]: " "$msg" "$hint" >&2
  read -r REPLY
  REPLY="${REPLY:-$default}"
  REPLY="$(echo "$REPLY" | tr '[:upper:]' '[:lower:]')"
}

# Publish empty 0.0.0 placeholder to npm; skips gracefully if not authed or name taken.
_reserve_npm() {
  local ts_dir="$1"

  if ! npm whoami >/dev/null 2>&1; then
    echo "npm: not authenticated — run 'npm login' to reserve package name"
    return 0
  fi

  local pkg_name=""
  if [ -f "$ts_dir/package.json" ]; then
    pkg_name=$(
      grep -o '"name"[[:space:]]*:[[:space:]]*"[^"]*"' "$ts_dir/package.json" \
        | head -1 | sed 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/'
    )
  fi

  if [ -z "$pkg_name" ]; then
    echo "npm: could not determine package name — skipping"
    return 0
  fi

  if npm view "$pkg_name" >/dev/null 2>&1; then
    echo "npm: $pkg_name already exists — skipping"
    return 0
  fi

  _reserve_prompt_yn "Reserve $pkg_name on npm?"
  if [ "$REPLY" = "y" ]; then
    echo "npm: publishing $pkg_name..."
    (cd "$ts_dir" && npm publish --access public 2>&1) \
      || echo "npm: publish failed — manual publish needed"
  fi
}

# Publish empty 0.0.0 placeholder to PyPI via uv; skips if uv missing or name taken.
_reserve_pypi() {
  local py_dir="$1"

  if ! command -v uv >/dev/null 2>&1; then
    echo "PyPI: uv not installed — install uv to reserve package name"
    return 0
  fi

  local py_name=""
  if [ -f "$py_dir/pyproject.toml" ]; then
    py_name=$(
      grep '^name' "$py_dir/pyproject.toml" \
        | head -1 | sed 's/.*= *"\(.*\)"/\1/'
    )
  fi

  if [ -z "$py_name" ]; then
    echo "PyPI: could not determine package name — skipping"
    return 0
  fi

  if curl -sf "https://pypi.org/pypi/$py_name/json" >/dev/null 2>&1; then
    echo "PyPI: $py_name already exists — skipping"
    return 0
  fi

  _reserve_prompt_yn "Reserve $py_name on PyPI?"
  if [ "$REPLY" = "y" ]; then
    echo "PyPI: publishing $py_name..."
    (cd "$py_dir" && uv build 2>&1 && uv publish 2>&1) \
      || echo "PyPI: publish failed — manual publish needed"
  fi
}

# Publish empty 0.0.0 placeholder to crates.io; skips if cargo missing, not authed, or name taken.
_reserve_cratesio() {
  local rs_dir="$1"

  if ! command -v cargo >/dev/null 2>&1; then
    echo "crates.io: cargo not installed — install Rust toolchain to reserve crate name"
    return 0
  fi

  if [ ! -f "$HOME/.cargo/credentials.toml" ] \
    && [ ! -f "$HOME/.cargo/credentials" ] \
    && [ -z "$CARGO_REGISTRY_TOKEN" ]; then
    echo "crates.io: not authenticated — run 'cargo login' or set CARGO_REGISTRY_TOKEN to reserve crate name"
    return 0
  fi

  local crate_name=""
  if [ -f "$rs_dir/Cargo.toml" ]; then
    crate_name=$(
      grep -E '^[[:space:]]*name[[:space:]]*=' "$rs_dir/Cargo.toml" \
        | head -1 | sed 's/.*= *"\(.*\)".*/\1/'
    )
  fi

  if [ -z "$crate_name" ]; then
    echo "crates.io: could not determine crate name — skipping"
    return 0
  fi

  if curl -sf "https://crates.io/api/v1/crates/$crate_name" >/dev/null 2>&1; then
    echo "crates.io: $crate_name already exists — skipping"
    return 0
  fi

  _reserve_prompt_yn "Reserve $crate_name on crates.io?"
  if [ "$REPLY" = "y" ]; then
    echo "crates.io: publishing $crate_name..."
    (cd "$rs_dir" && cargo publish --allow-dirty 2>&1) \
      || echo "crates.io: publish failed — manual publish needed"
  fi
}

# Entry point: reserves npm/PyPI/crates.io names for ts/py/rs languages detected in LANG_ARRAY.
# Go is excluded (proxy.golang.org auto-indexes). Called by scaffold.sh post-clone.
reserve_package_names() {
  local has_ts=false has_py=false has_rs=false
  for l in "${LANG_ARRAY[@]}"; do
    case "$l" in
      ts) has_ts=true ;;
      py) has_py=true ;;
      rs) has_rs=true ;;
    esac
  done

  # Nothing to reserve for Go (proxy.golang.org auto-indexes)
  if [ "$has_ts" = false ] && [ "$has_py" = false ] && [ "$has_rs" = false ]; then
    return 0
  fi

  echo ""
  echo "Checking package name availability..."

  if [ "$has_ts" = true ]; then
    local ts_dir="$OUTPUT"
    [ "$IS_POLYGLOT" = true ] && ts_dir="$OUTPUT/ts"
    _reserve_npm "$ts_dir"
  fi

  if [ "$has_py" = true ]; then
    local py_dir="$OUTPUT"
    [ "$IS_POLYGLOT" = true ] && py_dir="$OUTPUT/py"
    _reserve_pypi "$py_dir"
  fi

  if [ "$has_rs" = true ]; then
    local rs_dir="$OUTPUT"
    [ "$IS_POLYGLOT" = true ] && rs_dir="$OUTPUT/rs"
    _reserve_cratesio "$rs_dir"
  fi
}
