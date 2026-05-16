"""Tests for hop_top_kit.aim — AI model registry."""

import json
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from hop_top_kit.aim import (
    Cache,
    Filter,
    Model,
    ModelsDevSource,
    Provider,
    Registry,
    _matches_filter,
    _providers_from_dict,
    parse_query,
)

_TESTDATA = Path(__file__).resolve().parent / "testdata"

# ---------------------------------------------------------------------------
# Query parser — vector tests
# ---------------------------------------------------------------------------

_FIELD_MAP = {
    "Provider": "provider",
    "Family": "family",
    "Input": "input",
    "Output": "output",
    "ToolCall": "tool_call",
    "Reasoning": "reasoning",
    "OpenWeights": "open_weights",
    "Query": "query",
}


def _load_vectors() -> list[dict]:
    return json.loads((_TESTDATA / "query-vectors.json").read_text())


@pytest.mark.parametrize("vec", _load_vectors(), ids=lambda v: v.get("description", v["input"]))
def test_parse_query_vector(vec: dict):
    if "error" in vec:
        with pytest.raises(ValueError, match=vec["error"]):
            parse_query(vec["input"])
        return
    f = parse_query(vec["input"])
    expected = vec["expected"]
    for go_key, py_attr in _FIELD_MAP.items():
        want = expected.get(go_key)
        got = getattr(f, py_attr)
        if want is None:
            default = "" if isinstance(got, str) else None
            assert got == default, f"{py_attr}: expected default, got {got!r}"
        else:
            assert got == want, f"{py_attr}: expected {want!r}, got {got!r}"


# ---------------------------------------------------------------------------
# ModelsDevSource
# ---------------------------------------------------------------------------


def _fixture_bytes() -> bytes:
    return (_TESTDATA / "api-fixture.json").read_bytes()


def _fixture_providers() -> dict[str, Provider]:
    return _providers_from_dict(json.loads(_fixture_bytes()))


def _mock_urlopen(body: bytes, status: int = 200, etag: str = ""):
    resp = MagicMock()
    resp.read = MagicMock(return_value=body)
    resp.status = status
    resp.headers = {"ETag": etag}
    return resp


class TestModelsDevSource:
    def test_fetch_parses_fixture(self):
        src = ModelsDevSource(url="http://test.invalid/api.json")
        resp = _mock_urlopen(_fixture_bytes())
        with patch("hop_top_kit.aim.urllib.request.urlopen", return_value=resp):
            providers = src.fetch()
        assert "anthropic" in providers
        assert "openai" in providers
        m = providers["anthropic"].models["claude-3-5-sonnet"]
        assert m.tool_call is True
        assert m.input == ["text", "image"]
        assert m.context == 200000

    def test_fetch_with_etag_not_modified(self):
        src = ModelsDevSource(url="http://test.invalid/api.json")
        err = __import__("urllib.error", fromlist=["HTTPError"]).HTTPError(
            "http://test.invalid/api.json", 304, "Not Modified", {}, None
        )
        with patch("hop_top_kit.aim.urllib.request.urlopen", side_effect=err):
            providers, etag, not_mod = src.fetch_with_etag("old-etag")
        assert not_mod is True
        assert providers is None
        assert etag == "old-etag"


# ---------------------------------------------------------------------------
# Cache
# ---------------------------------------------------------------------------


class _FakeSource:
    def __init__(self, providers: dict[str, Provider]):
        self._providers = providers
        self.call_count = 0

    def fetch(self) -> dict[str, Provider]:
        self.call_count += 1
        return self._providers


class TestCache:
    def test_stores_and_loads(self, tmp_path):
        src = _FakeSource(_fixture_providers())
        c = Cache(src, cache_dir=str(tmp_path))
        p1 = c.fetch()
        assert "anthropic" in p1
        assert src.call_count == 1
        # second call serves from disk
        c2 = Cache(src, cache_dir=str(tmp_path))
        p2 = c2.fetch()
        assert "anthropic" in p2
        assert src.call_count == 1

    def test_ttl_expires(self, tmp_path):
        src = _FakeSource(_fixture_providers())
        c = Cache(src, cache_dir=str(tmp_path), ttl=0)
        c.fetch()
        assert src.call_count == 1
        c.fetch()
        assert src.call_count == 2

    def test_stale_on_error(self, tmp_path):
        src = _FakeSource(_fixture_providers())
        c = Cache(src, cache_dir=str(tmp_path), ttl=0)
        c.fetch()
        # now make source fail
        src.fetch = MagicMock(side_effect=RuntimeError("network down"))
        result = c.fetch()
        assert "anthropic" in result

    def test_lock_and_unlock(self, tmp_path):
        src = _FakeSource(_fixture_providers())
        c = Cache(src, cache_dir=str(tmp_path))
        c.fetch()
        assert not (tmp_path / ".lock").exists()

    def test_force_refresh(self, tmp_path):
        src = _FakeSource(_fixture_providers())
        c = Cache(src, cache_dir=str(tmp_path))
        c.fetch()
        assert src.call_count == 1
        c.refresh(force=True)
        assert src.call_count == 2


