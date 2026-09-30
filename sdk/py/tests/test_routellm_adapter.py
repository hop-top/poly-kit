"""Tests for hop_top_kit.routellm_adapter."""

from __future__ import annotations

from unittest import mock

import pytest

from hop_top_kit import llm

# ------------------------------------------------------------------
# URI parsing
# ------------------------------------------------------------------


class TestParseRouterThreshold:
    def test_basic(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        router, thresh = parse_router_threshold("mf:0.7")
        assert router == "mf"
        assert thresh == pytest.approx(0.7)

    def test_zero_threshold(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        router, thresh = parse_router_threshold("bert:0.0")
        assert router == "bert"
        assert thresh == 0.0

    def test_one_threshold(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        _, thresh = parse_router_threshold("sw_ranking:1.0")
        assert thresh == 1.0

    def test_missing_colon_raises(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        with pytest.raises(llm.LLMError, match="router:threshold"):
            parse_router_threshold("mf0.7")

    def test_invalid_float_raises(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        with pytest.raises(llm.LLMError, match="not a valid float"):
            parse_router_threshold("mf:abc")

    def test_out_of_range_raises(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        with pytest.raises(llm.ThresholdInvalidError):
            parse_router_threshold("mf:1.5")

    def test_negative_raises(self):
        from hop_top_kit.routellm_adapter import parse_router_threshold

        with pytest.raises(llm.ThresholdInvalidError):
            parse_router_threshold("mf:-0.1")


# ------------------------------------------------------------------
# Factory error when routellm not installed
# ------------------------------------------------------------------


class TestFactoryMissingDep:
    def test_raises_import_error(self):
        """Factory raises ImportError when routellm is absent."""
        from hop_top_kit.routellm_adapter import RouteLLMAdapter

        cfg = llm.ResolvedConfig(
            uri=llm.URI(scheme="routellm", model="mf:0.5"),
            provider=llm.ProviderConfig(extras={}),
        )

        # Patch Controller to None to simulate missing dep
        with (
            mock.patch("hop_top_kit.routellm_adapter.Controller", None),
            pytest.raises(ImportError, match="routellm is not installed"),
        ):
            RouteLLMAdapter(cfg)


# ------------------------------------------------------------------
# Scheme registration
# ------------------------------------------------------------------


class TestRegistration:
    def test_routellm_scheme_registered(self):
        """Importing the adapter registers the routellm scheme."""
        import hop_top_kit.routellm_adapter as mod

        # Re-register in case other tests cleared the registry.
        mod._register()
        assert "routellm" in llm._registry

    def test_double_import_no_error(self):
        """Re-importing does not raise on duplicate registration."""
        import hop_top_kit.routellm_adapter as mod

        # Call _register again explicitly — should be idempotent.
        mod._register()
        assert "routellm" in llm._registry


# ------------------------------------------------------------------
# Wire mapping: parts + tool-call linkage (OpenAI chat shape)
# ------------------------------------------------------------------


def _adapter(controller: mock.MagicMock):
    from hop_top_kit.routellm_adapter import RouteLLMAdapter

    cfg = llm.ResolvedConfig(
        uri=llm.URI(scheme="routellm", model="mf:0.7"),
        provider=llm.ProviderConfig(extras={}),
    )
    with mock.patch("hop_top_kit.routellm_adapter.Controller", return_value=controller):
        return RouteLLMAdapter(cfg)


def _completion_result() -> mock.MagicMock:
    result = mock.MagicMock()
    result.choices[0].message.content = "ok"
    result.choices[0].finish_reason = "stop"
    result.usage = None
    return result


def _stream_result() -> list[mock.MagicMock]:
    chunk = mock.MagicMock()
    chunk.choices[0].delta.content = "ok"
    chunk.choices[0].finish_reason = "stop"
    return [chunk]


def _tool_round() -> list[llm.Message]:
    return [
        llm.Message(role="user", content="Weather?"),
        llm.Message(
            role="assistant",
            tool_calls=[
                llm.ToolCall(id="call_1", name="get_weather", arguments='{"city":"NYC"}'),
            ],
        ),
        llm.Message(role="tool", content="sunny", tool_call_id="call_1"),
    ]


_TOOL_ROUND_WIRE = [
    {"role": "user", "content": "Weather?"},
    {
        "role": "assistant",
        "tool_calls": [
            {
                "id": "call_1",
                "type": "function",
                "function": {"name": "get_weather", "arguments": '{"city":"NYC"}'},
            }
        ],
    },
    {"role": "tool", "content": "sunny", "tool_call_id": "call_1"},
]


def _sent_messages(controller: mock.MagicMock) -> list[dict]:
    return controller.completion.call_args.kwargs["messages"]


class TestWireToolLinkage:
    def test_complete_sends_tool_calls_and_tool_call_id(self):
        controller = mock.MagicMock()
        controller.completion.return_value = _completion_result()
        _adapter(controller).complete(llm.Request(messages=_tool_round()))
        assert _sent_messages(controller) == _TOOL_ROUND_WIRE

    def test_stream_sends_tool_calls_and_tool_call_id(self):
        controller = mock.MagicMock()
        controller.completion.return_value = _stream_result()
        list(_adapter(controller).stream(llm.Request(messages=_tool_round())))
        assert _sent_messages(controller) == _TOOL_ROUND_WIRE

    def test_plain_messages_map_as_before(self):
        controller = mock.MagicMock()
        controller.completion.return_value = _completion_result()
        msgs = [llm.Message("system", "be brief"), llm.Message("user", "hi")]
        _adapter(controller).complete(llm.Request(messages=msgs))
        assert _sent_messages(controller) == [
            {"role": "system", "content": "be brief"},
            {"role": "user", "content": "hi"},
        ]


class TestMapMessages:
    """Direct tests of the request builder; no RouteLLM install needed."""

    @staticmethod
    def _map(msgs: list[llm.Message]) -> list[dict]:
        from hop_top_kit.routellm_adapter import map_messages

        return map_messages(msgs)

    def test_assistant_text_kept_alongside_calls(self):
        out = self._map(
            [
                llm.Message(
                    role="assistant",
                    content="Checking.",
                    tool_calls=[llm.ToolCall(id="c1", name="f", arguments="{}")],
                )
            ]
        )
        assert out[0]["content"] == "Checking."

    def test_empty_content_omitted_on_tool_call_turn(self):
        out = self._map(
            [llm.Message(role="assistant", tool_calls=[llm.ToolCall(id="c1", name="f")])]
        )
        assert "content" not in out[0]

    @pytest.mark.parametrize("args", [None, "", b""])
    def test_empty_arguments_become_empty_object(self, args):
        out = self._map(
            [
                llm.Message(
                    role="assistant", tool_calls=[llm.ToolCall(id="c1", name="f", arguments=args)]
                )
            ]
        )
        assert out[0]["tool_calls"][0]["function"]["arguments"] == "{}"

    def test_bytes_arguments_sent_as_string(self):
        out = self._map(
            [
                llm.Message(
                    role="assistant",
                    tool_calls=[llm.ToolCall(id="c1", name="f", arguments=b'{"a":1}')],
                )
            ]
        )
        assert out[0]["tool_calls"][0]["function"]["arguments"] == '{"a":1}'

    def test_structured_arguments_serialized_to_json_string(self):
        out = self._map(
            [
                llm.Message(
                    role="assistant",
                    tool_calls=[llm.ToolCall(id="c1", name="f", arguments={"city": "NYC"})],
                )
            ]
        )
        assert out[0]["tool_calls"][0]["function"]["arguments"] == '{"city":"NYC"}'

    def test_multiple_results_one_message_each(self):
        out = self._map(
            [
                llm.Message(role="tool", content="a", tool_call_id="c1"),
                llm.Message(role="tool", content="b", tool_call_id="c2"),
            ]
        )
        assert [m["tool_call_id"] for m in out] == ["c1", "c2"]

    @pytest.mark.parametrize(
        ("msg", "match"),
        [
            (llm.Message(role="tool", content="x"), "tool result needs tool_call_id"),
            (
                llm.Message(
                    role="tool",
                    tool_call_id="c1",
                    tool_calls=[llm.ToolCall(id="c1", name="f")],
                ),
                "carries only content",
            ),
            (
                llm.Message(
                    role="tool",
                    tool_call_id="c1",
                    parts=[llm.ContentPart(type=llm.PartType.TEXT, text="x")],
                ),
                "carries only content",
            ),
            (
                llm.Message(role="user", content="x", tool_call_id="c1"),
                "tool_call_id is valid only on role tool",
            ),
            (
                llm.Message(role="user", tool_calls=[llm.ToolCall(id="c1", name="f")]),
                "tool_calls are valid only on role assistant",
            ),
            (
                llm.Message(
                    role="assistant",
                    tool_calls=[llm.ToolCall(id="c1", name="f")],
                    parts=[llm.ContentPart(type=llm.PartType.TEXT, text="x")],
                ),
                "do not support content parts",
            ),
            (
                llm.Message(role="assistant", tool_calls=[llm.ToolCall(id="", name="f")]),
                "needs an id",
            ),
            (
                llm.Message(
                    role="assistant",
                    tool_calls=[llm.ToolCall(id="c1", name="f", arguments="{not json")],
                ),
                "not valid JSON",
            ),
        ],
    )
    def test_invalid_linkage_raises_instead_of_degrading(self, msg, match):
        with pytest.raises(llm.LLMError, match=match):
            self._map([msg])


class TestMapParts:
    @staticmethod
    def _map(msgs: list[llm.Message]) -> list[dict]:
        from hop_top_kit.routellm_adapter import map_messages

        return map_messages(msgs)

    def test_parts_take_precedence_over_content(self):
        out = self._map(
            [
                llm.Message(
                    role="user",
                    content="ignored",
                    parts=[
                        llm.ContentPart(type=llm.PartType.TEXT, text="look"),
                        llm.ContentPart(
                            type=llm.PartType.IMAGE,
                            source=llm.URLSource("https://example.com/cat.png"),
                        ),
                    ],
                )
            ]
        )
        assert out == [
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": "look"},
                    {"type": "image_url", "image_url": {"url": "https://example.com/cat.png"}},
                ],
            }
        ]

    def test_empty_role_with_parts_is_user(self):
        out = self._map(
            [llm.Message(role="", parts=[llm.ContentPart(type=llm.PartType.TEXT, text="x")])]
        )
        assert out[0]["role"] == "user"

    def test_inline_image_becomes_data_uri(self):
        out = self._map(
            [
                llm.Message(
                    role="user",
                    parts=[
                        llm.ContentPart(
                            type=llm.PartType.IMAGE,
                            source=llm.InlineSource(b"abc", "image/png"),
                        )
                    ],
                )
            ]
        )
        assert out[0]["content"][0] == {
            "type": "image_url",
            "image_url": {"url": "data:image/png;base64,YWJj"},
        }

    def test_part_mime_overrides_source_mime(self):
        out = self._map(
            [
                llm.Message(
                    role="user",
                    parts=[
                        llm.ContentPart(
                            type=llm.PartType.IMAGE,
                            source=llm.InlineSource(b"abc", "image/png"),
                            mime_type="image/jpeg",
                        )
                    ],
                )
            ]
        )
        assert out[0]["content"][0]["image_url"]["url"].startswith("data:image/jpeg;base64,")

    def test_pdf_becomes_file_part(self):
        out = self._map(
            [
                llm.Message(
                    role="user",
                    parts=[
                        llm.ContentPart(
                            type=llm.PartType.IMAGE,
                            source=llm.InlineSource(b"%PDF", "application/pdf"),
                        )
                    ],
                )
            ]
        )
        assert out[0]["content"][0] == {"type": "file", "file": {"file_data": "JVBERg=="}}

    @pytest.mark.parametrize(
        ("msg", "match"),
        [
            (
                llm.Message(
                    role="assistant", parts=[llm.ContentPart(type=llm.PartType.TEXT, text="x")]
                ),
                "does not support content parts",
            ),
            (
                llm.Message(
                    role="user",
                    parts=[
                        llm.ContentPart(type=llm.PartType.IMAGE, source=llm.InlineSource(b"x", ""))
                    ],
                ),
                "no MIME type",
            ),
            (
                llm.Message(
                    role="user",
                    parts=[llm.ContentPart(type=llm.PartType.IMAGE, mime_type="image/png")],
                ),
                "no media source",
            ),
            (
                llm.Message(
                    role="user",
                    parts=[
                        llm.ContentPart(
                            type=llm.PartType.AUDIO, source=llm.InlineSource(b"x", "audio/wav")
                        )
                    ],
                ),
                "unsupported modality",
            ),
        ],
    )
    def test_invalid_parts_raise(self, msg, match):
        with pytest.raises(llm.LLMError, match=match):
            self._map([msg])

    def test_complete_forwards_parts(self):
        controller = mock.MagicMock()
        controller.completion.return_value = _completion_result()
        msg = llm.Message(role="user", parts=[llm.ContentPart(type=llm.PartType.TEXT, text="x")])
        _adapter(controller).complete(llm.Request(messages=[msg]))
        assert _sent_messages(controller)[0]["content"] == [{"type": "text", "text": "x"}]

    def test_stream_forwards_parts(self):
        controller = mock.MagicMock()
        controller.completion.return_value = _stream_result()
        msg = llm.Message(role="user", parts=[llm.ContentPart(type=llm.PartType.TEXT, text="x")])
        list(_adapter(controller).stream(llm.Request(messages=[msg])))
        assert _sent_messages(controller)[0]["content"] == [{"type": "text", "text": "x"}]
