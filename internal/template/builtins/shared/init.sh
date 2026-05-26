#!/usr/bin/env bash
set -euo pipefail

# -------------------------------------------------------
# init.sh — Post-clone project initializer
# Replaces {{placeholder}} tokens, configures license,
# optionally prunes unselected languages (polyglot).
# Compatible with macOS + Linux.
#
# Environment overrides (for CI/automation):
#   INIT_APP_NAME, INIT_DESCRIPTION, INIT_AUTHOR_NAME,
#   INIT_AUTHOR_EMAIL, INIT_LICENSE, INIT_MODULE_PREFIX,
#   INIT_LANGUAGES (comma-separated, polyglot only)
# -------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$SCRIPT_DIR"
YEAR="$(date +%Y)"

# shellcheck source=../lib.sh
source "$SCRIPT_DIR/../lib.sh"

# --- Detect polyglot -----------------------------------

is_polyglot=false
if [ -d "$PROJECT_DIR/go" ] && \
   [ -d "$PROJECT_DIR/ts" ] && \
   [ -d "$PROJECT_DIR/py" ] && \
   [ -d "$PROJECT_DIR/rs" ]; then
  is_polyglot=true
fi

# --- Prompts (env vars override for automation) --------

default_name="$(basename "$PROJECT_DIR")"

app_name="${INIT_APP_NAME:-}"
[ -z "$app_name" ] && prompt app_name "App name" "$default_name"

description="${INIT_DESCRIPTION:-}"
[ -z "$description" ] && prompt description "Description" ""

author_name="${INIT_AUTHOR_NAME:-}"
[ -z "$author_name" ] && prompt author_name "Author name" ""

author_email="${INIT_AUTHOR_EMAIL:-}"
[ -z "$author_email" ] && prompt author_email "Author email" ""

license="${INIT_LICENSE:-}"
[ -z "$license" ] && prompt_choice license "License" "MIT/Apache-2.0" "MIT"

module_prefix="${INIT_MODULE_PREFIX:-}"
[ -z "$module_prefix" ] && prompt module_prefix \
  "Module prefix (e.g. github.com/user)" ""

selected_langs=""
if [ "$is_polyglot" = true ]; then
  selected_langs="${INIT_LANGUAGES:-}"
  [ -z "$selected_langs" ] && prompt_multi selected_langs \
    "Languages (comma-separated)" \
    "go,ts,py,rs" "go,ts,py,rs"
fi

# --- Derived tokens ------------------------------------

# Uppercase app name for env var prefixes (e.g. my-app -> MY_APP)
app_name_upper="$(echo "$app_name" | tr '[:lower:]-' '[:upper:]_')"

# Org: trailing segment of module_prefix (github.com/foo -> foo).
# Strip trailing slash first so `github.com/foo/` still yields `foo`.
# Falls back to author_name (then app_name) if no usable segment.
module_prefix_trimmed="${module_prefix%/}"
if [[ "$module_prefix_trimmed" == */* ]]; then
  org="${module_prefix_trimmed##*/}"
else
  org="$module_prefix_trimmed"
fi
[ -z "$org" ] && org="${author_name:-$app_name}"

# PascalCase converter: splits on hyphen/underscore/space and
# title-cases each segment. Camel-cased input (e.g. myApp) is
# treated as a single segment, producing Myapp — fine for the
# CLI naming conventions we target.
to_pascal() {
  printf '%s' "$1" \
    | awk 'BEGIN{FS="[-_ ]+"} {
        out=""
        for (i=1; i<=NF; i++) {
          w=$i
          if (length(w) > 0) {
            out = out toupper(substr(w,1,1)) tolower(substr(w,2))
          }
        }
        print out
      }'
}

vendor_namespace="$(to_pascal "$org")"
name_namespace="$(to_pascal "$app_name")"
module="${module_prefix:+${module_prefix}/}${app_name}"

# --- Replace tokens ------------------------------------

echo "Replacing tokens..."

# Go text/template dot-notation tokens used across cli-*/templates and
# shared/. Order matters: substitute longer/qualified tokens before shorter
# ones to avoid partial matches (e.g. NameUpper before Name).
replace_token "{{.NameUpper}}"       "$app_name_upper"
replace_token "{{.NameNamespace}}"   "$name_namespace"
replace_token "{{.VendorNamespace}}" "$vendor_namespace"
replace_token "{{.Description}}"     "$description"
replace_token "{{.Author}}"          "$author_name"
replace_token "{{.Email}}"           "$author_email"
replace_token "{{.License}}"         "$license"
replace_token "{{.CrateName}}"       "$app_name"
replace_token "{{.Module}}"          "$module"
replace_token "{{.Vendor}}"          "$org"
replace_token "{{.Year}}"            "$YEAR"
replace_token "{{.Org}}"             "$org"
replace_token "{{.Name}}"            "$app_name"

# Unwrap Go-template literal escapes `{{` `…` `}}` used to pass
# `{{ .Version }}`-style tokens through to downstream renderers
# like goreleaser. Strip the outer escape, keep the inner text.
replace_token '{{`'                  ""
replace_token '`}}'                  ""

# --- License -------------------------------------------

echo "Configuring license..."

# License sources ship with a .tmpl suffix so language toolchains
# ignore them; strip the suffix early so the cp below resolves on
# both shipped (LICENSE-*.tmpl) and pre-stripped (LICENSE-*) layouts.
for f in "$PROJECT_DIR"/LICENSE-MIT.tmpl "$PROJECT_DIR"/LICENSE-Apache-2.0.tmpl; do
  [ -e "$f" ] || continue
  mv "$f" "${f%.tmpl}"
