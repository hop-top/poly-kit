#!/usr/bin/env bash
#
# test-scope.sh — the tests a change needs, per suite.
#
# One definition of "affected" for `make test-affected`, the CI test jobs
# and the bats suite that pins it. The changed set is
# scripts/changed-files.sh's (same base, same LINT_* knobs as the lint
# targets), deleted paths included.
#
# Usage:
#   scripts/test-scope.sh [--gate] <suite>
#
# Suites and their units (one per line on stdout):
#
#   go       "<module dir>\t<import path>": in every module `make test-go`
#            covers, the packages a change touches and every package
#            whose build or tests import one of them, transitively. A
#            _test.go file, testdata or a file only a test names affects
#            its own package alone. "<module dir>\tALL" is the whole
#            module (its go.mod or go.sum changed, a package was deleted).
#   go-race  the same under -race, kept to `make test-go-race`'s
#            packages (go/, cmd/, internal/, examples/ of the root module)
#   rs ts py php
#            a package dir: the package when a file under it changed, or
#            a file its sources name by path (scripts/path-refs.awk: a
#            contract vector, a shared fixture, another package)
#   bats     a suite path: a changed suite, a suite naming a changed
#            file, the suites that build Go when Go changed
#   parity   a make target from `test-parity`'s list: its Go half
#            affected (go, with -tags parity) or one of its SDK packages
#
#   --gate   file level only, no toolchain needed: the changed files that
#            make the suite relevant, one per line (empty: skip). CI runs
#            it before installing a toolchain; a gated-in go suite can
#            still come out empty once packages are resolved.
#
# Exit:
#   0  the units (empty: nothing to run)
#   3  run the whole suite: TEST_ALL or LINT_ALL is set, the base is
#      unknown, or a file every suite depends on changed (this script,
#      its awk helpers, test-affected.sh, the Makefile, the mise pins,
#      ci.yml, changed-files.sh) or one the suite's toolchain reads (root
#      go.mod/go.sum; the pnpm lockfile and workspace; uv.toml; for
#      bats, any shell, template, hook or workflow file; for parity, any
#      of those or contracts/). A reason goes to stderr.
#   2  usage error
#   1  a tool failed (go list): callers run the whole suite
#
# Correctness over savings: anything ambiguous widens the scope. Full
# suites still run on pushes and on the nightly schedule, which catch a
# coupling no rule here can see.
set -euo pipefail

FULL=3
GO_MODULE_DIRS=(go cmd contracts engine examples incubator)
GO_RACE_PREFIXES=(go cmd internal examples)
TS_PKGS=(sdk/ts engine/sdk/ts-kit-engine)
PY_PKGS=(sdk/py engine/sdk/py-kit-engine)
RS_PKGS=(sdk/experimental/rs)
PHP_PKGS=(sdk/experimental/php)

# Parity targets: "<target> <go packages, comma-separated, /... for a
# subtree> <SDK package dirs, comma-separated>". A test-parity
# prerequisite missing here always runs.
RS=sdk/experimental/rs TS=sdk/ts PY=sdk/py PHP=sdk/experimental/php
PARITY_TABLE="
test-parity-typeid go/core/id/... $RS,$TS,$PY,$PHP
test-parity-consent go/core/consent $RS
test-parity-authn go/transport/authn $RS
test-parity-breaker go/core/breaker $RS
test-parity-bus go/runtime/bus $RS
test-parity-bus-network sdk/tests/cross-lang/bus-network/gonode $RS
test-parity-config go/core/config $RS
test-parity-retry go/core/util $RS
test-parity-completion go/console/cli/completion $RS
test-parity-completion-spec go/console/cli/completionspec $RS
test-parity-netpolicy go/core/netpolicy $RS
test-parity-llm go/ai/llm,go/ai/llm/anthropic,go/ai/llm/openai $RS,$PY,$TS
test-parity-kv go/storage/kv/sqlite/... $RS
test-parity-mcp go/transport/cmdsurface $RS,$TS,$PY,$PHP
test-parity-taxonomy go/console/output/envelope $RS,$TS,$PY,$PHP
test-parity-statemachine go/runtime/domain $RS
test-parity-redact go/core/redact $RS
test-parity-identity go/core/identity $RS
test-parity-upgrade go/core/upgrade $RS
test-parity-cli go/console/cli/... $TS,$PY
test-parity-engine engine/sdk/parity/... engine/sdk/ts-kit-engine,engine/sdk/py-kit-engine
test-parity-alias go/console/alias,go/console/cli $RS,$TS,$PY
"

