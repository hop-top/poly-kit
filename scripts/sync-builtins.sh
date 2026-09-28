#!/usr/bin/env bash
#
# sync-builtins.sh — copy built-in template trees from templates/ into an
# embed mirror.
#
# Usage: scripts/sync-builtins.sh <dest> <template>...
#
#   make builtins-sync       writes internal/template/builtins
#   make check-mirror-sync   writes a temp dir and diffs it against the mirror
#
# templates/ is canonical and the mirror is a verbatim copy, with one
# rewrite: a relative Markdown link in a *.md file that leaves its
# template tree (templates/shared/README.md -> ../../docs/x.md) is rebased
# so it resolves from the mirror's location instead
# (../../../../docs/x.md). Without it the same bytes cannot be a working
# link in both places. Links inside the template tree, absolute URLs,
# anchors and template actions are copied unchanged.
#
# Run from the repo root. Idempotent: the same templates/ always produce
# the same mirror, so the check is "regenerate, then diff".

set -euo pipefail

MIRROR=internal/template/builtins

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <dest> <template>..." >&2
  exit 2
fi
dest="$1"
shift

[ -d templates ] || { echo "$0: run from the repo root (no templates/)" >&2; exit 2; }

rm -rf "$dest"
mkdir -p "$dest"
for tmpl in "$@"; do
  [ -d "templates/$tmpl" ] && cp -R "templates/$tmpl" "$dest/$tmpl"
done

# Rebase escaping links. Paths are resolved as strings against the repo
# root, so nothing outside the tree is touched or stat'ed.
find "$dest" -type f -name '*.md' | LC_ALL=C sort | while IFS= read -r file; do
  rel="${file#"$dest"/}"
  MIRROR="$MIRROR" REL="$rel" perl -0pi -e '
    sub norm {
      my @out;
      for my $seg (split m{/}, $_[0]) {
        next if $seg eq "" || $seg eq ".";
        if ($seg eq "..") { return undef unless @out; pop @out; next }
        push @out, $seg;
      }
      return join "/", @out;
    }
    sub rebase {
      my ($link) = @_;
      return $link if $link =~ m{^(?:[a-z][a-z0-9+.-]*:|/|#|\{\{)}i;
      my ($path, $frag) = $link =~ m{^([^#]*)(#.*)?$};
      $frag //= "";
      return $link if $path eq "";
      my ($tmpl) = $ENV{REL} =~ m{^([^/]+)/};
      (my $dir = $ENV{REL}) =~ s{/?[^/]*$}{};
      my $target = norm("templates/$dir/$path");
      return $link unless defined $target;
      return $link if index("$target/", "templates/$tmpl/") == 0;
      my $depth = () = "$ENV{MIRROR}/$dir" =~ m{[^/]+}g;
      return ("../" x $depth) . $target . $frag;
    }
    s{(\]\()([^)\s]+)(\))}{$1 . rebase($2) . $3}ge;
    s{^(\s*\[[^\]]+\]:\s*)(\S+)}{$1 . rebase($2)}gme;
  ' "$file"
done
