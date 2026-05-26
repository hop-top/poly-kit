#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SHARED="$SCRIPT_DIR/shared"
DIST="$SCRIPT_DIR/dist"

# Clean dist
rm -rf "$DIST"

# copy_shared <dest>
# Copies community files, docs, devcontainer, init.sh
copy_shared() {
  local dest="$1"

  cp "$SHARED/LICENSE-MIT.tmpl" "$dest/LICENSE-MIT"
  cp "$SHARED/LICENSE-Apache-2.0.tmpl" "$dest/LICENSE-Apache-2.0"
  cp "$SHARED/SECURITY.md.tmpl" "$dest/SECURITY.md"
  cp "$SHARED/CONTRIBUTING.md.tmpl" "$dest/CONTRIBUTING.md"
  cp "$SHARED/RELEASING.md" "$dest/"
  cp "$SHARED/init.sh" "$dest/"
  chmod +x "$dest/init.sh"

  # Docs
  cp -r "$SHARED/docs" "$dest/"
  mkdir -p "$dest/docs/stories" "$dest/docs/personas"
  touch "$dest/docs/stories/.gitkeep"
  touch "$dest/docs/personas/.gitkeep"

  # Scripts
  mkdir -p "$dest/scripts"
  cp "$SHARED/scripts/"* "$dest/scripts/"
  chmod +x "$dest/scripts/"*

  # Devcontainer — emitted by templates/shared/emit-devcontainer-json.sh
  # and templates/shared/emit-docker-compose.sh during scaffold.sh; no
  # pre-existing template files to copy here. The directory is created
  # at emit time.
}

# compose_gitignore <dest> <lang...>
# Merges common + per-lang gitignore files into dest/.gitignore
compose_gitignore() {
  local dest="$1"; shift
  local parts=("$SHARED/gitignore/common.gitignore")
  for lang in "$@"; do
    parts+=("$SHARED/gitignore/${lang}.gitignore")
  done
  cat "${parts[@]}" > "$dest/.gitignore"
}

# copy_ci_single <dest> <lang>
# Generates a single-lang dependabot.yml + copies lang-specific CI as ci.yml.
copy_ci_single() {
  local dest="$1" lang="$2"
  mkdir -p "$dest/.github/workflows"

  # Per-lang dependabot ecosystem
  local ecosystem
  case "$lang" in
    go) ecosystem="gomod" ;;
    ts) ecosystem="npm"   ;;
    py) ecosystem="pip"   ;;
    rs) ecosystem="cargo" ;;
    *)
      echo "Warning: no dependabot ecosystem for lang=$lang" >&2
      ecosystem=""
      ;;
  esac

  if [ -n "$ecosystem" ]; then
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

  local src="$SHARED/ci/ci-${lang}.yml"
  [ -f "$src" ] || src="${src}.tmpl"
  cp "$src" "$dest/.github/workflows/ci.yml"
}

# overlay_lang <dest> <lang>
# Copies lang-specific source (including hidden files)
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
  copy_shared "$dest"
  copy_ci_single "$dest" "$lang"
  overlay_lang "$dest" "$lang"
done

# --- Polyglot template ---
poly="$DIST/cli-template-polyglot"
mkdir -p "$poly"

compose_gitignore "$poly" go ts py rs php
copy_shared "$poly"

# CI -- all lang workflows
mkdir -p "$poly/.github/workflows"
# Polyglot dependabot — watch subdirectories
cat > "$poly/.github/dependabot.yml" << 'DEPEOF'
version: 2
updates:
  - package-ecosystem: gomod
    directory: "/go"
    schedule:
      interval: weekly
    commit-message:
      prefix: "build(deps):"

  - package-ecosystem: npm
    directory: "/ts"
    schedule:
      interval: weekly
    commit-message:
      prefix: "build(deps):"

  - package-ecosystem: pip
    directory: "/py"
    schedule:
      interval: weekly
    commit-message:
      prefix: "build(deps):"

  - package-ecosystem: cargo
    directory: "/rs"
    schedule:
      interval: weekly
    commit-message:
      prefix: "build(deps):"
DEPEOF
for lang in go ts py rs; do
  src="$SHARED/ci/ci-${lang}.yml"
  [ -f "$src" ] || src="${src}.tmpl"
  cp "$src" "$poly/.github/workflows/ci-${lang}.yml"
done

# Lang dirs (without shared root files)
for lang in go ts py rs; do
  src="$SCRIPT_DIR/cli-${lang}"
  langdir="$poly/$lang"
  mkdir -p "$langdir"
  if [ ! -d "$src" ]; then
    echo "Warning: $src not found, skipping" >&2
    continue
  fi
  for entry in "$src"/* "$src"/.*; do
    base="$(basename "$entry")"
    case "$base" in
      .|..|CHANGELOG.md|README.md) continue ;;
      *)  cp -r "$entry" "$langdir/" ;;
    esac
  done
done

# Polyglot root Makefile -- delegates to lang dirs
cat > "$poly/Makefile" << 'MKEOF'
.PHONY: build test lint links check setup

check: lint test links

build test lint setup:
	$(MAKE) -C go $@
	$(MAKE) -C ts $@
	$(MAKE) -C py $@
	$(MAKE) -C rs $@

links:
	@if command -v lychee >/dev/null 2>&1; then \
		lychee --no-progress .; \
	else \
		echo "lychee not installed; skipping link check"; \
	fi
MKEOF

cat > "$poly/CHANGELOG.md" << 'CLEOF'
# Changelog

All notable changes documented here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)

## [Unreleased]
CLEOF

cat > "$poly/README.md" << 'RMEOF'
# {{app_name}}

> {{description}}

## Quick Start

```bash
./init.sh
```

Select your languages and configure the project.

## Structure

- `go/` — Go CLI source
- `ts/` — TypeScript CLI source
- `py/` — Python CLI source
- `rs/` — Rust CLI source

## Development

```bash
make check    # lint + test all languages
make setup    # install deps for all languages
```

## License

{{license}} — see [LICENSE](LICENSE).
RMEOF

echo "Build complete. Output in $DIST/"
echo "Templates:"
ls -d "$DIST"/cli-template-*
