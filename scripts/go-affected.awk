# go-affected.awk — the packages of one Go module a change affects, for
# scripts/test-scope.sh.
#
# Inputs, in order:
#   1. modules   "<dir>\t<module path>", every go.mod in the repo
#   2. listing   `go list -e -test` rows of the module (format in
#                test-scope.sh): import path, ForTest, dir, deps, files
#   3. refs      "<path>\t<source file>" from path-refs.awk over the
#                listing's compiled files
#   4. changed   "<E|D>\t<path>": exists, or deleted
# Variables: mod (module dir, "." for the root), root (absolute repo
# root, to make listing dirs relative).
#
# Output: one "<mod>\t<import path>" per affected package, or the single
# line "<mod>\tALL" when the whole module must run.
#
# A change seeds packages two ways:
#   prop  the package's build changed (a non-test .go file, a file it
#         embeds): it and every package whose build or test imports it,
#         transitively (go list -test Deps), are affected
#   self  only its tests changed (a _test.go file, testdata, a file a
#         test names): the package alone
# The whole module runs when its go.mod or go.sum changed or one of its
# packages was deleted. A package go list reports an error for always
# runs, so a broken package is never skipped.

BEGIN { FS = "\t" }

FILENAME == ARGV[1] {
    modpath[$1] = $2
    next
}

FILENAME == ARGV[2] {
    ip = $1
    dir = $3
    if (index(dir, root "/") == 1) dir = substr(dir, length(root) + 2)
    else if (dir == root) dir = "."
    if ($2 != "") key = $2
    else if (ip ~ /\.test$/) key = substr(ip, 1, length(ip) - 5)
    else key = ip
    deps[key] = deps[key] " " $4
    if ($2 == "" && ip !~ /\.test$/ && ip !~ / \[/) {
        pkg[key] = 1
        pkgdir[dir] = key
        addfiles(dir, $5, key, 0, 0)
        addfiles(dir, $6, key, 1, 0)
        addfiles(dir, $7, key, 0, 1)
        addfiles(dir, $8, key, 1, 1)
        if ($9 != "") broken[key] = 1
    }
    next
}

FILENAME == ARGV[3] {
    nref++
    rcand[nref] = $1
    rsrc[nref] = $2
    next
}

{
    nch++
    chst[nch] = $1
    chf[nch] = $2
}

function addfiles(dir, list, key, istest, isembed,    n, a, i, p) {
    n = split(list, a, " ")
    for (i = 1; i <= n; i++) {
        p = (dir == "." ? "" : dir "/") a[i]
        if (isembed) {
            if (istest) tembed[p] = key
            else embed[p] = key
        } else {
            file2pkg[p] = key
            ftest[p] = istest
        }
    }
}

function dirname(p) {
    if (sub(/\/[^\/]*$/, "", p) == 0) return "."
    return p
}

# owner: the module dir holding p (longest go.mod dir prefix).
function owner(p,    d, best) {
    best = "."
    for (d in modpath)
        if (d != "." && index(p, d "/") == 1 && length(d) > length(best)) best = d
    return best
}

function importpath(d, o,    rel) {
    if (o == ".") rel = d
    else rel = substr(d, length(o) + 2)
    if (rel == "." || rel == "") return modpath[o]
    return modpath[o] "/" rel
}

function seedfile(p,    k) {
    k = file2pkg[p]
    if (ftest[p]) self[k] = 1
    else prop[k] = 1
}

END {
    whole = 0
    mfile = (mod == "." ? "" : mod "/")
    for (i = 1; i <= nch; i++) {
        f = chf[i]
        st = chst[i]
        o = owner(f)
        d = dirname(f)
        base = f
        sub(/.*\//, "", base)

        if (base == "go.mod" || base == "go.sum") {
            if (f == mfile "go.mod" || f == mfile "go.sum") whole = 1
            else if (dirname(f) in modpath) prefprop[modpath[dirname(f)]] = 1
            continue
        }

        if (f in file2pkg) {
            seedfile(f)
            continue
        }

        if (f ~ /\.go$/) {
            if (d in pkgdir) {
                # Not compiled under these build tags, unless deleted.
                if (st == "D") {
                    if (f ~ /_test\.go$/) self[pkgdir[d]] = 1
                    else prop[pkgdir[d]] = 1
                }
                continue
            }
            if (st == "D" && o == mod) {
                whole = 1
                continue
            }
            # A package outside the listed patterns or in another module
            # (a replace target), or Go source kept as test data.
            prop[importpath(d, o)] = 1
            for (a = d; a != "." && !(a in pkgdir); a = dirname(a)) ;
            if (a in pkgdir) self[pkgdir[a]] = 1
            continue
        }

        if (f in embed) prop[embed[f]] = 1
        if (f in tembed) self[tembed[f]] = 1
        if (o != mod) {
            prefprop[modpath[o]] = 1
            continue
        }
        for (a = d; a != "." && !(a in pkgdir); a = dirname(a)) ;
        if (a in pkgdir) self[pkgdir[a]] = 1
    }

    for (r = 1; r <= nref; r++) {
        c = rcand[r]
        for (i = 1; i <= nch; i++) {
            f = chf[i]
            if (f == c || index(f, c "/") == 1 || (c == "contracts" && f ~ /^contracts\//)) {
                if (rsrc[r] in file2pkg) seedfile(rsrc[r])
                break
            }
        }
    }

    if (whole) {
        print mod "\tALL"
        exit 0
    }

    for (k in pkg) {
        hit = (k in self) || (k in prop) || (k in broken)
        if (!hit) {
            n = split(deps[k], ds, " ")
            for (j = 1; j <= n && !hit; j++) {
                dep = ds[j]
                if (dep == "" || dep ~ /^\[/) continue
                if (dep in prop) hit = 1
                else
                    for (p in prefprop)
                        if (dep == p || index(dep, p "/") == 1) { hit = 1; break }
            }
        }
        if (hit) print mod "\t" k
    }
}
