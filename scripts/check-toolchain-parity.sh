#!/usr/bin/env bash
#
# check-toolchain-parity.sh — fail when a declared tool version disagrees
# with the pin in mise.toml.
#
# mise.toml is the repo's toolchain source of truth. CI jobs read it
# through scripts/toolchain-pins.sh, and so does the Makefile. Some files
# cannot read it and carry their own copy of a version; this script holds
# each copy to the pin:
#
#   go.mod (root)          `go` directive equals the go pin (setup-go reads
#                          it via go-version-file); a `toolchain` line, if
#                          any, names the same release
#   other go.mod files     `go` / `toolchain` no newer than the go pin, or
#                          GOTOOLCHAIN=auto quietly swaps compilers
#   rust-toolchain.toml    `channel` equals the rust pin (mise exports
#                          RUSTUP_TOOLCHAIN, which overrides the file)
#   uv.lock                locked ruff equals the ruff pin (`uv run ruff`
#                          is what `make lint-py` runs)
#   workflows, actions     every literal node-version, python-version,
#                          go-version, toolchain / rust-toolchain,
#                          lycheeVersion, and the `version:` input of
#                          pnpm/action-setup, astral-sh/setup-uv,
#                          bufbuild/buf-action, golangci/golangci-lint-action
#                          equals its pin; php-version equals the floor of
#                          `require.php` in sdk/experimental/php/composer.json
#                          (PHP is not a mise tool: mise builds it from source)
#   devcontainer           the nixpkgs attributes in .devcontainer/flake.nix
#                          (go_1_NN, nodejs_NN, python3NN) and the store
#                          globs in its Dockerfile name the pinned
#                          go minor, node major and python minor
#
# A literal that differs on purpose (a release job that tests the oldest
# supported Python, say) carries `toolchain-parity: allow` in a comment on
# the same line, with the reason.
#
# Expressions (`${{ ... }}`) are not literals and are skipped: that is how
# a workflow reads a pin (`${{ steps.pins.outputs.node }}`).
#
# Files under templates/ and internal/template/ pin SCAFFOLDED projects'
# tools, not this repo's, and are out of scope.
#
# Usage: scripts/check-toolchain-parity.sh   (from anywhere inside the repo)
# Exit:  0 agree, 1 disagreement found, 2 a pin or file could not be read.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

pins_script="$root/scripts/toolchain-pins.sh"
pins=$("$pins_script") || exit 2

# Pins CI or the Makefile consume. Missing one would make a workflow
# step read an empty version and fall back to the action's default.
required="go node pnpm python uv rust golangci-lint ruff lychee buf markdownlint-cli2"

pin() {
    printf '%s\n' "$pins" | awk -F= -v want="$1" '$1 == want { print $2; exit }'
}

findings=0
report() {
    # report <file:line> <tool> <declared> <expected> <expected-from>
    printf '%s: %s %s disagrees with %s %s\n' "$1" "$2" "$3" "$5" "$4"
    findings=$((findings + 1))
}

# version_gt <a> <b>: true when a is a strictly newer version than b.
version_gt() {
    [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1)" = "$1" ]
}

# major_minor 1.26.1 -> 1.26
major_minor() { printf '%s\n' "$1" | awk -F. '{ print $1 "." $2 }'; }

tracked() {
    git ls-files -- "$@" ':(exclude)templates/**' ':(exclude)internal/template/**'
}

for t in $required; do
    if [ -z "$(pin "$t")" ]; then
        printf 'mise.toml: no pin for %s (CI and the Makefile read it)\n' "$t"
        findings=$((findings + 1))
    fi
done

go_pin=$(pin go)
rust_pin=$(pin rust)
ruff_pin=$(pin ruff)
node_pin=$(pin node)
python_pin=$(pin python)

