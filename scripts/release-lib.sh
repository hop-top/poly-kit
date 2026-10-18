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
#   Breaking changes (commit.js). A message splits into one commit per
#   paragraph that opens with a lowercase conventional type and ": ". A part
#   counts only when its header parses (type, optional non-empty scope,
#   optional "!", ":", a subject), and is breaking when the header has "!"
#   before the colon, a body line starts with "BREAKING CHANGE:" followed by
#   text, or a body line holds "BREAKING-CHANGE:" with text after it.
#   BEGIN_NESTED_COMMIT blocks and BEGIN_COMMIT_OVERRIDE pull request bodies
#   are not mirrored.
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

# rp_is_breaking <message> — succeeds when release-please would count a
# commit parsed from the message as breaking (see the header).
rp_is_breaking() {
  printf '%s\n' "$1" | awk '
    function flush(   i, line, rest, pending) {
      if (n == 0) return
      if (lines[1] ~ /^[A-Za-z0-9_-]+(\([^()]+\))?!?:[ \t]*[^ \t]/) {
        if (lines[1] ~ /^[A-Za-z0-9_-]+(\([^()]+\))?!:/) found = 1
        pending = 0
        for (i = 2; i <= n; i++) {
          line = lines[i]
          if (line ~ /^BREAKING CHANGE:[ \t]*[^ \t]/) found = 1
          if (pending && line ~ /[^ \t]/) found = 1
          if (match(line, /BREAKING-CHANGE:/)) {
            rest = substr(line, RSTART + RLENGTH)
            if (rest ~ /[^ \t]/) found = 1; else pending = 1
          }
        }
      }
      n = 0
    }
    {
      sub(/\r$/, "")
      if (NR > 1 && prev == "" &&
          $0 ~ /^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\(.*\))?: /)
        flush()
      lines[++n] = $0
      prev = $0
    }
    END { flush(); exit(found ? 0 : 1) }'
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
