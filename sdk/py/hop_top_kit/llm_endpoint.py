"""
hop_top_kit.llm_endpoint — where a provider credential may go, and
credential masking of provider URIs.

A key kit resolved from llm.yaml or the environment reaches an endpoint
the URI names only when it is loopback or shares an origin with the
provider's declared endpoints, llm.yaml ``base_url`` or
``LLM_BASE_URL``. A key the URI carries is the caller's choice for any
https host. No credential goes over plain http to a remote host, or to a
malformed endpoint: one carrying userinfo, a backslash, a
percent-escape, whitespace or a control character, forms URL parsers
disagree on.

Ports Go ``RedactURI``, ``CheckKeyEndpoint`` and ``CredentialsTrusted``
text for text; pinned by ``contracts/llm-v1`` (``redact_uri``,
``key_endpoint``, ``credential_headers``).
"""

from __future__ import annotations

import ipaddress
import unicodedata
from collections.abc import Iterable
from dataclasses import dataclass
from urllib.parse import urlsplit

from hop_top_kit import llm

__all__ = [
    "Endpoint",
    "check_key_endpoint",
    "credential_param",
    "credentials_trusted",
    "is_blank",
    "parse_endpoint",
    "redact_uri",
]

REDACTED = "REDACTED"

_EXACT = frozenset(
    {
        "key",
        "apikey",
        "token",
        "secret",
        "password",
        "passwd",
        "pwd",
        "auth",
        "authorization",
        "signature",
        "sig",
        "credential",
        "credentials",
    }
)
_SUFFIX = (
    "_key",
    "apikey",
    "_token",
    "_secret",
    "_password",
    "_signature",
    "_credential",
    "_credentials",
)


def is_blank(v: str) -> bool:
    """A key value that is no key: empty or only whitespace."""
    return not v.strip()


# ---------------------------------------------------------------------------
# Redaction
# ---------------------------------------------------------------------------


def redact_uri(raw: str) -> str:
    """*raw* with credentials replaced by ``REDACTED``, for errors and logs.

    Masks credential params (``key``, ``token``, ``secret``,
    ``password``, ``auth``, ``signature``, names ending ``_key``,
    ``_token``, ``apikey`` and the like, case-insensitive, ``-`` read as
    ``_``) and URL userinfo, in *raw* or in a param value such as
    ``base_url``. *raw* need not parse. A model id ``name@version`` is
    kept.
    """
    out: list[str] = []
    rest = raw
    while True:
        i = min((j for j in (rest.find("?"), rest.find("&")) if j >= 0), default=-1)
        if i < 0:
            break
        out.append(_redact_segment(rest[:i]))
        out.append(rest[i])
        rest = rest[i + 1 :]
    out.append(_redact_segment(rest))
    return "".join(out)


def _redact_segment(seg: str) -> str:
    k, eq, v = seg.partition("=")
    if eq and "://" not in k:
        if credential_param(k):
            return seg if not v else f"{k}={REDACTED}"
        return f"{k}={_redact_userinfo(v, True)}"
    return _redact_userinfo(seg, False)


def _redact_userinfo(s: str, strict: bool) -> str:
    i = s.find("://")
    start, scheme = (i + 3, s[:i].lower()) if i >= 0 else (0, "")
    rest = s[start:]
    e = rest.find("/")
    end, has_path = (e, True) if e >= 0 else (len(rest), False)
    at = rest[:end].rfind("@")
    if at <= 0:
        return s
    credential = (
        strict or scheme in ("http", "https") or (has_path and start > 0) or ":" in rest[:at]
    )
    if not credential:
        return s
    return f"{s[:start]}{REDACTED}{rest[at:]}"


def credential_param(name: str) -> bool:
    """Whether a param named *name* carries a credential."""
    n = _query_unescape(name)
    n = (n if n is not None else name).strip().lower().replace("-", "_")
    return n in _EXACT or n.endswith(_SUFFIX)


def _query_unescape(s: str) -> str | None:
    """Go ``url.QueryUnescape``: ``+`` is a space; ``None`` on a bad escape."""
    out = bytearray()
    i = 0
    b = s.encode("utf-8")
    while i < len(b):
        c = b[i]
        if c == 0x25:  # %
            hexd = b[i + 1 : i + 3]
            if len(hexd) != 2:
                return None
            try:
                out.append(int(hexd, 16))
            except ValueError:
                return None
            i += 3
        elif c == 0x2B:  # +
            out.append(0x20)
            i += 1
        else:
            out.append(c)
            i += 1
    try:
        return out.decode("utf-8")
    except UnicodeDecodeError:
        return None


# ---------------------------------------------------------------------------
# Endpoints
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class Endpoint:
    """A base URL parsed for a credential check."""

    scheme: str
    host: str
    port: int

    @property
    def origin(self) -> tuple[str, str, int]:
        return (self.scheme, self.host, self.port)

    @property
    def loopback(self) -> bool:
        return _is_loopback(self.host)


_DEFAULT_PORTS = {"https": 443, "http": 80}


def _bad_char(c: str) -> bool:
    return (
        c in "\\%"
        or c.isspace()
        or ord(c) <= 0x20
        or ord(c) == 0x7F
        or unicodedata.category(c) == "Cc"
    )


def parse_endpoint(raw: str) -> Endpoint | None:
    """Parse *raw* as urllib does for a credential check.

    ``None`` (no credential may go there) when it does not parse, has no
    host, or carries userinfo, a backslash, a percent-escape, whitespace
    or a control character.
    """
    if any(_bad_char(c) for c in raw):
        return None
    try:
        u = urlsplit(raw)
        port = u.port
    except ValueError:
        return None
    if not u.scheme or not u.netloc or "@" in u.netloc or not u.hostname:
        return None
    scheme = u.scheme.lower()
    if port is None:
        port = _DEFAULT_PORTS.get(scheme, 0)
    return Endpoint(scheme=scheme, host=u.hostname.lower(), port=port)


def _is_loopback(host: str) -> bool:
    """``localhost`` exactly, 127.0.0.0/8, ``::1`` or a v4-mapped loopback."""
    if host == "localhost":
        return True
    try:
        ip = ipaddress.ip_address(host)
    except ValueError:
        return False
    if ip.is_loopback:
        return True
    mapped = getattr(ip, "ipv4_mapped", None)
    return mapped is not None and mapped.is_loopback


def check_key_endpoint(scheme: str, key: str, base_url: str) -> None:
    """Refuse a non-blank *key* for *base_url* when it is malformed for
    credentials or is plain http to a host that is not loopback.

    Raises :class:`~hop_top_kit.llm.KeyEndpointError`.
    """
    if is_blank(key) or not base_url:
        return
    e = parse_endpoint(base_url)
    if e is None:
        raise llm.KeyEndpointError(scheme, redact_uri(base_url), malformed=True)
    if e.scheme == "http" and not e.loopback:
        raise llm.KeyEndpointError(scheme, redact_uri(base_url), insecure=True)


def credentials_trusted(base_url: str, trusted: Iterable[str]) -> bool:
    """Whether credentials kit resolved itself (an env or llm.yaml key, a
    bearer token, a workspace header) may go to *base_url*: it is
    loopback, or shares an origin with one of *trusted*. Every credential
    path decides here."""
    e = parse_endpoint(base_url)
    if e is None:
        return False
    if e.loopback:
        return True
    for t in trusted:
        te = parse_endpoint(t) if t else None
        if te is not None and te.origin == e.origin:
            return True
    return False