# --- go.mod ---------------------------------------------------------------
if [ -n "$go_pin" ]; then
    while IFS= read -r mod; do
        [ -n "$mod" ] || continue
        while IFS=$'\t' read -r line kind ver; do
            [ -n "$ver" ] || continue
            if [ "$mod" = "go.mod" ]; then
                [ "$ver" = "$go_pin" ] || report "$mod:$line" "go ($kind)" "$ver" "$go_pin" "mise.toml go"
            elif version_gt "$ver" "$go_pin"; then
                report "$mod:$line" "go ($kind)" "$ver" "$go_pin (or older)" "mise.toml go"
            fi
        done < <(awk '
            /^go [0-9]/        { print NR "\tdirective\t" $2 }
            /^toolchain go[0-9]/ { v = $2; sub(/^go/, "", v); print NR "\ttoolchain\t" v }
        ' "$mod")
    done < <(tracked '*go.mod' ':(exclude)**/testdata/**')
fi

# --- rust-toolchain.toml --------------------------------------------------
if [ -n "$rust_pin" ]; then
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        while IFS=$'\t' read -r line ver; do
            [ "$ver" = "$rust_pin" ] || report "$f:$line" rust "$ver" "$rust_pin" "mise.toml rust"
        done < <(awk -F'"' '/^[[:space:]]*channel[[:space:]]*=/ { print NR "\t" $2 }' "$f")
    done < <(tracked '*rust-toolchain.toml' '*rust-toolchain')
fi

# --- uv.lock (ruff) -------------------------------------------------------
if [ -n "$ruff_pin" ]; then
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        while IFS=$'\t' read -r line ver; do
            [ "$ver" = "$ruff_pin" ] || report "$f:$line" ruff "$ver" "$ruff_pin" "mise.toml ruff"
        done < <(awk -F'"' '
            /^name = "ruff"$/ { want = 1; next }
            want && /^version = / { print NR "\t" $2; want = 0 }
            /^\[\[package\]\]/ { want = 0 }
        ' "$f")
    done < <(tracked '*uv.lock')
fi

# --- workflows and local actions ------------------------------------------
composer=sdk/experimental/php/composer.json
php_floor=""
if [ -f "$composer" ]; then
    php_floor=$(awk -F'"' '$2 == "php" { print $4; exit }' "$composer" |
        sed -E 's/^[^0-9]*//; s/^([0-9]+\.[0-9]+).*/\1/')
fi

workflow_files=$(tracked '.github/workflows/*.yml' '.github/workflows/*.yaml' \
    '.github/actions/*/action.yml' '.github/actions/*/action.yaml')
if [ -n "$workflow_files" ]; then
    # shellcheck disable=SC2086 # workflow_files is a newline-separated path list without spaces.
    while IFS=$'\t' read -r where tool ver; do
        case "$tool" in
            php)
                if [ -z "$php_floor" ]; then
                    printf '%s: php-version %s has no require.php in %s to agree with\n' "$where" "$ver" "$composer"
                    findings=$((findings + 1))
                elif [ "$ver" != "$php_floor" ]; then
                    report "$where" php "$ver" "$php_floor" "$composer require.php"
                fi
                ;;
            *)
                want=$(pin "$tool")
                [ "${ver#v}" = "$want" ] || report "$where" "$tool" "$ver" "${want:-<unpinned>}" "mise.toml $tool"
                ;;
        esac
    done < <(awk -v q="'" '
        function trim(s) { sub(/^[[:space:]]+/, "", s); sub(/[[:space:]]+$/, "", s); return s }
        FNR == 1 { uses = "" }
        /^[[:space:]]*#/ { next }
        {
            line = $0
            # A new sequence item (a new step) forgets the previous action.
            if (line ~ /^[[:space:]]*-[[:space:]]/) uses = ""
            if (match(line, /uses:[[:space:]]*[^[:space:]#]+/)) {
                uses = substr(line, RSTART, RLENGTH)
                sub(/^uses:[[:space:]]*/, "", uses)
                sub(/@.*/, "", uses)
            }
            if (!match(line, /^[[:space:]]*(-[[:space:]]+)?(node-version|python-version|go-version|php-version|toolchain|rust-toolchain|lycheeVersion|version):/)) next
            if (index(line, "toolchain-parity: allow")) next
            key = substr(line, RSTART, RLENGTH)
            sub(/^[[:space:]]*(-[[:space:]]+)?/, "", key)
            sub(/:$/, "", key)
            val = substr(line, RSTART + RLENGTH)
            sub(/[[:space:]]+#.*$/, "", val)
            val = trim(val)
            if (val ~ /^".*"$/ || (substr(val, 1, 1) == q && substr(val, length(val), 1) == q))
                val = substr(val, 2, length(val) - 2)
            if (val == "" || val ~ /\$\{\{/ || val ~ /^[|>]/) next

            tool = ""
            if (key == "node-version") tool = "node"
            else if (key == "python-version") tool = "python"
            else if (key == "go-version") tool = "go"
            else if (key == "php-version") tool = "php"
            else if (key == "toolchain" || key == "rust-toolchain") tool = "rust"
            else if (key == "lycheeVersion") tool = "lychee"
            else if (key == "version") {
                if (uses == "pnpm/action-setup") tool = "pnpm"
                else if (uses == "astral-sh/setup-uv") tool = "uv"
                else if (uses == "bufbuild/buf-action") tool = "buf"
                else if (uses == "golangci/golangci-lint-action") tool = "golangci-lint"
            }
            if (tool != "") print FILENAME ":" FNR "\t" tool "\t" val
        }
    ' $workflow_files)
fi

# --- devcontainer ---------------------------------------------------------
flake=.devcontainer/flake.nix
dockerfile=.devcontainer/Dockerfile
if [ -n "$go_pin" ] && [ -n "$node_pin" ] && [ -n "$python_pin" ]; then
    go_mm=$(major_minor "$go_pin")
    node_major=${node_pin%%.*}
    py_mm=$(major_minor "$python_pin")
    if [ -f "$flake" ]; then
        while IFS=$'\t' read -r line tool ver; do
            case "$tool" in
                go) [ "$ver" = "$go_mm" ] || report "$flake:$line" go "$ver" "$go_mm" "mise.toml go (minor)" ;;
                node) [ "$ver" = "$node_major" ] || report "$flake:$line" node "$ver" "$node_major" "mise.toml node (major)" ;;
                python) [ "$ver" = "$py_mm" ] || report "$flake:$line" python "$ver" "$py_mm" "mise.toml python (minor)" ;;
            esac
        done < <(awk '
            /^[[:space:]]*#/ { next }
            match($0, /go_1_[0-9]+/) {
                s = substr($0, RSTART, RLENGTH); sub(/^go_1_/, "", s); print NR "\tgo\t1." s
            }
            match($0, /nodejs_[0-9]+/) {
                s = substr($0, RSTART, RLENGTH); sub(/^nodejs_/, "", s); print NR "\tnode\t" s
            }
            match($0, /python3[0-9][0-9]+/) {
                s = substr($0, RSTART, RLENGTH); sub(/^python3/, "", s); print NR "\tpython\t3." s
            }
        ' "$flake")
    fi
    if [ -f "$dockerfile" ]; then
        while IFS=$'\t' read -r line tool ver; do
            case "$tool" in
                go) [ "$ver" = "$go_mm" ] || report "$dockerfile:$line" go "$ver" "$go_mm" "mise.toml go (minor)" ;;
                node) [ "$ver" = "$node_major" ] || report "$dockerfile:$line" node "$ver" "$node_major" "mise.toml node (major)" ;;
                python) [ "$ver" = "$py_mm" ] || report "$dockerfile:$line" python "$ver" "$py_mm" "mise.toml python (minor)" ;;
            esac
        done < <(awk '
            match($0, /-go-[0-9]+\.[0-9]+/) {
                s = substr($0, RSTART, RLENGTH); sub(/^-go-/, "", s); print NR "\tgo\t" s
            }
            match($0, /-nodejs-[0-9]+/) {
                s = substr($0, RSTART, RLENGTH); sub(/^-nodejs-/, "", s); print NR "\tnode\t" s
            }
            match($0, /-python3-[0-9]+\.[0-9]+/) {
                s = substr($0, RSTART, RLENGTH); sub(/^-python3-/, "", s); print NR "\tpython\t" s
            }
        ' "$dockerfile")
    fi
fi

if [ "$findings" -gt 0 ]; then
    echo "" >&2
    echo "error: $findings toolchain declaration(s) disagree with mise.toml." >&2
    echo "       mise.toml is the source of truth: change the pin there, then make" >&2
    echo "       each file above agree (or mark a deliberate difference with" >&2
    echo "       'toolchain-parity: allow' and the reason, on the same line)." >&2
    exit 1
fi
echo "Toolchain declarations agree with mise.toml."