SELF=(scripts/test-scope.sh scripts/path-refs.awk scripts/go-affected.awk
    scripts/test-affected.sh Makefile mise.toml .config/mise.toml
    .github/workflows/ci.yml)
GO_TRIGGERS=(go.mod go.sum)
TS_TRIGGERS=(pnpm-lock.yaml pnpm-workspace.yaml package.json)
PY_TRIGGERS=(uv.toml)
BATS_TRIGGERS=('**/*.sh' '**/*.bash' 'templates/**' 'internal/template/builtins/**'
    '.githooks/**' '.github/workflows/**' 'scripts/**')

gate=0
while [ $# -gt 0 ]; do
    case "$1" in
        --gate) gate=1; shift ;;
        -h|--help) sed -n '2,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p;}' "$0"; exit 0 ;;
        -*) echo "test-scope: unknown option: $1" >&2; exit 2 ;;
        *) break ;;
    esac
done
[ $# -eq 1 ] || { echo "usage: test-scope.sh [--gate] go|go-race|rs|ts|py|php|bats|parity" >&2; exit 2; }
suite=$1
case "$suite" in
    go|go-race|rs|ts|py|php|bats|parity) ;;
    *) echo "test-scope: unknown suite: $suite" >&2; exit 2 ;;
esac

ROOT=$(git rev-parse --show-toplevel)
cd "$ROOT"
ROOT_MOD=$(awk '/^module /{print $2; exit}' go.mod)
scripts=$ROOT/scripts

full() {
    echo "test-scope: $suite: whole suite: $*" >&2
    exit "$FULL"
}

case "${TEST_ALL:-}" in
    ''|0|false|no) ;;
    *) full "TEST_ALL=$TEST_ALL" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# The repo's top-level entries: a rooted path must start with one.
ROOTS=$(git ls-files | sed 's|/.*||' | sort -u | tr '\n' ' ')

triggers=()
case "$suite" in
    go|go-race) triggers=("${GO_TRIGGERS[@]}") ;;
    ts) triggers=("${TS_TRIGGERS[@]}") ;;
    py) triggers=("${PY_TRIGGERS[@]}") ;;
    bats) for t in "${BATS_TRIGGERS[@]}"; do triggers+=(":(glob)$t"); done ;;
    parity) triggers=("${GO_TRIGGERS[@]}" "${TS_TRIGGERS[@]}" "${PY_TRIGGERS[@]}" ':(glob)contracts/**') ;;
esac
args=(--include-deleted)
for t in "${SELF[@]}" ${triggers[@]+"${triggers[@]}"}; do args+=(--trigger "$t"); done

rc=0
"$scripts/changed-files.sh" "${args[@]}" >"$tmp/changed" 2>"$tmp/why" || rc=$?
case "$rc" in
    0) ;;
    3) full "$(sed 's/^changed-files: linting everything: //' "$tmp/why")" ;;
    *) cat "$tmp/why" >&2; exit "$rc" ;;
esac
[ -s "$tmp/changed" ] || exit 0

# changed with existence: "E\t<path>" or "D\t<path>" (deleted).
while IFS= read -r f; do
    if [ -e "$f" ]; then printf 'E\t%s\n' "$f"; else printf 'D\t%s\n' "$f"; fi
done <"$tmp/changed" >"$tmp/changed-st"

# refs <out>: path-refs.awk over the files listed on stdin.
refs() {
    local out=$1
    cat >"$out.files"
    : >"$out"
    [ -s "$out.files" ] || return 0
    tr '\n' '\0' <"$out.files" \
        | xargs -0 awk -v topdirs=contracts -v roots="$ROOTS" -f "$scripts/path-refs.awk" >"$out" 2>/dev/null || true
}

# ref_hits <refs>: the changed files a refs index names (prefix match;
# a bare "contracts" names the whole contracts tree).
ref_hits() {
    awk -F'\t' '
        NR == FNR { ch[$0] = 1; next }
        {
            for (f in ch)
                if (f == $1 || index(f, $1 "/") == 1 || ($1 == "contracts" && f ~ /^contracts\//))
                    hit[f] = 1
        }
        END { for (f in hit) print f }
    ' "$tmp/changed" "$1" | sort
}

# pkg_hits <dir> <git pathspec...>: changed files under <dir>, or named by
# the package's sources matching the pathspecs.
pkg_hits() {
    local dir=$1; shift
    {
        grep -E "^$(printf '%s' "$dir" | sed 's/[.]/\\./g')/" "$tmp/changed" || true
        git ls-files -- "$@" | refs "$tmp/refs.$$"
        ref_hits "$tmp/refs.$$"
    } | sort -u
}

pkg_globs() {
    case "$1" in
        ts) echo '*.ts *.tsx *.mts *.cts *.js *.mjs *.cjs *.json' ;;
        py) echo '*.py *.toml *.cfg *.ini' ;;
        rs) echo '*.rs *.toml' ;;
        php) echo '*.php *.json *.xml *.dist *.neon' ;;
    esac
}