done

if [ "$license" = "Apache-2.0" ]; then
  cp "$PROJECT_DIR/LICENSE-Apache-2.0" \
    "$PROJECT_DIR/LICENSE"
else
  cp "$PROJECT_DIR/LICENSE-MIT" \
    "$PROJECT_DIR/LICENSE"
fi

# Clean up license templates
rm -f "$PROJECT_DIR/LICENSE-MIT" \
  "$PROJECT_DIR/LICENSE-Apache-2.0"

# --- Module paths --------------------------------------

# Fix module paths at root (single-lang templates)
if [ -f "$PROJECT_DIR/go.mod" ]; then
  sedi \
    "s|module .*|module ${module_prefix}/${app_name}|" \
    "$PROJECT_DIR/go.mod"
fi

if [ -f "$PROJECT_DIR/package.json" ]; then
  sedi \
    "s|\"name\": \".*\"|\"name\": \"${app_name}\"|" \
    "$PROJECT_DIR/package.json"
fi

if [ -f "$PROJECT_DIR/pyproject.toml" ]; then
  sedi \
    "s|name = \".*\"|name = \"${app_name}\"|" \
    "$PROJECT_DIR/pyproject.toml"
fi

# Fix module paths in polyglot subdirs
if [ "$is_polyglot" = true ]; then
  if [ -f "$PROJECT_DIR/go/go.mod" ]; then
    sedi \
      "s|module .*|module ${module_prefix}/${app_name}|" \
      "$PROJECT_DIR/go/go.mod"
  fi

  if [ -f "$PROJECT_DIR/ts/package.json" ]; then
    sedi \
      "s|\"name\": \".*\"|\"name\": \"${app_name}\"|" \
      "$PROJECT_DIR/ts/package.json"
  fi

  if [ -f "$PROJECT_DIR/py/pyproject.toml" ]; then
    sedi \
      "s|name = \".*\"|name = \"${app_name}\"|" \
      "$PROJECT_DIR/py/pyproject.toml"
  fi
fi

# --- Rename placeholder path segments ------------------

# Some templates embed {{.Name}} in directory/file names
# (e.g. cli-py/src/{{.Name}}/, cli-php/bin/{{.Name}}.tmpl).
# Walk depth-first so we rename leaves before their parents.
echo "Renaming placeholder paths..."
while IFS= read -r path; do
  [ -e "$path" ] || continue
  new="${path//\{\{.Name\}\}/$app_name}"
  [ "$path" = "$new" ] && continue
  mv "$path" "$new"
done < <(find "$PROJECT_DIR" -depth -name '*{{.Name}}*' \
  ! -path '*/.git/*' \
  ! -path '*/node_modules/*' \
  ! -path '*/vendor/*')

# --- Strip .tmpl suffixes ------------------------------

# Rendering tokens above is in-place; templates ship with
# a .tmpl suffix so language toolchains ignore them until
# rendered. Strip the suffix now.
echo "Stripping .tmpl suffixes..."
while IFS= read -r f; do
  mv "$f" "${f%.tmpl}"
done < <(find "$PROJECT_DIR" -type f -name '*.tmpl' \
  ! -path '*/.git/*' \
  ! -path '*/node_modules/*' \
  ! -path '*/vendor/*')

# --- Polyglot: prune unselected languages --------------

if [ "$is_polyglot" = true ]; then
  echo "Pruning unselected languages..."

  # Use comma-delimited string for Bash 3.2 compat (no assoc arrays)
  keep_langs=",$(echo "$selected_langs" | tr -d ' '),"

  for lang in go ts py rs; do
    if [[ "$keep_langs" != *",$lang,"* ]]; then
      echo "  Removing $lang..."

      # Remove language directory
      rm -rf "${PROJECT_DIR:?}/${lang}"

      # Remove CI workflows
      rm -f "$PROJECT_DIR/.github/workflows/ci-${lang}.yml"
      rm -f "$PROJECT_DIR/.github/workflows/release-${lang}.yml"

      # Remove delegation lines from root Makefile
      if [ -f "$PROJECT_DIR/Makefile" ]; then
        sedi "/\$(MAKE) -C ${lang}/d" "$PROJECT_DIR/Makefile"
      fi
    fi
  done
fi

# --- Remove manifest leftovers -------------------------

# kit-template.yaml + tiers.yaml describe the template to the
# render pipeline; they don't belong in the rendered project.
# Mirrors the Go engine's render_rules.remove_after_render so
# both paths produce identical output trees.
rm -f "$PROJECT_DIR/kit-template.yaml" "$PROJECT_DIR/tiers.yaml"

# --- Git init ------------------------------------------

if [ ! -d "$PROJECT_DIR/.git" ]; then
  echo "Initializing git repository..."
  git -C "$PROJECT_DIR" init
fi

# Use prompted author info for local git config
git -C "$PROJECT_DIR" config user.name "$author_name"
git -C "$PROJECT_DIR" config user.email "$author_email"

echo "Creating initial commit..."
git -C "$PROJECT_DIR" add -A
git -C "$PROJECT_DIR" commit -m \
  "feat: initialize ${app_name}"

# --- Cleanup -------------------------------------------

echo "Cleaning up..."
rm -f "$0"

echo ""
echo "Done! ${app_name} is ready."
echo "Run 'cd ${PROJECT_DIR}' to get started."
