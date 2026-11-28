#!/bin/sh
# detect-port.sh - report which kit port(s) a project directory already uses.
#
# Usage: detect-port.sh [dir]        dir defaults to the current directory.
#
# Reads manifest files directly inside dir (no recursion). Writes nothing,
# touches no network, runs no package manager.
#
# Manifests, in the order they are reported:
#   go.mod            module hop.top/kit
#   package.json      @hop-top/kit
#   pyproject.toml    hop-top-kit
#   requirements*.txt hop-top-kit
#   uv.lock           hop-top-kit
#   Cargo.toml        hop-top-kit
#   composer.json     hop-top/kit
#
# Output: one line per manifest found, fields separated by single spaces,
# key=value, no value contains a space:
#   port=<go|ts|py|rs|php> manifest=<file> kit=<spec> skill=<name> url=<url>
# kit=<spec> is the version or range as the manifest writes it (spaces
# removed), "unpinned" when kit is listed without one, "absent" when the
# manifest does not list kit, "unreadable" when the file cannot be read.
# url is the port skill's SKILL.md. A directory with no manifest prints
# nothing on stdout and one note on stderr.
#
# Exit codes:
#   0  dir is readable (whether or not any manifest was found)
#   2  usage error, or dir is not a readable directory

LC_ALL=C
export LC_ALL

usage() {
    echo "usage: detect-port.sh [dir]" >&2
    exit 2
}

case ${1-} in
-h | --help)
    echo "usage: detect-port.sh [dir]"
    exit 0
    ;;
-*) usage ;;
esac
[ $# -le 1 ] || usage
dir=${1:-.}
if [ ! -d "$dir" ] || [ ! -r "$dir" ] || [ ! -x "$dir" ]; then
    echo "detect-port: not a readable directory: $dir" >&2
    exit 2
fi

found=0

# report <port> <file> <spec>
report() {
    case $1 in
    go) skill=hop-top-kit-go mirror=kit ;;
    ts) skill=hop-top-kit-ts mirror=kit-ts ;;
    py) skill=hop-top-kit-py mirror=kit-py ;;
    rs) skill=hop-top-kit-rs mirror=kit-rs ;;
    php) skill=hop-top-kit-php mirror=kit-php ;;
    esac
    printf 'port=%s manifest=%s kit=%s skill=%s url=https://github.com/hop-top/%s/blob/main/skills/%s/SKILL.md\n' \
        "$1" "$2" "$3" "$skill" "$mirror" "$skill"
    found=1
}

# check <port> <name> <extractor> [path]: report when the manifest exists.
check() {
    path=${4:-$dir/$2}
    [ -f "$path" ] || return 0
    if [ -r "$path" ]; then
        spec=$(extract "$3" "$path" | tr -d ' \t')
    else
        spec=unreadable
    fi
    report "$1" "$2" "${spec:-unpinned}"
}

# Extractors print the spec, "absent", or nothing for an unpinned entry.

extract() {
    case $1 in
    gomod) ex_gomod "$2" ;;
    package) ex_package "$2" ;;
    python) ex_python "$2" ;;
    uvlock) ex_uvlock "$2" ;;
    cargo) ex_cargo "$2" ;;
    composer) ex_composer "$2" ;;
    esac
}

# json_dep <key> <file>: the value of "<key>": "<value>".
json_dep() {
    v=$(sed -n 's|.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*|\1|p' "$2" | head -n 1)
    [ -n "$v" ] && echo "$v" || echo absent
}
ex_package() { json_dep '@hop-top/kit' "$1"; }
ex_composer() { json_dep 'hop-top/kit' "$1"; }

ex_gomod() {
    awk '
    $1 == "require" && $2 == "hop.top/kit" && $3 ~ /^v[0-9]/ { print $3; f = 1; exit }
    $1 == "hop.top/kit" && $2 ~ /^v[0-9]/ { print $2; f = 1; exit }
    END { if (!f) print "absent" }' "$1"
}

# Python: requirement lines and TOML dependency entries, name normalised.
ex_python() {
    awk '
    /^[ \t]*#/ || /^[ \t]*name[ \t]*=/ { next }
    {
        low = tolower($0); gsub(/[_.]/, "-", low)
        for (off = 0; (i = index(substr(low, off + 1), "hop-top-kit")) > 0; off += i) {
            p = off + i
            if (p > 1 && substr(low, p - 1, 1) ~ /[a-z0-9-]/) continue
            if (substr(low, p + 11, 1) ~ /[a-z0-9-]/) continue
            f = 1; rest = substr($0, p + 11)
            sub(/^\[[^]]*\]/, "", rest)
            if (match(rest, /^[ \t]*=[ \t]*["'"'"'][^"'"'"']*["'"'"']/)) {
                v = substr(rest, RSTART, RLENGTH); sub(/^[ \t]*=[ \t]*["'"'"']/, "", v); sub(/["'"'"']$/, "", v)
            } else if (match(rest, /^[ \t]*[<>=!~]+[ \t]*[^"'"'"';# \t,]*([ \t]*,[ \t]*[<>=!~]+[ \t]*[^"'"'"';# \t,]*)*/)) {
                v = substr(rest, RSTART, RLENGTH); sub(/^[ \t]+/, "", v)
            }
            print v; exit
        }
    }
    END { if (!f) print "absent" }' "$1"
}

ex_uvlock() {
    awk '
    tolower($0) ~ /^name[ \t]*=[ \t]*"hop[-_.]top[-_.]kit"/ { hit = 1; next }
    hit && /^version[ \t]*=/ { v = $0; sub(/^[^"]*"/, "", v); sub(/".*/, "", v); print v; f = 1; exit }
    /^\[\[package\]\]/ { hit = 0 }
    END { if (!f) print (hit ? "" : "absent") }' "$1"
}

ex_cargo() {
    awk '
    /^[ \t]*#/ { next }
    /^\[/ { tbl = ($0 ~ /dependencies\.hop-top-kit[ \t]*\]/); if (tbl) f = 1; next }
    tbl && /^version[ \t]*=/ { v = $0; sub(/^[^"]*"/, "", v); sub(/".*/, "", v); print v; exit }
    /^hop-top-kit[ \t]*=/ {
        f = 1; v = $0; sub(/^[^=]*=[ \t]*/, "", v)
        if (v ~ /^"/) { sub(/^"/, "", v); sub(/".*/, "", v) }
        else if (match(v, /version[ \t]*=[ \t]*"[^"]*"/)) {
            v = substr(v, RSTART, RLENGTH); sub(/^[^"]*"/, "", v); sub(/"$/, "", v)
        } else v = ""
        print v; exit
    }
    END { if (!f) print "absent" }' "$1"
}

check go go.mod gomod
check ts package.json package
check py pyproject.toml python
for req in "$dir"/requirements*.txt; do
    [ -f "$req" ] || continue
    check py "${req##*/}" python "$req"
done
check py uv.lock uvlock
check rs Cargo.toml cargo
check php composer.json composer

[ "$found" -eq 1 ] || echo "detect-port: no kit-relevant manifest in $dir" >&2
exit 0
