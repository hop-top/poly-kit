"""Lenient version order for migrations.

Kit's own ordering, shared with the Go reference, not ``packaging``: a
leading ``v`` is dropped, build metadata after ``+`` is ignored, each of
major, minor and patch is the leading decimal digits of its dot-separated
part (missing or non-numeric is 0; anything after the third dot belongs
to patch and is ignored). A release sorts above its pre-releases;
pre-release identifiers compare left to right, a numeric identifier
(decimal, no leading zero) below an alphanumeric one, numbers by value,
text bytewise, and a shorter list of equal identifiers first.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

_DIGITS = re.compile(r"[0-9]+")
# An identifier is numeric when it is its own canonical decimal form.
_NUMERIC = re.compile(r"0|-?[1-9][0-9]*")


@dataclass(frozen=True)
class Semver:
    """A parsed version: ``major.minor.patch`` plus the pre-release text."""

    major: int = 0
    minor: int = 0
    patch: int = 0
    pre: str = ""


def _lead(part: str) -> int:
    m = _DIGITS.match(part)
    return int(m.group()) if m else 0


def parse_semver(s: str) -> Semver:
    """Parse *s* leniently; it never fails. A leading ``v`` is the caller's
    to drop (:func:`compare_versions` does)."""
    s = s.split("+", 1)[0]
    pre = ""
    if "-" in s:
        s, pre = s.split("-", 1)
    parts = s.split(".", 2)
    nums = [_lead(p) for p in parts] + [0] * (3 - len(parts))
    return Semver(nums[0], nums[1], nums[2], pre)


def _cmp(a: int, b: int) -> int:
    return (a > b) - (a < b)


def _cmp_ident(a: str, b: str) -> int:
    a_num = _NUMERIC.fullmatch(a) is not None
    b_num = _NUMERIC.fullmatch(b) is not None
    if a_num and b_num:
        return _cmp(int(a), int(b))
    if a_num:
        return -1
    if b_num:
        return 1
    ab, bb = a.encode(), b.encode()
    return (ab > bb) - (ab < bb)


def _cmp_pre(a: str, b: str) -> int:
    ap, bp = a.split("."), b.split(".")
    for x, y in zip(ap, bp, strict=False):
        d = _cmp_ident(x, y)
        if d:
            return d
    return _cmp(len(ap), len(bp))


def compare_semver(a: Semver, b: Semver) -> int:
    """Sign of *a* against *b*: -1, 0 or 1."""
    for x, y in ((a.major, b.major), (a.minor, b.minor), (a.patch, b.patch)):
        if x != y:
            return _cmp(x, y)
    if not a.pre and not b.pre:
        return 0
    if not a.pre:
        return 1
    if not b.pre:
        return -1
    return _cmp_pre(a.pre, b.pre)


def compare_versions(a: str, b: str) -> int:
    """Sign of version string *a* against *b*: -1, 0 or 1. One leading
    ``v`` is dropped from each."""
    return compare_semver(parse_semver(a.removeprefix("v")), parse_semver(b.removeprefix("v")))
