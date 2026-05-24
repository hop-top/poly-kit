#!/usr/bin/env bash
# setup-release-please.sh — Generate release-please config for scaffolded projects
# Sourced by scaffold.sh; expects LANG_ARRAY, NAME, OUTPUT to be set.

setup_release_please() {
  local out="$1"
  shift
  local -a langs=("$@")

  echo "Generating release-please config..."

  local lang_count=${#langs[@]}
  local is_polyglot=false
  [ "$lang_count" -gt 1 ] && is_polyglot=true

  # --- Manifest (all versions start at 0.0.0) ---
  local manifest="{"
  local first=true

  for l in "${langs[@]}"; do
    case "$l" in
      go)
        if [ "$first" = true ]; then first=false; else manifest+=","; fi
        manifest+=$'\n  ".": "0.0.0"'
        ;;
      ts)
        if [ "$first" = true ]; then first=false; else manifest+=","; fi
        if [ "$is_polyglot" = true ]; then
          manifest+=$'\n  "ts": "0.0.0"'
        else
          manifest+=$'\n  ".": "0.0.0"'
        fi
        ;;
      py)
        if [ "$first" = true ]; then first=false; else manifest+=","; fi
        if [ "$is_polyglot" = true ]; then
          manifest+=$'\n  "py": "0.0.0"'
        else
          manifest+=$'\n  ".": "0.0.0"'
        fi
        ;;
      rs)
        if [ "$first" = true ]; then first=false; else manifest+=","; fi
        if [ "$is_polyglot" = true ]; then
          manifest+=$'\n  "rs": "0.0.0"'
        else
          manifest+=$'\n  ".": "0.0.0"'
        fi
        ;;
    esac
  done
  manifest+=$'\n}'
  echo "$manifest" > "$out/.release-please-manifest.json"

  # --- Config ---
  local has_go=false has_ts=false has_py=false has_rs=false
  for l in "${langs[@]}"; do
    case "$l" in
      go) has_go=true ;;
      ts) has_ts=true ;;
      py) has_py=true ;;
      rs) has_rs=true ;;
    esac
  done

  # Build exclude-paths for root Go package (polyglot only)
  local go_excludes=""
  if [ "$has_go" = true ] && [ "$is_polyglot" = true ]; then
    local ex_parts=()
    [ "$has_ts" = true ] && ex_parts+=("\"ts\"")
    [ "$has_py" = true ] && ex_parts+=("\"py\"")
    [ "$has_rs" = true ] && ex_parts+=("\"rs\"")
    ex_parts+=("\"templates\"")
    go_excludes=$(IFS=,; echo "${ex_parts[*]}")
  fi

  local pkg_entries=""
  local pkg_first=true

  if [ "$has_go" = true ]; then
    [ "$pkg_first" = true ] && pkg_first=false || pkg_entries+=","
    if [ "$is_polyglot" = true ]; then
      pkg_entries+="
    \".\": {
      \"release-type\": \"go\",
      \"component\": \"${NAME}\",
      \"changelog-path\": \"CHANGELOG.md\",
      \"bump-minor-pre-major\": true,
      \"bump-patch-for-minor-pre-major\": true,
      \"exclude-paths\": [${go_excludes}]
    }"
    else
      pkg_entries+="
    \".\": {
      \"release-type\": \"go\",
      \"component\": \"${NAME}\",
      \"changelog-path\": \"CHANGELOG.md\",
      \"bump-minor-pre-major\": true,
      \"bump-patch-for-minor-pre-major\": true
    }"
    fi
  fi

  if [ "$has_ts" = true ]; then
    [ "$pkg_first" = true ] && pkg_first=false || pkg_entries+=","
    local ts_path="."
    [ "$is_polyglot" = true ] && ts_path="ts"
    local ts_component="${NAME}"
    [ "$is_polyglot" = true ] && ts_component="ts/${NAME}"
    pkg_entries+="
    \"${ts_path}\": {
      \"release-type\": \"node\",
      \"component\": \"${ts_component}\",
      \"changelog-path\": \"CHANGELOG.md\",
      \"bump-minor-pre-major\": true,
      \"bump-patch-for-minor-pre-major\": true
    }"
  fi

  if [ "$has_py" = true ]; then
    [ "$pkg_first" = true ] && pkg_first=false || pkg_entries+=","
    local py_path="."
    [ "$is_polyglot" = true ] && py_path="py"
    local py_component="${NAME}"
    [ "$is_polyglot" = true ] && py_component="py/${NAME}"
    pkg_entries+="
    \"${py_path}\": {
      \"release-type\": \"python\",
      \"component\": \"${py_component}\",
      \"package-name\": \"${NAME}\",
      \"changelog-path\": \"CHANGELOG.md\",
      \"bump-minor-pre-major\": true,
      \"bump-patch-for-minor-pre-major\": true,
      \"extra-files\": [
        {
          \"type\": \"toml\",
          \"path\": \"pyproject.toml\",
          \"jsonpath\": \"$.project.version\"
        }
      ]
    }"
  fi

  if [ "$has_rs" = true ]; then
    [ "$pkg_first" = true ] && pkg_first=false || pkg_entries+=","
    local rs_path="."
    [ "$is_polyglot" = true ] && rs_path="rs"
    local rs_component="${NAME}"
    [ "$is_polyglot" = true ] && rs_component="rs/${NAME}"
    pkg_entries+="
    \"${rs_path}\": {
      \"release-type\": \"rust\",
      \"component\": \"${rs_component}\",
      \"changelog-path\": \"CHANGELOG.md\",
      \"bump-minor-pre-major\": true,
      \"bump-patch-for-minor-pre-major\": true,
      \"extra-files\": [
        {
          \"type\": \"toml\",
          \"path\": \"Cargo.toml\",
          \"jsonpath\": \"$.package.version\"
        }
      ]
    }"
  fi

  # Build linked-versions for polyglot (major.minor sync)
  local linked_versions=""
  if [ "$is_polyglot" = true ]; then
    local components=()
    [ "$has_go" = true ] && components+=("\"${NAME}\"")
    [ "$has_ts" = true ] && components+=("\"ts/${NAME}\"")
    [ "$has_py" = true ] && components+=("\"py/${NAME}\"")
    [ "$has_rs" = true ] && components+=("\"rs/${NAME}\"")
    local comp_list
    comp_list=$(IFS=,; echo "${components[*]}")
    linked_versions="
  \"linked-versions\": [
    { \"group\": \"${NAME}\", \"components\": [${comp_list}] }
  ],"
  fi

  cat > "$out/release-please-config.json" <<RPEOF
{
  "\$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
  "separate-pull-requests": false,
  "group-pull-request-title-pattern": "chore: release \${branch}",${linked_versions}
  "changelog-sections": [
    { "type": "feat", "section": "Features" },
    { "type": "fix", "section": "Bug Fixes" },
    { "type": "perf", "section": "Performance" },
    { "type": "refactor", "section": "Refactoring", "hidden": true },
    { "type": "chore", "section": "Miscellaneous", "hidden": true },
    { "type": "docs", "section": "Documentation", "hidden": true },
    { "type": "test", "section": "Tests", "hidden": true },
    { "type": "ci", "section": "CI", "hidden": true },
    { "type": "build", "section": "Build", "hidden": true }
  ],
  "packages": {${pkg_entries}
  }
}
RPEOF

  # --- Workflow ---
  setup_release_please_workflow "$out" "${langs[@]}"

  # --- Clean up legacy release workflows ---
  rm -f "$out/.github/workflows/release.yml"
  rm -f "$out/.github/workflows/release-go.yml"
  rm -f "$out/.github/workflows/release-ts.yml"
  rm -f "$out/.github/workflows/release-py.yml"
  rm -f "$out/.github/workflows/release-rs.yml"
}

