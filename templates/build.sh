#!/usr/bin/env bash
# shellcheck disable=SC1091
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST="$SCRIPT_DIR/dist"

# Shared composition helpers (compose_gitignore, compose_gitattributes,
# copy_shared, dependabot_ecosystem, ci_workflow_src, overlay_lang_dir,
# compose_polyglot_*). Sourced by both build.sh and scaffold.sh so the
# composition rules have one home.
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

# Clean dist
rm -rf "$DIST"

# copy_ci_single <dest> <lang>
# Generates a single-lang dependabot.yml + copies lang-specific CI as ci.yml.
copy_ci_single() {
  local dest="$1" lang="$2"
  mkdir -p "$dest/.github/workflows"

  local ecosystem
  ecosystem="$(dependabot_ecosystem "$lang")"
  if [ -z "$ecosystem" ]; then
    echo "Warning: no dependabot ecosystem for lang=$lang" >&2
  else
    cat > "$dest/.github/dependabot.yml" <<DEPEOF
version: 2
updates:
  - package-ecosystem: $ecosystem
    directory: "/"
    schedule:
      interval: weekly
    commit-message:
      prefix: "build(deps):"
DEPEOF
  fi

  local src
  src="$(ci_workflow_src "$lang")"
  cp "$src" "$dest/.github/workflows/ci.yml"
}

# overlay_lang <dest> <lang>
# Copies lang-specific source (including hidden files) flat into $dest
# — used by single-lang dist builds. NOT the same as
# overlay_lang_dir (lib.sh), which copies under $dest/<lang>/.
overlay_lang() {
  local dest="$1" lang="$2"
  local src="$SCRIPT_DIR/cli-${lang}"
  if [ ! -d "$src" ]; then
    echo "Warning: $src not found, skipping" >&2
    return
  fi
  # Regular files/dirs
  cp -r "$src/"* "$dest/" 2>/dev/null || true
  # Hidden files (dotfiles like .golangci.yml)
  for f in "$src"/.*; do
    case "$(basename "$f")" in
      .|..) continue ;;
      *)    cp -r "$f" "$dest/" ;;
    esac
  done
}

# --- Single-language templates ---
for lang in go ts py rs php; do
  dest="$DIST/cli-template-$lang"
  mkdir -p "$dest"

  compose_gitignore "$dest" "$lang"
  compose_gitattributes "$dest" "$lang"
  copy_shared "$dest"
  copy_ci_single "$dest" "$lang"
  overlay_lang "$dest" "$lang"
done

# --- Base template (lang-agnostic; consumed by scaffold.sh for polyglot) ---
# Contains only content that ANY polyglot project gets regardless of which
# --langs subset is chosen. Lang-specific overlays (per-lang dirs, CI
# workflows, dependabot ecosystems, root Makefile) are composed at
# scaffold time based on LANG_ARRAY.
base="$DIST/cli-template-base"
mkdir -p "$base"

compose_gitignore "$base"        # common only, no langs
compose_gitattributes "$base"    # common only, no langs
copy_shared "$base"

cat > "$base/CHANGELOG.md" << 'CLEOF'
# Changelog

All notable changes documented here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)

## [Unreleased]
CLEOF

cat > "$base/README.md" << 'RMEOF'
# {{.Name}}

> {{.Description}}

## Quick Start

```bash
./init.sh
```

Select your languages and configure the project.

## Development

```bash
make check    # lint + test all enabled languages
make setup    # install deps for all enabled languages
```

## License

{{.License}} — see [LICENSE](LICENSE).
RMEOF

echo "Build complete. Output in $DIST/"
echo "Templates:"
ls -d "$DIST"/cli-template-*
