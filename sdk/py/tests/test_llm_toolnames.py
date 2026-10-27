"""Tests for hop_top_kit.llm_toolnames beyond the shared vectors."""

from __future__ import annotations

import hashlib

import pytest

from hop_top_kit import llm
from hop_top_kit.llm_toolnames import (
    ANTHROPIC_TOOL_NAMES,
    GEMINI_TOOL_NAMES,
    OPENAI_TOOL_NAMES,
    TOOL_NAMES_PARAM,
    ToolNameMode,
    ToolNames,
    tool_name_mode_of,
)


def _defs(*names: str) -> list[llm.ToolDef]:
    return [llm.ToolDef(name=n, description="") for n in names]


class TestSanitize:
    def test_run_of_disallowed_bytes_is_one_underscore(self):
        assert OPENAI_TOOL_NAMES.sanitize("a.:/b") == "a_b"
        assert OPENAI_TOOL_NAMES.sanitize("a.b.c") == "a_b_c"

    def test_hash_suffix_is_8_hex_of_sha256(self):
        name = "a" * 65
        h = hashlib.sha256(name.encode()).hexdigest()[:8]
        got = OPENAI_TOOL_NAMES.sanitize(name)
        assert got == "a" * 55 + "_" + h
        assert len(got) == 64

    def test_spec_examples(self):
        assert OPENAI_TOOL_NAMES.sanitize("pane.open") == "pane_open"
        assert OPENAI_TOOL_NAMES.sanitize("mcp://fs/read_file") == "mcp_fs_read_file"
        assert OPENAI_TOOL_NAMES.sanitize("café.menu") == "caf_menu"
        assert OPENAI_TOOL_NAMES.sanitize("検索") == "__1b6b1a6f"
        assert GEMINI_TOOL_NAMES.sanitize("pane.open") == "pane.open"
        assert GEMINI_TOOL_NAMES.sanitize("1tool") == "_1tool"

    def test_empty_is_an_error(self):
        with pytest.raises(llm.ToolNameError) as ei:
            ANTHROPIC_TOOL_NAMES.sanitize("")
        assert ei.value.reason == llm.ToolNameReason.EMPTY

    def test_lone_surrogate_is_invalid_utf8(self):
        with pytest.raises(llm.ToolNameError) as ei:
            ANTHROPIC_TOOL_NAMES.sanitize("a\ud800b")
        assert ei.value.reason == "invalid_utf8"
        assert ei.value.name == "a\ud800b"
        assert ei.value.provider == "anthropic"


class TestToolNames:
    def test_invalid_utf8_fails_the_request(self):
        for mode in ToolNameMode:
            with pytest.raises(llm.ToolNameError) as ei:
                ToolNames.build(ANTHROPIC_TOOL_NAMES, mode, _defs("ok", "\udcff"))
            assert ei.value.reason == "invalid_utf8"

    def test_defs_go_out_under_wire_names_and_keep_fields(self):
        defs = [llm.ToolDef(name="pane.open", description="d", parameters={"type": "object"})]
        names = ToolNames.build(ANTHROPIC_TOOL_NAMES, ToolNameMode.SANITIZE, defs)
        out = names.defs(defs)
        assert out == [
            llm.ToolDef(name="pane_open", description="d", parameters={"type": "object"})
        ]
        assert defs[0].name == "pane.open", "input not modified"

    def test_calls_carry_original_names(self):
        names = ToolNames.build(ANTHROPIC_TOOL_NAMES, ToolNameMode.SANITIZE, _defs("pane.open"))
        calls = [llm.ToolCall(id="c1", name="pane_open", arguments="{}")]
        assert names.calls(calls) == [llm.ToolCall(id="c1", name="pane.open", arguments="{}")]
        assert calls[0].name == "pane_open", "input not modified"

    def test_history_name_without_def(self):
        sanitize = ToolNames.build(OPENAI_TOOL_NAMES, ToolNameMode.SANITIZE, [])
        strict = ToolNames.build(OPENAI_TOOL_NAMES, ToolNameMode.STRICT, [])
        assert sanitize.wire("files.read") == "files_read"
        assert strict.wire("files.read") == "files.read"
        assert sanitize.wire("") == "", "a name sanitize refuses goes out unchanged"
        assert sanitize.wire("\ud800") == "\ud800"

    def test_error_messages(self):
        cases = [
            (("anthropic", "empty", "", ""), "tool name is empty (provider 'anthropic')"),
            (
                ("openai", "collision", "pane:open", "pane.open"),
                "tool names 'pane.open' and 'pane:open' map to the same provider name "
                "(provider 'openai')",
            ),
            (
                ("openai", "invalid", "a.b", ""),
                "tool name 'a.b' is not valid for provider 'openai'",
            ),
            (
                ("openai", "duplicate", "x", ""),
                "tool name 'x' is defined twice (provider 'openai')",
            ),
        ]
        for args, msg in cases:
            assert str(llm.ToolNameError(*args)) == msg

    def test_not_fallbackable(self):
        err = llm.ToolNameError("anthropic", "collision", "a_b", "a.b")
        assert isinstance(err, llm.LLMError)
        assert not llm.is_fallbackable(err)


class TestMode:
    def test_default_is_sanitize(self):
        assert tool_name_mode_of({}) is ToolNameMode.SANITIZE

    @pytest.mark.parametrize("raw", ["sanitize", "strict", " strict "])
    def test_known(self, raw):
        assert tool_name_mode_of({TOOL_NAMES_PARAM: raw}) == raw.strip()

    @pytest.mark.parametrize("raw", ["", "lenient", "STRICT"])
    def test_unknown_fails(self, raw):
        with pytest.raises(llm.LLMError, match="tool_names must be"):
            tool_name_mode_of({TOOL_NAMES_PARAM: raw})