# ---------------------------------------------------------------------------
# Registry — filter matching
# ---------------------------------------------------------------------------


class TestFilterMatching:
    @pytest.fixture()
    def models(self) -> dict[str, Model]:
        return {mid: m for p in _fixture_providers().values() for mid, m in p.models.items()}

    def test_empty_filter_matches_all(self, models):
        f = Filter()
        matched = [m for m in models.values() if _matches_filter(m, f)]
        assert len(matched) == len(models)

    def test_provider_filter(self, models):
        f = Filter(provider="anthropic")
        matched = [m for m in models.values() if _matches_filter(m, f)]
        assert all(m.provider == "anthropic" for m in matched)
        assert len(matched) == 3

    def test_family_filter(self, models):
        f = Filter(family="llama")
        matched = [m for m in models.values() if _matches_filter(m, f)]
        assert all(m.family == "llama" for m in matched)
        assert len(matched) == 2

    def test_bool_filter_reasoning(self, models):
        f = Filter(reasoning=True)
        matched = [m for m in models.values() if _matches_filter(m, f)]
        ids = {m.id for m in matched}
        assert "claude-3-7-sonnet" in ids
        assert "o1" in ids

    def test_bool_filter_open_weights(self, models):
        f = Filter(open_weights=True)
        matched = [m for m in models.values() if _matches_filter(m, f)]
        assert all(m.open_weights for m in matched)
        assert len(matched) == 2

    def test_modality_filter(self, models):
        f = Filter(input="image")
        matched = [m for m in models.values() if _matches_filter(m, f)]
        assert all("image" in m.input for m in matched)

    def test_query_filter(self, models):
        f = Filter(query="claude")
        matched = [m for m in models.values() if _matches_filter(m, f)]
        assert all("claude" in m.id or "Claude" in m.name for m in matched)

    def test_combined_filter(self, models):
        f = Filter(provider="anthropic", reasoning=True)
        matched = [m for m in models.values() if _matches_filter(m, f)]
        assert len(matched) == 1
        assert matched[0].id == "claude-3-7-sonnet"


# ---------------------------------------------------------------------------
# Registry — E2E with fixture data
# ---------------------------------------------------------------------------


class TestRegistry:
    def _make_registry(self, tmp_path) -> Registry:
        src = _FakeSource(_fixture_providers())
        return Registry(sources=[src], cache_dir=str(tmp_path))

    def test_providers_sorted(self, tmp_path):
        r = self._make_registry(tmp_path)
        ids = [p.id for p in r.providers()]
        assert ids == sorted(ids)

    def test_models_all(self, tmp_path):
        r = self._make_registry(tmp_path)
        assert len(r.models()) == 8

    def test_models_filtered(self, tmp_path):
        r = self._make_registry(tmp_path)
        result = r.models(Filter(provider="meta"))
        assert all(m.provider == "meta" for m in result)
        assert len(result) == 2

    def test_get_existing(self, tmp_path):
        r = self._make_registry(tmp_path)
        m = r.get("openai", "gpt-4o")
        assert m is not None
        assert m.name == "GPT-4o"

    def test_get_missing(self, tmp_path):
        r = self._make_registry(tmp_path)
        assert r.get("openai", "nonexistent") is None
        assert r.get("nonexistent", "gpt-4o") is None

    def test_query_string(self, tmp_path):
        r = self._make_registry(tmp_path)
        result = r.query("provider:anthropic reasoning:true")
        assert len(result) == 1
        assert result[0].id == "claude-3-7-sonnet"

    def test_query_freetext(self, tmp_path):
        r = self._make_registry(tmp_path)
        result = r.query("dall")
        assert len(result) == 1
        assert result[0].id == "dall-e-3"

    def test_model_normalize(self):
        m = Model(
            id="test",
            modalities=__import__("hop_top_kit.aim", fromlist=["Modalities"]).Modalities(
                input=["text"], output=["image"]
            ),
            limit=__import__("hop_top_kit.aim", fromlist=["Limit"]).Limit(
                context=1000, max_output=500
            ),
        )
        m.normalize()
        assert m.input == ["text"]
        assert m.output == ["image"]
        assert m.context == 1000
        assert m.max_output == 500
