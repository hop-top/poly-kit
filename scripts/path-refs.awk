# path-refs.awk — the repo paths a source file names, for
# scripts/test-scope.sh: a test that reads a file outside its own package
# (a contract vector, a fixture, a doc it checks) is affected when that
# file changes.
#
# Input: source files, as repo-relative paths (run from the repo root).
# Output: one "<path>\t<source file>" line per path a file names.
# Variables: roots="a b c", the repo's top-level entries (space-separated);
# topdirs="contracts", the top-level directories a bare "dir/" may name.
#
# A path is named as
#   - a path-shaped token anywhere in the code, quoted or not
#     ("../../contracts/bus-v1", '/contracts/typeid-v1/x' appended to a
#     root, "contracts/llm-v1/vectors.json: not found" in a message)
#   - a run of quoted components joined by commas, slashes or chained
#     join calls (filepath.Join(root, "examples", "spaced"),
#     ROOT / "contracts" / "typeid-v1", .join("contracts").join("x"))
#   - one quoted component joined onto a variable named *root*
#     (filepath.Join(repoRoot, "README.md"))
#   - a quoted "dir/" for a directory in topdirs ("contracts/" + name)
# Each yields up to two candidates:
#   relative  resolved against the source file's directory, when the path
#             climbs out of it with ../
#   rooted    leading ./, ../ and / dropped: a path joined onto the repo
#             root. Kept when its first component is a top-level entry.
#
# A path is cut at its first component holding a glob, format verb or
# template character ("contracts/%s/x" names contracts). A candidate
# that is a directory holding the source file itself is dropped: "../.."
# from a manifest dir is an anchor a later join extends. Matching is by
# prefix, so a named directory covers every file under it.
#
# Whole-line comments (//, #, /* and * lines) are skipped: they name
# paths in prose. Otherwise this over-reports on purpose: a spurious
# path costs one extra package run, a missed one a test that should have
# run.

BEGIN {
    n = split(roots, a, " ")
    for (i = 1; i <= n; i++) root[a[i]] = 1
    n = split(topdirs, a, " ")
    for (i = 1; i <= n; i++) top[a[i]] = 1
    TOK = "[A-Za-z0-9_.@-]+"
    Q = "[\"']"
    SEP = "[ \t]*(,|/|\\)[ \t]*\\.[A-Za-z_]+\\()[ \t]*"
}

FNR == 1 {
    if (cur != "") scan(cur, buf)
    cur = FILENAME
    buf = ""
}

/^[ \t]*(\/\/|#|\/?\*)/ { next }

{ buf = buf " " $0 }

END {
    if (cur != "") scan(cur, buf)
}

function scan(file, s,    dir, rest, lit, re) {
    dir = file
    if (sub(/\/[^\/]*$/, "", dir) == 0) dir = ""

    # Path-shaped tokens, quoted or bare.
    rest = s
    re = "/?" TOK "(/" TOK ")+/?"
    while (match(rest, re)) {
        lit = substr(rest, RSTART, RLENGTH)
        rest = substr(rest, RSTART + RLENGTH)
        emit(file, dir, lit, 0)
    }

    # Quoted strings holding a slash: globs and templates cut, "dir/".
    rest = s
    while (match(rest, /["'`][^"'` \t]*\/[^"'` \t]*["'`]/)) {
        lit = substr(rest, RSTART + 1, RLENGTH - 2)
        rest = substr(rest, RSTART + RLENGTH)
        emit(file, dir, lit, 0)
    }

    # Quoted components joined into a path.
    rest = s
    re = Q TOK Q "(" SEP Q TOK Q ")+"
    while (match(rest, re)) {
        lit = substr(rest, RSTART, RLENGTH)
        rest = substr(rest, RSTART + RLENGTH)
        gsub(/\)[ \t]*\.[A-Za-z_]+\(/, ",", lit)
        gsub(/["' \t]/, "", lit)
        gsub(/,/, "/", lit)
        emit(file, dir, lit, 0)
    }

    # One component joined onto a *root* variable (more components make
    # it a joined path, above).
    rest = s
    re = "[A-Za-z_]*(root|Root|ROOT)[A-Za-z_]*[ \t]*\\)?" SEP Q TOK Q
    while (match(rest, re)) {
        lit = substr(rest, RSTART, RLENGTH)
        rest = substr(rest, RSTART + RLENGTH)
        if (match(rest, "^" SEP Q TOK Q)) continue
        if (match(lit, Q TOK Q "$")) emit(file, dir, substr(lit, RSTART + 1, RLENGTH - 2), 1)
    }
}

function emit(file, dir, lit, single,    n, parts, i, p, out, dirlike, c) {
    if (lit ~ /:\/\//) return
    dirlike = (lit ~ /\/$/)
    n = split(lit, parts, "/")
    out = ""
    for (i = 1; i <= n; i++) {
        p = parts[i]
        if (p ~ /[*?\[{}%$<>()|\\:=]/) break
        if (p == "") continue
        out = out (out == "" ? "" : "/") p
    }
    if (out == "") return
    if (i <= n) dirlike = 1

    if (out ~ /^\.\.\// && lit !~ /^\//) {
        c = norm(dir == "" ? out : dir "/" out)
        if (named(c, file)) print c "\t" file
    }

    c = out
    while (c ~ /^\.\.?\//) sub(/^\.\.?\//, "", c)
    c = norm(c)
    if (!named(c, file)) return
    p = c
    sub(/\/.*/, "", p)
    if (!(p in root)) return
    if (c ~ /\// || single || (dirlike && (c in top))) print c "\t" file
}

# named: c is a path the file can be reading, not the repo root, a climb
# above it, or a directory holding the file itself.
function named(c, file) {
    if (c == "" || c == "." || c ~ /^\.\.(\/|$)/) return 0
    return index(file, c "/") != 1
}

# norm: collapse "." and ".." components; ".." when the path climbs
# above where it started.
function norm(p,    n, parts, i, k, st, out) {
    n = split(p, parts, "/")
    k = 0
    for (i = 1; i <= n; i++) {
        if (parts[i] == "" || parts[i] == ".") continue
        if (parts[i] == "..") {
            if (k == 0) return ".."
            k--
            continue
        }
        st[++k] = parts[i]
    }
    out = ""
    for (i = 1; i <= k; i++) out = out (i == 1 ? "" : "/") st[i]
    return out
}