# pkg_scope <lang>: the affected package dirs of a language (with --gate:
# the files that affect them).
pkg_scope() {
    local lang=$1 p hits pkgs globs g
    case "$lang" in
        ts) pkgs=("${TS_PKGS[@]}") ;;
        py) pkgs=("${PY_PKGS[@]}") ;;
        rs) pkgs=("${RS_PKGS[@]}") ;;
        php) pkgs=("${PHP_PKGS[@]}") ;;
    esac
    read -r -a globs <<<"$(pkg_globs "$lang")"
    for p in "${pkgs[@]}"; do
        g=()
        for x in "${globs[@]}"; do g+=(":(glob)$p/**/$x"); done
        hits=$(pkg_hits "$p" "${g[@]}")
        [ -n "$hits" ] || continue
        if [ "$gate" -eq 1 ]; then printf '%s\n' "$hits"; else printf '%s\n' "$p"; fi
    done | sort -u
}

# go_gate: changed files that can matter to a Go package: Go sources and
# module files, files in or under a directory holding Go sources, files
# any Go source names.
go_gate() {
    {
        grep -E '(\.go|(^|/)go\.(mod|sum))$' "$tmp/changed" || true
        git ls-files -- '*.go' | sed 's|/[^/]*$||' | sort -u >"$tmp/godirs"
        awk 'NR == FNR { d[$0] = 1; next }
            { p = $0; while (sub(/\/[^\/]*$/, "", p)) if (p in d) { print $0; break } }' \
            "$tmp/godirs" "$tmp/changed"
        git ls-files -- '*.go' | refs "$tmp/refs-go"
        ref_hits "$tmp/refs-go"
    } | sort -u
}

go_modules() {
    echo .
    find "${GO_MODULE_DIRS[@]}" -name go.mod -not -path '*/node_modules/*' 2>/dev/null \
        | sed 's|/go.mod$||' | sort
}

GO_LIST_FMT=$(printf '%s\t' '{{.ImportPath}}' '{{.ForTest}}' '{{.Dir}}' '{{join .Deps " "}}' \
    '{{join .GoFiles " "}} {{join .CgoFiles " "}}' \
    '{{join .TestGoFiles " "}} {{join .XTestGoFiles " "}}' \
    '{{join .EmbedFiles " "}}' \
    '{{join .TestEmbedFiles " "}} {{join .XTestEmbedFiles " "}}')
GO_LIST_FMT="${GO_LIST_FMT}{{if .Error}}error{{end}}"

# go_scope <flags...>: "<module>\t<import path|ALL>" for every module.
go_scope() {
    local m flags=("$@")
    git ls-files -- go.mod '**/go.mod' | while IFS= read -r f; do
        d=$(dirname "$f")
        printf '%s\t%s\n' "$d" "$(awk '/^module /{print $2; exit}' "$f")"
    done >"$tmp/modules"
    for m in $(go_modules); do
        if ! (cd "$m" && go list -e -test -buildvcs=false ${flags[@]+"${flags[@]}"} \
            -f "$GO_LIST_FMT" ./...) >"$tmp/list" 2>"$tmp/list.err"; then
            echo "test-scope: $suite: go list failed in $m:" >&2
            cat "$tmp/list.err" >&2
            exit 1
        fi
        awk -F'\t' -v root="$ROOT" '
            $2 == "" && $1 !~ /\.test$/ {
                d = $3
                if (index(d, root "/") == 1) d = substr(d, length(root) + 2); else d = ""
                n = split($5 " " $6, a, " ")
                for (i = 1; i <= n; i++) print (d == "" ? "" : d "/") a[i]
            }' "$tmp/list" | refs "$tmp/refs-go"
        awk -v mod="$m" -v root="$ROOT" -f "$scripts/go-affected.awk" \
            "$tmp/modules" "$tmp/list" "$tmp/refs-go" "$tmp/changed-st"
    done
}

