"""RouteLLM model field (router pair or tier name), HTTP mode against an
OpenAI-compatible RouteLLM server, and mode selection. Tables mirror Go
``ai/llm/routellm`` and the TS port. Every server is loopback."""

from __future__ import annotations

import json
from typing import Any
from unittest import mock

import pytest

from hop_top_kit import llm
from hop_top_kit.routellm_adapter import (
    RouteLLMAdapter,
    format_threshold,
    parse_model_field,
)
from tests.llm_fakeserver import Canned, serve


@pytest.fixture(autouse=True)
def clean_env(monkeypatch, tmp_path):
    for k in (
        "ROUTELLM_BASE_URL",
        "ROUTELLM_API_KEY",
        "LLM_BASE_URL",
        "LLM_API_KEY",
        "LLM_PROVIDER",
        "LLM_FALLBACK",
    ):
        monkeypatch.delenv(k, raising=False)
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))


def _completion(message: dict[str, Any], finish: str = "stop") -> dict[str, Any]:
    return {
        "choices": [{"index": 0, "message": message, "finish_reason": finish}],
        "usage": {"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
    }


_OK = _completion({"role": "assistant", "content": "ok"})


def _hi() -> llm.Request:
    return llm.Request(messages=[llm.Message(role="user", content="hi")])


def _cfg(
    model: str,
    *,
    base: str = "",
    key: str = "",
    extras: dict[str, Any] | None = None,
    uri_only: bool = False,
    hermetic: bool = False,
    **params: str,
) -> llm.ResolvedConfig:
    return llm.ResolvedConfig(
        uri=llm.URI(scheme="routellm", model=model, params=dict(params)),
        provider=llm.ProviderConfig(
            api_key=key,
            base_url=base,
            model="" if uri_only else model,
            params=dict(params),
            extras=extras or {},
        ),
        hermetic=hermetic,
    )


# ------------------------------------------------------------------
# parse_model_field
# ------------------------------------------------------------------

# Router pair: the text after the last colon parses as a finite float
# (Go ParseFloat syntax, probed against strconv).
_PAIRS = [
    ("valid", "mf:0.7", "mf", 0.7),
    ("zero", "bert:0", "bert", 0.0),
    ("one", "causal_llm:1", "causal_llm", 1.0),
    ("precise", "mf:0.123456", "mf", 0.123456),
    ("exponent", "mf:5e-1", "mf", 0.5),
    ("leading dot", "mf:.5", "mf", 0.5),
    ("trailing dot", "mf:1.", "mf", 1.0),
    ("hex float", "mf:0x1p-1", "mf", 0.5),
    ("colon in router name", "a:b:0.5", "a:b", 0.5),
    ("out of range still a pair", "mf:1.5", "mf", 1.5),
    ("upper-case hex float", "mf:0X.8P1", "mf", 1.0),
    ("underscore after hex prefix", "mf:0x_1p-1", "mf", 0.5),
    ("underscore between digits", "mf:1_0", "mf", 10.0),
    ("underscore in exponent", "mf:1e+0_1", "mf", 10.0),
    ("leading zeros", "mf:00.5", "mf", 0.5),
    ("underflow to zero", "mf:1e-400", "mf", 0.0),
]

# Tier: everything else non-empty, verbatim.
_TIERS = [
    ("bare tier", "private"),
    ("underscore tier", "coding_fast"),
    ("server router id", "router-mf-0.5"),
    ("non-numeric suffix", "mf:abc"),
    ("colon in tier name", "a:b"),
    ("empty name", ":0.5"),
    ("empty threshold", "mf:"),
    ("NaN suffix", "mf:NaN"),
    ("Inf suffix", "mf:Inf"),
    ("Infinity suffix", "mf:Infinity"),
    ("lower-case inf suffix", "mf:inf"),
    ("overflowing suffix", "mf:1e400"),
    ("overflowing hex suffix", "mf:0x1p1024"),
    ("whitespace suffix", "mf: 0.5"),
    ("trailing whitespace suffix", "mf:0.5 "),
    ("hex integer suffix", "mf:0x10"),
    ("empty-after-sign suffix", "mf:+"),
    ("double underscore", "mf:1__0"),
    ("leading underscore", "mf:_1"),
    ("trailing underscore", "mf:1_"),
    ("underscore before dot", "mf:1_.5"),
    ("underscore before hex exponent", "mf:0x1_p0"),
    ("underscore inside hex prefix", "mf:0_x1p0"),
    ("non-ascii digit", "mf:" + chr(0x661)),
]


class TestParseModelField:
    @pytest.mark.parametrize(("name", "model", "router", "threshold"), _PAIRS)
    def test_router_pair(self, name, model, router, threshold):
        p = parse_model_field(model)
        assert not p.is_tier
        assert p.tier == ""
        assert p.router_name == router
        assert p.threshold == threshold

    @pytest.mark.parametrize(("name", "model"), _TIERS)
    def test_tier(self, name, model):
        p = parse_model_field(model)
        assert p.is_tier
        assert p.tier == model
        assert p.router_name == ""
        assert p.threshold == 0.0

    def test_empty_raises(self):
        with pytest.raises(
            llm.LLMError,
            match=r"^model field is required \(router_name:threshold or tier name\)$",
        ):
            parse_model_field("")


class TestFormatThreshold:
    # Go strconv.FormatFloat(v, 'f', -1, 64).
    @pytest.mark.parametrize(
        ("v", "want"),
        [
            (0.7, "0.7"),
            (0.0, "0"),
            (-0.0, "-0"),
            (1.0, "1"),
            (0.5, "0.5"),
            (1e-7, "0.0000001"),
            (1e22, "10000000000000000000000"),
            (0.30000000000000004, "0.30000000000000004"),
        ],
    )
    def test_go_format(self, v, want):
        assert format_threshold(v) == want


# ------------------------------------------------------------------
# HTTP mode: model field on the wire
# ------------------------------------------------------------------

_WIRE = [
    # Router pair: translated to the server's router id.
    ("router pair", "mf:0.7", "router-mf-0.7"),
    ("threshold zero", "bert:0", "router-bert-0"),
    ("threshold one", "causal_llm:1", "router-causal_llm-1"),
    ("trailing zeros trimmed", "mf:0.50", "router-mf-0.5"),
    ("exponent expanded", "mf:5e-1", "router-mf-0.5"),
    ("tiny threshold without exponent", "mf:1e-7", "router-mf-0.0000001"),
    ("negative zero", "mf:-0", "router-mf--0"),
    ("colon in router name", "a:b:0.5", "router-a:b-0.5"),
    # Tier: verbatim, server resolves router and threshold.
    ("tier", "private", "private"),
    ("tier underscore", "coding_fast", "coding_fast"),
    ("tier server router id", "router-mf-0.5", "router-mf-0.5"),
    ("tier with colon", "a:b", "a:b"),
]


class TestHTTPModeWire:
    @pytest.mark.parametrize(("name", "model", "want"), _WIRE)
    def test_wire_model(self, name, model, want):
        with serve(Canned.json(200, _OK)) as srv:
            a = RouteLLMAdapter(_cfg(model, base=srv.url, hermetic=True))
            assert a.mode == "http"
            assert a.complete(_hi()).content == "ok"
            req = srv.only()
            assert req.path == "/v1/chat/completions"
            assert req.json()["model"] == want
            a.close()

    def test_falls_back_to_uri_model(self):
        with serve(Canned.json(200, _OK)) as srv:
            RouteLLMAdapter(_cfg("bert:0.5", base=srv.url, uri_only=True)).complete(_hi())
            assert srv.only().json()["model"] == "router-bert-0.5"

    def test_provider_model_outranks_uri_model(self):
        cfg = _cfg("private", base="http://127.0.0.1:1")
        cfg.uri.model = "mf:0.7"
        a = RouteLLMAdapter(cfg)
        assert (a.tier, a.is_tier()) == ("private", True)

    def test_exposes_tier(self):
        a = RouteLLMAdapter(_cfg("private", base="http://127.0.0.1:1"))
        assert a.is_tier()
        assert (a.tier, a.router_name, a.threshold) == ("private", "", 0.0)

    def test_exposes_router_pair(self):
        a = RouteLLMAdapter(_cfg("mf:0", base="http://127.0.0.1:1"))
        assert not a.is_tier()
        assert (a.tier, a.router_name, a.threshold) == ("", "mf", 0.0)

    def test_rejects_empty_model(self):
        with pytest.raises(
            llm.LLMError,
            match=r"^routellm: model field is required \(router_name:threshold or tier name\)$",
        ):
            RouteLLMAdapter(_cfg("", base="http://127.0.0.1:1"))

    @pytest.mark.parametrize(
        ("model", "msg"),
        [
            ("mf:-0.1", "routellm: threshold -0.1000 out of range [0, 1]"),
            ("mf:1.5", "routellm: threshold 1.5000 out of range [0, 1]"),
        ],
    )
    @pytest.mark.parametrize("base", ["http://127.0.0.1:1", ""])
    def test_rejects_threshold_out_of_range(self, model, msg, base):
        with pytest.raises(llm.ThresholdInvalidError) as ei:
            RouteLLMAdapter(_cfg(model, base=base))
        assert str(ei.value) == msg

    def test_v1_not_doubled(self):
        with serve(Canned.json(200, _OK)) as srv:
            RouteLLMAdapter(_cfg("private", base=srv.url + "/v1/")).complete(_hi())
            assert srv.only().path == "/v1/chat/completions"

    def test_bearer_key_sent(self):
        with serve(Canned.json(200, _OK)) as srv:
            RouteLLMAdapter(_cfg("private", base=srv.url, key="sk-r")).complete(_hi())
            assert srv.only().headers["authorization"] == "Bearer sk-r"

    def test_no_key_no_authorization(self):
        with serve(Canned.json(200, _OK)) as srv:
            RouteLLMAdapter(_cfg("private", base=srv.url)).complete(_hi())
            assert "authorization" not in srv.only().headers

    def test_key_over_plain_http_to_remote_refused(self):
        with pytest.raises(llm.KeyEndpointError, match="routellm"):
            RouteLLMAdapter(_cfg("private", base="http://router.example.test", key="k"))

    def test_errors_labelled_routellm(self):
        body = {"error": {"type": "invalid_request_error", "message": "unknown tier"}}
        with serve(Canned.json(404, body)) as srv:
            with pytest.raises(llm.ModelError) as ei:
                RouteLLMAdapter(_cfg("nope", base=srv.url)).complete(_hi())
            assert "routellm" in str(ei.value)

    def test_call_with_tools_sanitizes_and_maps_back(self):
        body = _completion(
            {
                "role": "assistant",
                "content": None,
                "tool_calls": [
                    {
                        "id": "c1",
                        "type": "function",
                        "function": {"name": "pane_open", "arguments": "{}"},
                    }
                ],
            },
            finish="tool_calls",
        )
        with serve(Canned.json(200, body)) as srv:
            a = RouteLLMAdapter(_cfg("private", base=srv.url))
            r = a.call_with_tools(_hi(), [llm.ToolDef(name="pane.open", description="")])
            sent = srv.only().json()
            assert sent["model"] == "private"
            assert sent["tools"][0]["function"]["name"] == "pane_open"
            assert [(c.id, c.name) for c in r.tool_calls] == [("c1", "pane.open")]
            assert r.usage == llm.Usage(prompt_tokens=3, completion_tokens=2, total_tokens=5)
            assert "call_with_tools" in llm.Client(a).capabilities()

    def test_strict_tool_names_param_passed_through(self):
        with serve(Canned.json(200, _OK)) as srv:
            a = RouteLLMAdapter(_cfg("private", base=srv.url, tool_names="strict"))
            with pytest.raises(llm.ToolNameError):
                a.call_with_tools(_hi(), [llm.ToolDef(name="pane.open", description="")])
            assert srv.attempts == 0

    def test_max_body_bytes_param_passed_through(self):
        with serve(Canned.json(200, _OK)) as srv:
            a = RouteLLMAdapter(_cfg("private", base=srv.url, max_body_bytes="8"))
            with pytest.raises(llm.ResponseTooLargeError):
                a.complete(_hi())

    def test_stream_with_tools(self):
        chunks = [
            {"choices": [{"delta": {"content": "ok"}}]},
            {
                "choices": [
                    {
                        "delta": {
                            "tool_calls": [
                                {
                                    "index": 0,
                                    "id": "c1",
                                    "function": {"name": "pane_open", "arguments": "{}"},
                                }
                            ]
                        },
                        "finish_reason": "tool_calls",
                    }
                ]
            },
        ]
        sse = "".join(f"data: {json.dumps(c)}\n\n" for c in chunks) + "data: [DONE]\n\n"
        with serve(Canned.sse(sse)) as srv:
            a = RouteLLMAdapter(_cfg("mf:0.7", base=srv.url))
            s = a.stream_with_tools(_hi(), [llm.ToolDef(name="pane.open", description="")])
            toks = list(s)
            assert [t.content for t in toks] == ["ok", ""]
            assert toks[-1].done
            sent = srv.only().json()
            assert (sent["model"], sent["stream"]) == ("router-mf-0.7", True)
            out = s.outcome()
            assert out is not None
            assert [(c.id, c.name) for c in out.tool_calls] == [("c1", "pane.open")]

    def test_stream_truncated(self):
        sse = f"data: {json.dumps({'choices': [{'delta': {'content': 'x'}}]})}\n\n"
        with serve(Canned.sse(sse)) as srv:
            s = RouteLLMAdapter(_cfg("private", base=srv.url)).stream(_hi())
            assert next(s).content == "x"
            with pytest.raises(llm.StreamTruncatedError):
                next(s)

    def test_eva_runs_after_complete(self):
        fake_eva = mock.MagicMock()
        fake_eva.validate.return_value = ["too short"]
        extras = {"eva": {"contract": "c1"}}
        with serve(Canned.json(200, _OK)) as srv:
            a = RouteLLMAdapter(_cfg("private", base=srv.url, extras=extras))
            with (
                mock.patch("hop_top_kit.routellm_adapter.eva", fake_eva),
                pytest.raises(llm.ContractViolationError),
            ):
                a.complete(_hi())
        fake_eva.validate.assert_called_once_with("c1", "ok")

    def test_resolve_tier_uri(self):
        with serve(Canned.json(200, _OK)) as srv:
            p = llm.resolve(f"routellm://private?base_url={srv.url}", hermetic=True)
            assert p.complete(_hi()).content == "ok"
            assert srv.only().json()["model"] == "private"

    def test_resolve_host_form(self):
        with serve(Canned.json(200, _OK)) as srv:
            host = srv.url.removeprefix("http://")
            p = llm.resolve(f"routellm://{host}/coding_fast", hermetic=True)
            p.complete(_hi())
            req = srv.only()
            assert (req.path, req.json()["model"]) == ("/v1/chat/completions", "coding_fast")


# ------------------------------------------------------------------
# Mode selection and base URL precedence (Go order)
# ------------------------------------------------------------------


def _in_process(cfg: llm.ResolvedConfig) -> tuple[RouteLLMAdapter, mock.MagicMock]:
    ctor = mock.MagicMock()
    with mock.patch("hop_top_kit.routellm_adapter.Controller", ctor):
        return RouteLLMAdapter(cfg), ctor


class TestModeSelection:
    def test_no_base_url_is_in_process(self):
        a, ctor = _in_process(_cfg("mf:0.7"))
        assert (a.mode, a.base_url) == ("in_process", "")
        ctor.assert_called_once()
        assert ctor.call_args.kwargs["api_base"] is None

    def test_go_default_url_does_not_select_http(self):
        # parse_router_config defaults base_url to localhost:6060; only an
        # explicitly configured URL selects HTTP mode.
        assert llm.parse_router_config({}).base_url == "http://localhost:6060"
        a, _ = _in_process(_cfg("mf:0.7", extras={"routellm": {"strong_model": "s"}}))
        assert a.mode == "in_process"

    def test_in_process_api_base_from_extras(self):
        a, ctor = _in_process(_cfg("mf:0.7", extras={"api_base": "https://up.test/v1"}))
        assert a.mode == "in_process"
        assert ctor.call_args.kwargs["api_base"] == "https://up.test/v1"

    def test_in_process_tier_needs_server(self):
        with pytest.raises(llm.LLMError, match=r'^routellm model "private" is a tier name') as ei:
            _in_process(_cfg("private"))
        assert "ROUTELLM_BASE_URL" in str(ei.value)

    def test_in_process_tier_error_before_missing_dep(self):
        with (
            mock.patch("hop_top_kit.routellm_adapter.Controller", None),
            pytest.raises(llm.LLMError, match="is a tier name"),
        ):
            RouteLLMAdapter(_cfg("private"))

    def test_http_mode_needs_no_routellm_package(self):
        with mock.patch("hop_top_kit.routellm_adapter.Controller", None):
            a = RouteLLMAdapter(_cfg("private", base="http://127.0.0.1:1"))
        assert a.mode == "http"

    def test_extras_base_url_selects_http(self):
        with serve(Canned.json(200, _OK)) as srv:
            a = RouteLLMAdapter(_cfg("private", extras={"routellm": {"base_url": srv.url}}))
            assert (a.mode, a.base_url) == ("http", srv.url)
            a.complete(_hi())
            assert srv.attempts == 1

    def test_env_base_url_selects_http(self, monkeypatch):
        with serve(Canned.json(200, _OK)) as srv:
            monkeypatch.setenv("ROUTELLM_BASE_URL", srv.url)
            a = RouteLLMAdapter(_cfg("private"))
            assert (a.mode, a.base_url) == ("http", srv.url)
            a.complete(_hi())
            assert srv.attempts == 1

    def test_provider_outranks_env_and_extras(self, monkeypatch):
        with serve(Canned.json(200, _OK)) as want, serve(Canned.json(200, _OK)) as other:
            monkeypatch.setenv("ROUTELLM_BASE_URL", other.url)
            extras = {"routellm": {"base_url": other.url}}
            RouteLLMAdapter(_cfg("private", base=want.url, extras=extras)).complete(_hi())
            assert (want.attempts, other.attempts) == (1, 0)

    def test_env_outranks_extras(self, monkeypatch):
        with serve(Canned.json(200, _OK)) as want, serve(Canned.json(200, _OK)) as other:
            monkeypatch.setenv("ROUTELLM_BASE_URL", want.url)
            extras = {"routellm": {"base_url": other.url}}
            RouteLLMAdapter(_cfg("private", extras=extras)).complete(_hi())
            assert (want.attempts, other.attempts) == (1, 0)

    def test_hermetic_ignores_env_in_process(self, monkeypatch):
        monkeypatch.setenv("ROUTELLM_BASE_URL", "http://127.0.0.1:1")
        a, _ = _in_process(_cfg("mf:0.7", hermetic=True))
        assert a.mode == "in_process"

    def test_hermetic_ignores_env_tier_needs_server(self, monkeypatch):
        monkeypatch.setenv("ROUTELLM_BASE_URL", "http://127.0.0.1:1")
        with pytest.raises(llm.LLMError, match="is a tier name"):
            _in_process(_cfg("private", hermetic=True))

    def test_hermetic_uses_extras_not_env(self, monkeypatch):
        with serve(Canned.json(200, _OK)) as want, serve(Canned.json(200, _OK)) as other:
            monkeypatch.setenv("ROUTELLM_BASE_URL", other.url)
            extras = {"routellm": {"base_url": want.url}}
            RouteLLMAdapter(_cfg("private", extras=extras, hermetic=True)).complete(_hi())
            assert (want.attempts, other.attempts) == (1, 0)

    def test_non_map_router_extras_rejected(self):
        with pytest.raises(llm.LLMError, match="routellm: expected dict"):
            RouteLLMAdapter(_cfg("private", base="http://127.0.0.1:1", extras={"routellm": "x"}))
