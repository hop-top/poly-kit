"""
hop_top_kit.llm_media — the guarded fetch behind
:meth:`hop_top_kit.llm.URLSource.read`.

A media URL may come from untrusted input, so the fetch refuses what
would make it a server-side request forgery or a local file read:

- ``http`` and ``https`` only (no ``file:``, ``ftp:``, ``data:``), no
  userinfo;
- every address the host resolves to must be public: loopback, private,
  link-local (cloud metadata ``169.254.169.254`` among them), shared,
  reserved, unspecified and multicast addresses are refused, v4-mapped
  and NAT64 IPv6 judged by their IPv4;
- the connection goes to the vetted address itself, so a second DNS
  answer cannot rebind it; TLS still verifies the host name;
- redirects are not followed; a non-2xx status is an error;
- the body is capped (:data:`DEFAULT_MAX_MEDIA_BYTES`), with a timeout;
- the ``--offline`` policy (:mod:`hop_top_kit.netpolicy`) applies.

Errors name the destination without query or userinfo.
"""

from __future__ import annotations

import http.client
import ipaddress
import socket
import ssl
from urllib.parse import urlsplit

from hop_top_kit import llm, netpolicy

__all__ = ["DEFAULT_MAX_MEDIA_BYTES", "fetch", "is_public_ip"]

DEFAULT_MAX_MEDIA_BYTES = 32 << 20
DEFAULT_TIMEOUT = 30.0
_CHUNK = 64 << 10

IPAddress = ipaddress.IPv4Address | ipaddress.IPv6Address


_NAT64 = ipaddress.IPv6Network("64:ff9b::/96")


def is_public_ip(ip: IPAddress) -> bool:
    """Whether *ip* is a globally routable unicast address. A v4-mapped
    or NAT64 (``64:ff9b::/96``) address is judged by the IPv4 address it
    carries."""
    mapped = getattr(ip, "ipv4_mapped", None)
    if mapped is not None:
        ip = mapped
    elif isinstance(ip, ipaddress.IPv6Address) and ip in _NAT64:
        ip = ipaddress.IPv4Address(ip.packed[12:])
    return ip.is_global and not ip.is_multicast


def _refuse(dest: str, why: str) -> llm.LLMError:
    return llm.LLMError(f"media: refusing to fetch {dest}: {why}")


def _vetted_address(dest: str, host: str, port: int) -> str:
    try:
        infos = socket.getaddrinfo(host, port, type=socket.SOCK_STREAM)
    except OSError as e:
        raise llm.LLMError(f"media: fetch {dest}: {e}") from None
    addrs = []
    for info in infos:
        ip = ipaddress.ip_address(info[4][0].split("%", 1)[0])
        if not is_public_ip(ip):
            raise _refuse(dest, f"non-public address {ip}")
        addrs.append(str(ip))
    if not addrs:
        raise _refuse(dest, "host resolves to no address")
    return addrs[0]


class _PinnedHTTP(http.client.HTTPConnection):
    def __init__(self, host: str, port: int, ip: str, timeout: float) -> None:
        super().__init__(host, port, timeout=timeout)
        self._ip = ip

    def connect(self) -> None:
        self.sock = socket.create_connection((self._ip, self.port), self.timeout)


class _PinnedHTTPS(http.client.HTTPSConnection):
    def __init__(self, host: str, port: int, ip: str, timeout: float) -> None:
        self._ctx = ssl.create_default_context()
        super().__init__(host, port, timeout=timeout, context=self._ctx)
        self._ip = ip

    def connect(self) -> None:
        raw = socket.create_connection((self._ip, self.port), self.timeout)
        self.sock = self._ctx.wrap_socket(raw, server_hostname=self.host)


def fetch(
    url: str, *, max_bytes: int = DEFAULT_MAX_MEDIA_BYTES, timeout: float = DEFAULT_TIMEOUT
) -> bytes:
    """GET *url* under the rules in the module docs and return its body."""
    dest = netpolicy._destination(url)
    try:
        u = urlsplit(url)
        port = u.port
    except ValueError:
        raise _refuse(dest, "URL does not parse") from None
    scheme = u.scheme.lower()
    if scheme not in ("http", "https"):
        raise _refuse(dest, "only http or https URLs are fetched")
    if "@" in u.netloc:
        raise _refuse(dest, "URL carries userinfo")
    host = u.hostname
    if not host:
        raise _refuse(dest, "URL has no host")
    port = port or (443 if scheme == "https" else 80)
    if netpolicy.is_offline():
        raise netpolicy.OfflineError(f"GET {dest}: network disabled by --offline")
    ip = _vetted_address(dest, host, port)
    conn_cls = _PinnedHTTPS if scheme == "https" else _PinnedHTTP
    conn = conn_cls(host, port, ip, timeout)
    path = (u.path or "/") + (f"?{u.query}" if u.query else "")
    try:
        conn.request("GET", path, headers={"accept": "*/*"})
        resp = conn.getresponse()
        if 300 <= resp.status < 400:
            raise llm.HTTPStatusError(resp.status, f"media: redirect not followed ({dest})")
        if not 200 <= resp.status < 300:
            raise llm.LLMError(f"media: fetch {dest}: http {resp.status}")
        length = resp.getheader("content-length")
        if length and length.isdigit() and int(length) > max_bytes:
            raise llm.MediaTooLargeError(int(length), max_bytes, "media")
        out = bytearray()
        while chunk := resp.read(_CHUNK):
            out += chunk
            if len(out) > max_bytes:
                raise llm.MediaTooLargeError(len(out), max_bytes, "media")
        return bytes(out)
    except (OSError, http.client.HTTPException) as e:
        raise llm.LLMError(f"media: fetch {dest}: {e}") from None
    finally:
        conn.close()
