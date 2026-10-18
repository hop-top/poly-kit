# shellcheck shell=bash
#
# release-lib.sh — release-please helpers shared by check-release-bump.sh and
# release-as.sh. Source it; it only defines functions. Needs git, jq, awk.
#
# Where it mirrors release-please, it follows release-please 17 (the line
# googleapis/release-please-action@v5 bundles):
#
#   Commit routing (util/commit-split.js, util/commit-exclude.js). The root
#   package "." sees every commit. Any other package sees a commit when a
#   file the commit touches sits under the package's path, the longest
#   matching package path taking the file. A commit that touches no file
#   reaches every non-root package. A package with exclude-paths then drops
#   a commit when every file of it under the package sits under an excluded
#   path; a commit that touches no file is dropped too.
#
#   Tag names (util/tag-name.js, strategies/base.js).
#   <component><tag-separator>v<version>; include-component-in-tag defaults
#   to true, tag-separator to "-", include-v-in-tag to true. A package's own
#   setting wins over the top-level one, as for release-as.
#
#   Commit parsing (commit.js on @conventional-commits/parser 0.4). Each
#   BEGIN_NESTED_COMMIT..END_NESTED_COMMIT block is a message of its own, and
#   the rest splits into one message per paragraph that opens with a
#   lowercase conventional type and ": ". A message, trimmed, is a header,
#   a body, then footers: the trailing lines that each open with a token
#   ("BREAKING CHANGE", "BREAKING-CHANGE", "!", or a type with an optional
#   scope and "!") and a ":" or " #" separator, or that indent a footer's
#   continuation. A header that does not parse, or a line that opens a scope
#   ("<type>(") and never closes it, drops the message. A footer with a type
#   is parsed again as a commit of its own, so "Refs!: 12" is a breaking
#   commit. A commit is breaking when the last of its breaking markers has
#   text: "!" in the header (the subject), a body line that opens with
#   "BREAKING CHANGE:" or "BREAKING-CHANGE:" (the rest of the body from
#   there), a BREAKING CHANGE footer (its last line); or when its body holds
#   "BREAKING-CHANGE:" with text anywhere after it. BEGIN_COMMIT_OVERRIDE
#   pull request bodies are not mirrored.
#
# Linked groups are this repo's own: the top-level "x-linked-groups" key of
# the release-please config, which release-please ignores, lists each
# group's components. See RELEASING.md, "Linked releases".

# rp_config_path [rev] — path of the release-please config, read from rev
# (or the working tree when rev is empty). This repo keeps it under .github/;
# scaffolded repos keep it at the root. The manifest sits next to it.
rp_config_path() {
  local rev="${1:-}" candidate
  for candidate in .github/release-please-config.json release-please-config.json
  do
    if [ -n "$rev" ]; then
      git cat-file -e "$rev:$candidate" 2>/dev/null && { echo "$candidate"; return 0; }
    else
      [ -f "$candidate" ] && { echo "$candidate"; return 0; }
    fi
  done
  return 1
}

# rp_manifest_path <config-path>
rp_manifest_path() {
  echo "$(dirname "$1")/.release-please-manifest.json"
}

# rp_show <rev> <path> — file content at rev, or in the working tree when rev
# is empty. Fails when the file does not exist there.
rp_show() {
  if [ -n "$1" ]; then
    git show "$1:$2" 2>/dev/null
  else
    cat "$2" 2>/dev/null
  fi
}

# rp_component_path <config-json> <component> — the package path whose
# component is <component>; empty when none is.
rp_component_path() {
  jq -r --arg c "$2" \
    '[.packages | to_entries[] | select(.value.component == $c) | .key][0] // empty' \
    <<<"$1"
}

# rp_component <config-json> <path> — the component of package <path>.
rp_component() {
  jq -r --arg p "$2" '.packages[$p].component // empty' <<<"$1"
}

# rp_components <config-json> — every component, one per line.
rp_components() {
  jq -r '.packages[] | .component // empty' <<<"$1"
}

# rp_release_as <config-json> <path> — the release-as release-please applies
# to package <path> (its own, else the top-level one); empty when none.
rp_release_as() {
  jq -r --arg p "$2" '.packages[$p]["release-as"] // .["release-as"] // empty' <<<"$1"
}

# rp_group_of <config-json> <component> — the linked group <component> is in;
# empty when none.
rp_group_of() {
  jq -r --arg c "$2" \
    '[.["x-linked-groups"] // [] | .[] | select(.components | index($c)) | .group][0] // empty' \
    <<<"$1"
}

