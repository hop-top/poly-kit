"""URLSource fetches guarded against SSRF: http(s) only, public addresses
only (checked on the address actually connected to), no redirects, a
size cap, and the offline policy."""

from __future__ import annotations

import pytest

from hop_top_kit import llm, llm_media, netpolicy
from tests.llm_fakeserver import Canned, serve


def _allow_loopback(monkeypatch) -> None:
    """Let the fetch reach the loopback test server; every other check stays."""
    real = llm_media.is_public_ip
    monkeypatch.setattr(llm_media, "is_public_ip", lambda ip: ip.is_loopback or real(ip))


@pytest.mark.parametrize(
    "url",
    [
        "file:///etc/passwd",
        "ftp://example.com/x.png",
        "data:image/png;base64,AAAA",
        "gopher://example.com/x",
    ],
)
def test_non_http_schemes_refused(url):
    with pytest.raises(llm.LLMError, match="http or https"):
        llm.URLSource(url).read()


@pytest.mark.parametrize(
    "url",
    [
        "http://127.0.0.1/x.png",
        "http://localhost/x.png",
        "http://[::1]/x.png",
        "http://169.254.169.254/latest/meta-data/",
        "http://10.0.0.1/x.png",
        "http://192.168.1.1/x.png",
        "http://172.16.0.1/x.png",
        "http://0.0.0.0/x.png",
        "http://[::ffff:127.0.0.1]/x.png",
        "http://224.0.0.1/x.png",
        "http://100.64.0.1/x.png",
    ],
)
def test_non_public_addresses_refused(url):
    with pytest.raises(llm.LLMError, match="non-public address"):
        llm.URLSource(url).read()


def test_loopback_server_refused_by_default():
    with serve(Canned(200, {"content-type": "image/png"}, "PNG")) as srv:
        with pytest.raises(llm.LLMError, match="non-public address"):
            llm.URLSource(srv.url + "/x.png").read()
        assert srv.attempts == 0


def test_userinfo_refused():
    with pytest.raises(llm.LLMError, match="userinfo"):
        llm.URLSource("https://user:pw@example.com/x.png").read()


def test_fetch_public(monkeypatch):
    _allow_loopback(monkeypatch)
    with serve(Canned(200, {"content-type": "image/png"}, "PNG")) as srv:
        assert llm.URLSource(srv.url + "/x.png").read() == b"PNG"
        assert srv.only().path == "/x.png"


def test_redirect_not_followed(monkeypatch):
    _allow_loopback(monkeypatch)
    redirect = Canned(302, {"location": "http://169.254.169.254/latest/meta-data/"})
    with serve(redirect) as srv:
        with pytest.raises(llm.LLMError, match="redirect not followed"):
            llm.URLSource(srv.url + "/x.png").read()
        assert srv.attempts == 1


def test_error_status(monkeypatch):
    _allow_loopback(monkeypatch)
    with serve(Canned(404, {}, "nope")) as srv:
        with pytest.raises(llm.LLMError, match="http 404"):
            llm.URLSource(srv.url + "/x.png").read()


def test_oversize_refused(monkeypatch):
    _allow_loopback(monkeypatch)
    with serve(Canned(200, {"content-type": "image/png"}, "x" * 2048)) as srv:
        with pytest.raises(llm.MediaTooLargeError):
            llm.URLSource(srv.url + "/x.png", max_bytes=1024).read()


def test_offline_refused():
    netpolicy.set_offline(True)
    with pytest.raises(netpolicy.OfflineError):
        llm.URLSource("https://example.com/x.png").read()


def test_credentials_in_url_not_echoed():
    with pytest.raises(llm.LLMError) as ei:
        llm.URLSource("http://127.0.0.1/x.png?X-Amz-Signature=sekrit").read()
    assert "sekrit" not in str(ei.value)