setup_release_please_workflow() {
  local out="$1"
  shift
  local -a langs=("$@")

  local lang_count=${#langs[@]}
  local is_polyglot=false
  [ "$lang_count" -gt 1 ] && is_polyglot=true

  local has_go=false has_ts=false has_py=false has_rs=false
  for l in "${langs[@]}"; do
    case "$l" in
      go) has_go=true ;;
      ts) has_ts=true ;;
      py) has_py=true ;;
      rs) has_rs=true ;;
    esac
  done

  # Resolve TS package name from package.json if available
  local ts_pkg_name=""
  if [ "$has_ts" = true ]; then
    local ts_dir="$out"
    [ "$is_polyglot" = true ] && ts_dir="$out/ts"
    if [ -f "$ts_dir/package.json" ]; then
      ts_pkg_name=$(grep -o '"name"[[:space:]]*:[[:space:]]*"[^"]*"' "$ts_dir/package.json" \
        | head -1 | sed 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
    fi
    [ -z "$ts_pkg_name" ] && ts_pkg_name="${APP_NAME:-$NAME}"
  fi

  mkdir -p "$out/.github/workflows"

  # Build outputs block
  local outputs="      releases_created: \${{ steps.release.outputs.releases_created }}"
  if [ "$has_go" = true ]; then
    outputs+="
      go_release_created: \${{ steps.release.outputs.release_created }}
      go_tag_name: \${{ steps.release.outputs.tag_name }}"
  fi
  if [ "$has_ts" = true ]; then
    if [ "$is_polyglot" = true ]; then
      outputs+="
      ts_release_created: \${{ steps.release.outputs['ts--release_created'] }}
      ts_tag_name: \${{ steps.release.outputs['ts--tag_name'] }}"
    else
      outputs+="
      ts_release_created: \${{ steps.release.outputs.release_created }}
      ts_tag_name: \${{ steps.release.outputs.tag_name }}"
    fi
  fi
  if [ "$has_py" = true ]; then
    if [ "$is_polyglot" = true ]; then
      outputs+="
      py_release_created: \${{ steps.release.outputs['py--release_created'] }}
      py_tag_name: \${{ steps.release.outputs['py--tag_name'] }}"
    else
      outputs+="
      py_release_created: \${{ steps.release.outputs.release_created }}
      py_tag_name: \${{ steps.release.outputs.tag_name }}"
    fi
  fi
  if [ "$has_rs" = true ]; then
    if [ "$is_polyglot" = true ]; then
      outputs+="
      rs_release_created: \${{ steps.release.outputs['rs--release_created'] }}
      rs_tag_name: \${{ steps.release.outputs['rs--tag_name'] }}"
    else
      outputs+="
      rs_release_created: \${{ steps.release.outputs.release_created }}
      rs_tag_name: \${{ steps.release.outputs.tag_name }}"
    fi
  fi

  # Build release jobs
  local release_jobs=""

  if [ "$has_go" = true ]; then
    local go_version_file="go.mod"
    local go_wd=""
    if [ "$is_polyglot" = true ]; then
      go_version_file="go/go.mod"
      go_wd="
      working-directory: go"
    fi
    release_jobs+="
  release-go:
    needs: release-please
    if: \${{ needs.release-please.outputs.go_release_created == 'true' }}
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      # >>> kit-managed: toolchain >>>
      - uses: jdx/mise-action@v2
        with:
          install: true
          cache: true
      - run: mise run install
      # <<< kit-managed: toolchain <<<
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: '~> v2'
          args: release --clean${go_wd}
        env:
          GITHUB_TOKEN: \${{ secrets.GITHUB_TOKEN }}
          GORELEASER_CURRENT_TAG: \${{ needs.release-please.outputs.go_tag_name }}
"
  fi

  if [ "$has_ts" = true ]; then
    local ts_steps=""
    if [ "$is_polyglot" = true ]; then
      ts_steps="      - run: mise exec -- pnpm install --frozen-lockfile
      - run: mise exec -- pnpm --filter ${ts_pkg_name} exec vitest run
      - run: mise exec -- pnpm --filter ${ts_pkg_name} build
      - run: mise exec -- pnpm --filter ${ts_pkg_name} publish --access public --no-git-checks
        env:
          NODE_AUTH_TOKEN: \${{ secrets.NPM_TOKEN }}"
    else
      ts_steps="      - run: mise exec -- pnpm install --frozen-lockfile
      - run: mise exec -- pnpm exec vitest run
      - run: mise exec -- pnpm build
      - run: mise exec -- pnpm publish --access public --no-git-checks
        env:
          NODE_AUTH_TOKEN: \${{ secrets.NPM_TOKEN }}"
    fi
    release_jobs+="
  release-ts:
    needs: release-please
    if: \${{ needs.release-please.outputs.ts_release_created == 'true' }}
    runs-on: ubuntu-latest
    permissions:
      contents: read
      id-token: write
    steps:
      - uses: actions/checkout@v4
      # >>> kit-managed: toolchain >>>
      - uses: jdx/mise-action@v2
        with:
          install: true
          cache: true
      - run: mise run install
      # <<< kit-managed: toolchain <<<
${ts_steps}
"
  fi

  if [ "$has_py" = true ]; then
    local py_wd=""
    local py_dist="dist/"
    if [ "$is_polyglot" = true ]; then
      py_wd="
      - working-directory: py
        run: mise exec -- uv sync --all-extras
      - working-directory: py
        run: mise exec -- uv run pytest
      - working-directory: py
        run: mise exec -- uv build"
      py_dist="py/dist/"
    else
      py_wd="
      - run: mise exec -- uv sync --all-extras
      - run: mise exec -- uv run pytest
      - run: mise exec -- uv build"
    fi
    release_jobs+="
  release-py:
    needs: release-please
    if: \${{ needs.release-please.outputs.py_release_created == 'true' }}
    runs-on: ubuntu-latest
    permissions:
      contents: read
      id-token: write
    environment:
      name: pypi
      url: https://pypi.org/p/\${NAME:-app}
    steps:
      - uses: actions/checkout@v4
      # >>> kit-managed: toolchain >>>
      - uses: jdx/mise-action@v2
        with:
          install: true
          cache: true
      - run: mise run install
      # <<< kit-managed: toolchain <<<${py_wd}
      - uses: pypa/gh-action-pypi-publish@release/v1
        with:
          packages-dir: ${py_dist}
"
  fi

  if [ "$has_rs" = true ]; then
    local rs_test_step="      - run: mise exec -- cargo test --all-features --workspace"
    local rs_publish_step="      - run: mise exec -- cargo publish --token \${{ secrets.CARGO_REGISTRY_TOKEN }}"
    if [ "$is_polyglot" = true ]; then
      rs_test_step="      - working-directory: rs
        run: mise exec -- cargo test --all-features --workspace"
      rs_publish_step="      - working-directory: rs
        run: mise exec -- cargo publish --token \${{ secrets.CARGO_REGISTRY_TOKEN }}"
    fi
    release_jobs+="
  release-rs:
    needs: release-please
    if: \${{ needs.release-please.outputs.rs_release_created == 'true' }}
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@v4
      # >>> kit-managed: toolchain >>>
      - uses: jdx/mise-action@v2
        with:
          install: true
          cache: true
      - run: mise run install
      # <<< kit-managed: toolchain <<<
      - uses: Swatinem/rust-cache@v2
${rs_test_step}
${rs_publish_step}
"
  fi

  cat > "$out/.github/workflows/release-please.yml" <<WFEOF
name: Release Please

on:
  push:
    branches: [main]

permissions:
  contents: write
  pull-requests: write

jobs:
  release-please:
    runs-on: ubuntu-latest
    outputs:
${outputs}
    steps:
      - uses: googleapis/release-please-action@v4
        id: release
${release_jobs}
WFEOF
}