# rp_group_members <config-json> <group> — the group's components, one per
# line; nothing when the group is unknown.
rp_group_members() {
  jq -r --arg g "$2" \
    '.["x-linked-groups"] // [] | .[] | select(.group == $g) | .components[]' <<<"$1"
}

# rp_tag <config-json> <path> <version> — the tag release-please gives that
# package's release of <version>.
rp_tag() {
  jq -r --arg p "$2" --arg v "$3" '
    . as $c
    | def opt($k; $d): ($c.packages[$p][$k] // $c[$k]) as $x
                       | if $x == null then $d else $x end;
    (if opt("include-v-in-tag"; true) then "v" else "" end) as $vee
    | if opt("include-component-in-tag"; true)
      then "\($c.packages[$p].component)\(opt("tag-separator"; "-"))\($vee)\($v)"
      else "\($vee)\($v)"
      end' <<<"$1"
}

# rp_route <config-json> <files-json-array> — the package paths a commit
# touching exactly these files reaches, one per line (see the header).
rp_route() {
  jq -r --argjson files "$2" '
    def norm: sub("^/"; "") | sub("/$"; "");
    def under($p): ($p == ".") or startswith($p + "/");
    . as $c
    | [ $c.packages | keys[] | select(. != ".") | {key: ., path: norm} ]
      | sort_by(-(.path | length)) as $pkgs
    | ( if ($files | length) == 0 then [ $pkgs[].key ]
        else [ $files[] | select(contains("/")) | . as $f
               | first($pkgs[] | select(. as $p | $f | startswith($p.path + "/")))
               | .key ]
        end ) as $split
    | ( (if $c.packages["."] != null then ["."] else [] end) + $split | unique )
    | map( . as $k
           | ($k | norm) as $path
           | (($c.packages[$k]["exclude-paths"] // $c["exclude-paths"] // [])
              | map(norm)) as $excl
           | select( ($excl | length) == 0
                     or ( [ $files[] | select(under($path)) ]
                          | all(. as $f | any($excl[]; . as $e | $f | under($e)))
                          | not ) ) )
    | .[]' <<<"$1"
}

# rp_commit_files <sha> — the files a commit touches, as a JSON array.
rp_commit_files() {
  git diff-tree --no-commit-id --name-only -r -z --root "$1" |
    jq -Rs 'split("\u0000") | map(select(length > 0))'
}

# rp_parse_commits <message> [breaking] — the commits release-please parses
# from the message (see the header), one per line:
#   <type> TAB <scope> TAB <breaking> TAB <"!" in the header> TAB <a Release-As
#   footer with a version>
# the last three 1 or 0. With "breaking" it prints nothing and succeeds when
# one of them is breaking. NBSP and ZWNBSP, whitespace to the parser, read as
# a tab, which is whitespace to it too; a lone CR ends a line, as a CRLF does.
rp_parse_commits() {
  printf '%s\n' "$1" | LC_ALL=C awk -v mode="${2:-}" '
    function wstrip(s) { sub(/^[ \t\v\f]+/, "", s); return s }

    # head(s): the footer token s opens with: 0 none, 1 BREAKING CHANGE or
    # BREAKING-CHANGE, 2 "!", 3 a type; sets HT (type), HS ("(scope)"), HB
    # ("!" or empty), HP (separator) and HV (the value on this line). A
    # matched marker that misses its separator is no footer at all.
    function head(s,   r, kind) {
      HT = ""; HS = ""; HB = ""
      if (substr(s, 1, 1) == "!") { r = substr(s, 2); kind = 2 }
      else if (s ~ /^BREAKING[ -]CHANGE/) { r = substr(s, 16); kind = 1 }
      else {
        if (!match(s, /^[^()!: \t\v\f]+/)) return 0
        HT = substr(s, 1, RLENGTH); r = substr(s, RLENGTH + 1)
        if (substr(r, 1, 1) == "(") {
          if (!match(r, /^\([^()]+\)/)) return 0
          HS = substr(r, 1, RLENGTH); r = substr(r, RLENGTH + 1)
        }
        if (substr(r, 1, 1) == "!") { HB = "!"; r = substr(r, 2) }
        kind = 3
      }
      if (substr(r, 1, 1) == ":") { HP = ":"; r = substr(r, 2) }
      else if (substr(r, 1, 2) == " #") { HP = " #"; r = substr(r, 3) }
      else return 0
      HV = wstrip(r)
      return kind
    }

    # parse(d): parse the message L[d, 1..N[d]]; add a record per commit.
    # bt follows the text of the last breaking marker seen.
    function parse(d,   n, s, e, i, j, k, m, p, typ, scp, bang, bt, hit, ra,
                        fs, be, bl, f, fr, b, t, kind, ht, hs, hb, hp, last, nf) {
      n = N[d]
      s = 1; while (s <= n && L[d, s] ~ /^[ \t\v\f]*$/) s++
      if (s > n) return
      e = n; while (L[d, e] ~ /^[ \t\v\f]*$/) e--
      L[d, s] = wstrip(L[d, s]); sub(/[ \t\v\f]+$/, "", L[d, e])
      if (!match(L[d, s], /^[^()!: \t\v\f]+(\([^()]+\))?!?(:| #)/)) return
      p = substr(L[d, s], 1, RLENGTH - (substr(L[d, s], RLENGTH, 1) == ":" ? 1 : 2))
      bt = wstrip(substr(L[d, s], RLENGTH + 1))
      bang = substr(p, length(p)) == "!"
      if (bang) p = substr(p, 1, length(p) - 1); else bt = ""
      typ = p; scp = ""
      if ((i = index(p, "(")) > 0) {
        typ = substr(p, 1, i - 1); scp = substr(p, i + 1, length(p) - i - 1)
      }
      # A scope left open throws in the parser: no commit at all.
      for (i = s + 1; i <= e; i++) {
        if (L[d, i] ~ /^BREAKING[ -]CHANGE/ || !match(L[d, i], /^[^()!: \t\v\f]+\(/)) continue
        if (substr(L[d, i], RLENGTH + 1) !~ /^[^()]*\)/) return
      }
      hit = 0; ra = 0
      if (s < e) {
        # OK[d, i]: every line from i on opens a footer or continues one.
        OK[d, e + 1] = 1
        for (i = e; i > s; i--)
          OK[d, i] = (L[d, i] == "") ? OK[d, i + 1] : \
            (OK[d, i + 1] && (head(L[d, i]) || L[d, i] ~ /^[ \t\v\f]/))
        k = s + 1; while (L[d, k] == "") k++
        fs = k
        if (!(head(L[d, k]) && OK[d, k + 1])) {
          fs = 0
          for (i = k + 1; i <= e && !fs; i++)
            if (L[d, i] != "" && head(L[d, i]) && OK[d, i + 1]) fs = i
          be = fs ? fs - 1 : e
          # The body: its first marker line, and its text without markers.
          f = 0; fr = ""; b = ""; bl = 0
          for (i = k; i <= be; i++) {
            if ((t = L[d, i]) == "") continue
            if (t ~ /^BREAKING[ -]CHANGE:/) {
              t = wstrip(substr(t, 17))
              if (!f) { f = i; fr = t }
            } else if (t ~ /^BREAKING[ -]CHANGE/) t = substr(t, 16)
            b = b "\n" t; bl = i
          }
          if (f) bt = (fr != "" || f < bl) ? "x" : ""
          if ((j = index(b, "BREAKING-CHANGE:")) > 0 && substr(b, j + 16) ~ /[^ \t\n\v\f]/) hit = 1
        }
        # The footers. FT[d, ...] rebuilds a typed footer as the message the
        # parser reads it as: "<type>(<scope>)!: <value>", continuations on
        # lines of their own, a " #" separator dropped.
        for (i = fs; fs && i <= e; ) {
          kind = head(L[d, i]); ht = HT; hs = HS; hb = HB; hp = HP; last = HV
          nf = 1; FT[d, 1] = ht hs hb (hp == ":" ? ": " : "") HV
          for (j = i + 1; ; j = m + 1) {
            m = j; while (m <= e && L[d, m] == "") m++
            if (m > e || L[d, m] !~ /^[ \t\v\f]/) break
            while (j < m) FT[d, ++nf] = L[d, j++]
            last = wstrip(L[d, m]); FT[d, ++nf] = last
          }
          if (kind == 1) bt = last
          if (kind == 3) {
            if (tolower(ht) == "release-as" && last ~ /^[A-Za-z0-9_]*@?[0-9]+\.[0-9]+\.[0-9A-Za-z]/) ra = 1
            N[d + 1] = nf
            for (m = 1; m <= nf; m++) L[d + 1, m] = FT[d, m]
            parse(d + 1)
          }
          for (i = j; i <= e && L[d, i] == ""; i++) ;
        }
      }
      gsub(/\t/, " ", scp)
      OUT[++NO] = typ "\t" scp "\t" ((hit || bt != "") ? 1 : 0) "\t" bang "\t" ra
    }

    # load(a, b): the lines M[a..b] as the message L[1, ...].
    function load(a, b,   i, j, c, x) {
      N[1] = 0
      for (i = a; i <= b; i++) {
        x = M[i]; sub(/\r$/, "", x)
        c = split(x, PC, "\r")
        if (c == 0) L[1, ++N[1]] = ""
        for (j = 1; j <= c; j++) L[1, ++N[1]] = PC[j]
      }
    }

    { raw[++nr] = $0 }

    END {
      for (i = 1; i <= nr; i++) msg = msg (i > 1 ? "\n" : "") raw[i]
      gsub(/\302\240|\357\273\277/, "\t", msg)
      main = msg; nn = 0
      if ((p = index(msg, "BEGIN_NESTED_COMMIT")) > 0) {
        main = substr(msg, 1, p - 1); rest = substr(msg, p + 19)
        for (;;) {
          p = index(rest, "BEGIN_NESTED_COMMIT")
          seg = p ? substr(rest, 1, p - 1) : rest
          if ((q = index(seg, "END_NESTED_COMMIT")) > 0) {
            nest[++nn] = substr(seg, 1, q - 1); main = main substr(seg, q + 17)
          } else nest[++nn] = seg
          if (!p) break
          rest = substr(rest, p + 19)
        }
      }
      n = split(main, M, "\n"); a = 1
      for (i = 3; i <= n; i++)
        if ((M[i - 1] == "" || M[i - 1] == "\r") &&
            M[i] ~ /^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([^\r]*\))?: /) {
          load(a, i - 2); parse(1); a = i
        }
      load(a, n); parse(1)
      for (k = 1; k <= nn; k++) { n = split(nest[k], M, "\n"); load(1, n); parse(1) }
      if (mode == "breaking") {
        for (i = 1; i <= NO; i++) if (split(OUT[i], R, "\t") && R[3] == 1) exit 0
        exit 1
      }
      for (i = 1; i <= NO; i++) print OUT[i]
    }'
}

# rp_is_breaking <message> — succeeds when release-please would count a
# commit parsed from the message as breaking (see the header).
rp_is_breaking() {
  rp_parse_commits "$1" breaking
}

# rp_visible <config-json> <path> <message> — succeeds when a commit parsed
# from the message puts a line in package <path>'s changelog, without which
# release-please opens no release PR for it, pin or not: its type has a
# section that is not hidden (the package's changelog-sections, else the
# top-level ones, else release-please's defaults), it is breaking, its header
# has "!" after a type of word characters, or it carries a Release-As footer
# with a version.
rp_visible() {
  rp_parse_commits "$3" | jq -R -s -e --argjson cfg "$1" --arg p "$2" '
    ($cfg.packages[$p]["changelog-sections"] // $cfg["changelog-sections"] // [
      {type: "feat"}, {type: "feature"}, {type: "fix"}, {type: "perf"},
      {type: "revert"}, {type: "docs", hidden: true}, {type: "style", hidden: true},
      {type: "chore", hidden: true}, {type: "refactor", hidden: true},
      {type: "test", hidden: true}, {type: "build", hidden: true},
      {type: "ci", hidden: true}]) as $sections
    | split("\n") | map(select(length > 0) | split("\t"))
    | any(.[]; . as [$type, $scope, $breaking, $bang, $release_as]
        | $breaking == "1" or $release_as == "1"
          or ($bang == "1" and ($type | test("^[A-Za-z0-9_]*$")))
          or ((first($sections[] | select(.type == ($type | ascii_downcase)
                and ((.scope // "") == "" or .scope == $scope))) // {hidden: true})
              | .hidden != true))' >/dev/null
}

# rp_semver_valid <version>
rp_semver_valid() {
  local re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'
  [[ $1 =~ $re ]]
}

# rp_semver_field <version> <1|2|3> — major, minor or patch.
rp_semver_field() {
  local core="${1%%[-+]*}"
  echo "$core" | cut -d. -f"$2"
}

# rp_semver_pre <version> — the prerelease part ("alpha.0"); empty if none.
rp_semver_pre() {
  local v="${1%%+*}"
  case "$v" in *-*) echo "${v#*-}" ;; esac
}

# rp_semver_cmp <a> <b> — prints -1, 0 or 1 by semver precedence.
rp_semver_cmp() {
  local a="${1%%+*}" b="${2%%+*}" ap="" bp="" i x y n
  case "$a" in *-*) ap="${a#*-}"; a="${a%%-*}" ;; esac
  case "$b" in *-*) bp="${b#*-}"; b="${b%%-*}" ;; esac
  for i in 1 2 3; do
    x=$(echo "$a" | cut -d. -f"$i")
    y=$(echo "$b" | cut -d. -f"$i")
    if [ "$x" -lt "$y" ]; then echo -1; return; fi
    if [ "$x" -gt "$y" ]; then echo 1; return; fi
  done
  if [ -z "$ap" ] && [ -z "$bp" ]; then echo 0; return; fi
  if [ -z "$ap" ]; then echo 1; return; fi
  if [ -z "$bp" ]; then echo -1; return; fi
  local -a ai bi
  IFS=. read -ra ai <<<"$ap"
  IFS=. read -ra bi <<<"$bp"
  n=${#ai[@]}
  [ "${#bi[@]}" -gt "$n" ] && n=${#bi[@]}
  for ((i = 0; i < n; i++)); do
    if [ "$i" -ge "${#ai[@]}" ]; then echo -1; return; fi
    if [ "$i" -ge "${#bi[@]}" ]; then echo 1; return; fi
    x=${ai[$i]} y=${bi[$i]}
    if [[ $x =~ ^[0-9]+$ && $y =~ ^[0-9]+$ ]]; then
      if [ "$x" -lt "$y" ]; then echo -1; return; fi
      if [ "$x" -gt "$y" ]; then echo 1; return; fi
    elif [[ $x =~ ^[0-9]+$ ]]; then
      echo -1; return
    elif [[ $y =~ ^[0-9]+$ ]]; then
      echo 1; return
    elif [ "$x" != "$y" ]; then
      if [ "$(printf '%s\n%s\n' "$x" "$y" | LC_ALL=C sort | head -n1)" = "$x" ]; then
        echo -1
      else
        echo 1
      fi
      return
    fi
  done
  echo 0
}

# rp_semver_max <version>... — the highest by semver precedence.
rp_semver_max() {
  local best="$1" v
  shift
  for v in "$@"; do
    [ "$(rp_semver_cmp "$v" "$best")" -gt 0 ] && best="$v"
  done
  echo "$best"
}

# rp_is_new_line <current> <proposed> — succeeds when <proposed> starts a line
# a breaking change after <current> allows: a new minor while the major is 0,
# or a new major (minor 0); patch 0; no prerelease or exactly alpha.0.
rp_is_new_line() {
  local cmaj cmin pmaj pmin ppat pre
  cmaj=$(rp_semver_field "$1" 1); cmin=$(rp_semver_field "$1" 2)
  pmaj=$(rp_semver_field "$2" 1); pmin=$(rp_semver_field "$2" 2)
  ppat=$(rp_semver_field "$2" 3); pre=$(rp_semver_pre "$2")
  [ "$ppat" -eq 0 ] || return 1
  case "$pre" in ""|alpha.0) ;; *) return 1 ;; esac
  if [ "$pmaj" -gt "$cmaj" ]; then
    [ "$pmin" -eq 0 ]
  else
    [ "$cmaj" -eq 0 ] && [ "$pmaj" -eq 0 ] && [ "$pmin" -gt "$cmin" ]
  fi
}

# rp_new_line <current> — the version a breaking change after <current>
# starts: x.(y+1).0-alpha.0 while the major is 0, (x+1).0.0-alpha.0 after.
rp_new_line() {
  local maj min
  maj=$(rp_semver_field "$1" 1); min=$(rp_semver_field "$1" 2)
  if [ "$maj" -eq 0 ]; then
    echo "0.$((min + 1)).0-alpha.0"
  else
    echo "$((maj + 1)).0.0-alpha.0"
  fi
}

# rp_linked_applicable <current> <proposed> — succeeds when <proposed> makes
# a linked release (RELEASING.md, "Linked releases"): a new major in any
# channel, or a new minor that carries a prerelease suffix.
rp_linked_applicable() {
  local cmaj cmin pmaj pmin
  cmaj=$(rp_semver_field "$1" 1); cmin=$(rp_semver_field "$1" 2)
  pmaj=$(rp_semver_field "$2" 1); pmin=$(rp_semver_field "$2" 2)
  [ "$pmaj" -gt "$cmaj" ] && return 0
  [ "$pmaj" -eq "$cmaj" ] && [ "$pmin" -gt "$cmin" ] && [ -n "$(rp_semver_pre "$2")" ]
}
