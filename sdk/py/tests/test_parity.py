"""Drift guards for the parity contract loader.

Mirrors ``TestParityNoUnloadedBlocks`` and ``TestParityLoadedBlocksNonZero``
in ``contracts/parity/parity_test.go``.

``contracts/parity/parity.json`` is a loaded contract, not documentation. A
block declared there but not modelled by ``hop_top_kit.parity`` is invisible
to every test and every consumer — which is exactly how verbosity, streams
and table sat unenforced. These tests fail in both directions so a new block
cannot be added as decoration.
"""

from __future__ import annotations

import json

from hop_top_kit import parity


def _declared_blocks() -> list[str]:
    """Top-level parity.json keys, minus ``$``-prefixed schema metadata."""
    raw: dict = json.loads(parity.PARITY_PATH.read_text())
    return [k for k in raw if not k.startswith("$")]


class TestParityNoUnloadedBlocks:
    """The block registry must match parity.json exactly, both directions."""

    def test_models_every_declared_block(self) -> None:
        unknown = [k for k in _declared_blocks() if k not in parity.BLOCKS]
        assert not unknown, (
            f"contracts/parity/parity.json declares block(s) {unknown} that the Python "
            f"loader does not know.\n"
            f"parity.json is a loaded contract, not documentation: an unloaded block is "
            f"invisible to every test and every consumer.\n"
            f"Fix by adding an accessor for each block and its name to BLOCKS in "
            f"sdk/py/hop_top_kit/parity.py — or, if it is not a cross-language constant, "
            f"move it to prose in contracts/parity/README.md."
        )

    def test_registry_has_no_stale_entries(self) -> None:
        declared = set(_declared_blocks())
        stale = [b for b in parity.BLOCKS if b not in declared]
        assert not stale, (
            f"BLOCKS lists {stale} but contracts/parity/parity.json no longer declares "
            f"it; drop it from BLOCKS in sdk/py/hop_top_kit/parity.py."
        )


class TestParityLoadedBlocksNonZero:
    """Every known block must actually load a value.

    A block read under a renamed key parses clean and leaves the accessor's
    empty default behind — silent, and exactly what this contract exists to
    prevent. Asserting the key was SEEN is not enough; assert the value
    actually arrived.
    """

    def test_status_loaded(self) -> None:
        assert parity.STATUS_SYMBOLS, "status.symbols: empty after load"
        for kind in ("info", "success", "error", "warn"):
            assert parity.STATUS_SYMBOLS.get(kind), f"status.symbols[{kind!r}]: empty after load"

    def test_spinner_loaded(self) -> None:
        assert parity.SPINNER_FRAMES, "spinner.frames: empty after load"
        assert parity.SPINNER_INTERVAL_MS > 0, (
            f"spinner: incompletely loaded: interval_ms={parity.SPINNER_INTERVAL_MS}"
        )

    def test_anim_loaded(self) -> None:
        assert parity.ANIM_RUNES, "anim.runes: empty after load"
        assert parity.ANIM_INTERVAL_MS > 0 and parity.ANIM_DEFAULT_WIDTH > 0, (
            f"anim: incompletely loaded: interval_ms={parity.ANIM_INTERVAL_MS} "
            f"default_width={parity.ANIM_DEFAULT_WIDTH}"
        )

    def test_help_loaded(self) -> None:
        assert parity.HELP_SECTION_ORDER, "help.section_order: empty after load"
        assert parity.HELP_SECTIONS, "help.sections: empty after load"
        for section in parity.HELP_SECTION_ORDER:
            assert section in parity.HELP_SECTIONS, f"help.sections[{section!r}]: missing"

    def test_verbosity_loaded(self) -> None:
        assert (
            parity.VERBOSITY_FLAG and parity.VERBOSITY_LEVELS and (parity.VERBOSITY_QUIET_OVERRIDE)
        ), (
            f"verbosity: incompletely loaded: flag={parity.VERBOSITY_FLAG!r} "
            f"levels={parity.VERBOSITY_LEVELS!r} "
            f"quiet_override={parity.VERBOSITY_QUIET_OVERRIDE!r}"
        )

    def test_streams_loaded(self) -> None:
        assert parity.STREAMS_FLAG and parity.STREAMS_LABEL_FORMAT and parity.STREAMS_OUTPUT, (
            f"streams: incompletely loaded: flag={parity.STREAMS_FLAG!r} "
            f"label_format={parity.STREAMS_LABEL_FORMAT!r} "
            f"output={parity.STREAMS_OUTPUT!r}"
        )

    def test_description_and_extends_loaded(self) -> None:
        assert parity.PARITY.get("description"), "description: empty after load"
        assert parity.EXTENDS, "extends: empty after load"


class TestParityPinnedValues:
    """Pin the values each port reproduces, so a change fails rather than diverges."""

    def test_status_symbols(self) -> None:
        # Escapes, not literals: the info glyph U+2139 is visually ambiguous
        # with ASCII "i", and pinning it by codepoint keeps the intent exact.
        assert parity.STATUS_SYMBOLS == {
            "info": "\u2139",  # INFORMATION SOURCE
            "success": "\u2713",  # CHECK MARK
            "error": "\u25cf",  # BLACK CIRCLE
            "warn": "\u25b2",  # BLACK UP-POINTING TRIANGLE
        }

    def test_verbosity(self) -> None:
        assert parity.VERBOSITY_FLAG == "-V"
        assert parity.VERBOSITY_LEVELS == {"0": "info", "1": "debug", "2": "trace"}
        assert parity.VERBOSITY_QUIET_OVERRIDE == "warn"

    def test_streams(self) -> None:
        assert parity.STREAMS_FLAG == "--stream"
        assert parity.STREAMS_LABEL_FORMAT == "[{name}]"
        assert parity.STREAMS_OUTPUT == "stderr"