go_suite() {
    local race=$1 out p keep
    if [ "$gate" -eq 1 ]; then
        go_gate
        return
    fi
    [ -n "$(go_gate)" ] || return 0
    if [ "$race" -eq 1 ]; then
        out=$(go_scope -race)
        if printf '%s\n' "$out" | awk -F'\t' '$1 == "." && $2 == "ALL" { f = 1 } END { exit !f }'; then
            for p in "${GO_RACE_PREFIXES[@]}"; do printf '.\t./%s/...\n' "$p"; done
            return
        fi
        keep=$(printf '%s\n' "${GO_RACE_PREFIXES[@]}" | sed "s|.*|$ROOT_MOD/&/|" | paste -sd'|' -)
        printf '%s\n' "$out" | awk -F'\t' -v keep="$keep" '
            BEGIN { n = split(keep, k, "|") }
            $1 == "." { for (i = 1; i <= n; i++) if (index($2 "/", k[i]) == 1) { print; break } }' | sort
    else
        go_scope | sort
    fi
}

bats_suites() {
    git ls-files --cached --others --exclude-standard -- '*.bats' \
        ':(exclude)internal/template/builtins/**' | sort -u
}

bats_scope() {
    local hits
    bats_suites >"$tmp/bats-all"
    {
        grep -Fx -f "$tmp/bats-all" "$tmp/changed" || true
        refs "$tmp/refs-bats" <"$tmp/bats-all"
        ref_hits "$tmp/refs-bats" >"$tmp/bats-named"
        awk -F'\t' 'NR == FNR { h[$0] = 1; next }
            { for (f in h) if (f == $1 || index(f, $1 "/") == 1) { print $2; break } }' \
            "$tmp/bats-named" "$tmp/refs-bats"
        # Suites that build kit: a non-test Go file or a module file.
        if grep -E '(\.go|(^|/)go\.(mod|sum))$' "$tmp/changed" | grep -qv '_test\.go$'; then
            tr '\n' '\0' <"$tmp/bats-all" | xargs -0 grep -lE '\bgo (build|run|install|test)\b' || true
        fi
    } | sort -u >"$tmp/bats-scope"
    hits=$(cat "$tmp/bats-scope")
    [ -n "$hits" ] || return 0
    if [ "$gate" -eq 1 ]; then cat "$tmp/changed"; else printf '%s\n' "$hits"; fi
}

parity_targets() {
    awk '/^test-parity:/ {
            sub(/^test-parity:[ \t]*/, ""); sub(/[ \t]*##.*/, "")
            n = split($0, a, " "); for (i = 1; i <= n; i++) print a[i]
        }' Makefile
}

parity_scope() {
    local go_out sdk_out t row gopk sdks hit
    if [ "$gate" -eq 1 ]; then
        { go_gate; for l in rs ts py php; do pkg_scope "$l"; done; } | sort -u
        return
    fi
    go_gate >"$tmp/parity-go-gate"
    go_out=""
    if [ -s "$tmp/parity-go-gate" ]; then
        go_out=$(go_scope -tags parity | awk -F'\t' '$1 == "." { print $2 }')
    fi
    sdk_out=$(for l in rs ts py php; do pkg_scope "$l"; done)
    for t in $(parity_targets); do
        row=$(printf '%s\n' "$PARITY_TABLE" | awk -v t="$t" '$1 == t')
        if [ -z "$row" ]; then
            echo "$t"
            continue
        fi
        read -r _ gopk sdks <<<"$row"
        hit=$(printf '%s\n' "$go_out" | awk -v pk="$gopk" -v mp="$ROOT_MOD" '
            BEGIN { n = split(pk, a, ",") }
            $0 == "ALL" { print; exit }
            {
                for (i = 1; i <= n; i++) {
                    p = mp "/" a[i]
                    if (p ~ /\/\.\.\.$/) { sub(/\/\.\.\.$/, "", p); if ($0 == p || index($0, p "/") == 1) { print; exit } }
                    else if ($0 == p) { print; exit }
                }
            }')
        if [ -z "$hit" ]; then
            hit=$(printf '%s\n' "$sdk_out" | grep -Fx -f <(printf '%s\n' "${sdks//,/$'\n'}") || true)
        fi
        [ -z "$hit" ] || echo "$t"
    done
}

case "$suite" in
    go) go_suite 0 ;;
    go-race) go_suite 1 ;;
    rs|ts|py|php) pkg_scope "$suite" ;;
    bats) bats_scope ;;
    parity) parity_scope ;;
esac
