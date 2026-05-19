"""Best-effort PII/secret redactor for telemetry attrs.

NOT parity with ``go/core/redact`` — this is an opinionated regex set covering
common leak shapes (emails, IPs, common token prefixes, $HOME paths). The
deliberate placeholder strings (``<redacted:email>``, ``<redacted:ipv4>``,
``<redacted:ipv6>``, ``<redacted:token>``) MUST match byte-for-byte across the
py / ts / rs / php SDKs so the cross-language contract harness (T-0709) can
diff outputs without per-language quirks.

Callers can layer their own ``redactor`` callback on ``Client`` (T-0714); it
runs BEFORE this default pass — see ``Client.__init__`` docstring.
"""

from __future__ import annotations

import os
import re
from typing import Any, Iterable, Pattern, Tuple

# --- Patterns ----------------------------------------------------------------

# $HOME-prefix rewrite. We escape the literal expansion so user home dirs with
# regex metacharacters don't break the pattern. The trailing capture group
# preserves the path tail (slashes, alnum, dot, dash, underscore).
_HOME = os.path.expanduser("~")
_HOME_PATTERN: Pattern[str] = (
    re.compile(rf"{re.escape(_HOME)}([/\w.\-]*)") if _HOME and _HOME != "~" else re.compile(r"(?!x)x")
)

_EMAIL: Pattern[str] = re.compile(r"\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b")
_IPV4: Pattern[str] = re.compile(r"\b(?:\d{1,3}\.){3}\d{1,3}\b")
# IPv6 regex covers full form (8 groups) AND compressed `::` form (1-7
# groups + double colon). Matches PHP SDK behavior for cross-lang
# parity. May over-match on hex-heavy strings that resemble IPv6 —
# accepted trade-off (over-redact > leak). The lookbehind/lookahead on
# `[0-9a-fA-F:]` anchors the match so it can't bleed into an adjacent
# hex word (e.g. a UUID fragment touching a colon).
_IPV6: Pattern[str] = re.compile(
    r"(?<![0-9a-fA-F:])"
    r"(?:"
    r"(?:[0-9a-fA-F]{1,4}:){1,7}(?::[0-9a-fA-F]{1,4})+"
    r"|"
    r"(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}"
    r")"
    r"(?![0-9a-fA-F:])"
)
_TOKEN_SK: Pattern[str] = re.compile(r"\bsk-[A-Za-z0-9_\-]{8,}\b")
_TOKEN_GH: Pattern[str] = re.compile(r"\bgh[pousr]_[A-Za-z0-9_\-]{16,}\b")
_TOKEN_XOXB: Pattern[str] = re.compile(r"\bxoxb-[0-9]+-[0-9]+-[A-Za-z0-9]{24,}\b")

# Order matters: token patterns run BEFORE IP/email so we don't accidentally
# eat parts of an xoxb token as numeric IP-shaped fragments. $HOME runs last
# so its tail capture doesn't swallow an embedded email/ip-shaped substring.
_REPLACEMENTS: Tuple[Tuple[Pattern[str], str], ...] = (
    (_TOKEN_SK, "<redacted:token>"),
    (_TOKEN_GH, "<redacted:token>"),
    (_TOKEN_XOXB, "<redacted:token>"),
    (_EMAIL, "<redacted:email>"),
    (_IPV6, "<redacted:ipv6>"),
    (_IPV4, "<redacted:ipv4>"),
    (_HOME_PATTERN, r"$HOME\1"),
)


def redact_string(s: str) -> str:
    """Apply every pattern in the opinionated set to ``s``."""
    for pat, repl in _REPLACEMENTS:
        s = pat.sub(repl, s)
    return s


def redact(value: Any) -> Any:
    """Return a NEW value with the opinionated regex set applied to all strings.

    Walks dicts, lists, and tuples recursively. Non-string scalars (int, bool,
    float, None) pass through unchanged. Input is never mutated.

    NOT parity with ``go/core/redact``. Use the per-Client ``redactor``
    callback for stricter or custom rules; this is the default-on backstop.
    """
    if isinstance(value, str):
        return redact_string(value)
    if isinstance(value, dict):
        return {k: redact(v) for k, v in value.items()}
    if isinstance(value, list):
        return [redact(v) for v in value]
    if isinstance(value, tuple):
        return tuple(redact(v) for v in value)
    return value


__all__: Iterable[str] = ("redact", "redact_string")
