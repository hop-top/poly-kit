"""Cross-language exit-taxonomy contract loader.

Loads ``contracts/exit-taxonomy-v1/taxonomy.json`` — generated from
``go/console/output/envelope``, the single source of truth — and asserts
this Python SDK's table agrees with it on every class, every exit number
and every transience.

A failure here means Go's taxonomy moved and this port did not follow.
That is the exact drift this contract exists to catch: when Go gained
``CONSENT_REFUSED`` 7 and ``PREREQUISITE`` 70, all four SDK ports
silently kept the old nine-class table for two releases, and
``transience_for_code`` returned ``unknown`` where Go returned
``transient`` — so four runtimes handed agents wrong retry guidance.

Fix by adding the missing class here, not by editing the contract: the
contract is regenerated from Go, never hand-edited.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from hop_top_kit.output.error import (
    exit_classes,
    exit_code_for_class,
    transience_for_code,
)

# ---------------------------------------------------------------------------
# Contract loader
# ---------------------------------------------------------------------------

#: The slots envelope itself owns. The conformance-tree slots (66-69) are
#: recorded in the contract so a new allocation cannot double-book a
#: number, but no SDK port is expected to export them.
ENVELOPE_OWNER = "hop.top/kit/go/console/output/envelope"


def _locate_contract() -> Path:
    """Walk up from this test file until the taxonomy contract appears.

    Going through ``__file__`` (not CWD) means the loader works under
    both ``pytest`` (CWD = sdk/py) and editor / IDE invocations whose
    CWD may vary.
    """

    here = Path(__file__).resolve().parent
    for candidate in (here, *here.parents):
        target = candidate / "contracts" / "exit-taxonomy-v1" / "taxonomy.json"
        if target.is_file():
            return target
    raise FileNotFoundError(
        f"contracts/exit-taxonomy-v1/taxonomy.json: not found walking up from {here}"
    )


CONTRACT: dict = json.loads(_locate_contract().read_text(encoding="utf-8"))
CLASSES: list[dict] = CONTRACT["classes"]
BAND: list[dict] = CONTRACT["extension_band"]


def _class_id(row: dict) -> str:
    return str(row["class"])


# ---------------------------------------------------------------------------
# Metadata
# ---------------------------------------------------------------------------


def test_contract_metadata() -> None:
    assert CONTRACT["version"] == "v1"
    assert CONTRACT["source"] == ENVELOPE_OWNER
    assert len(CLASSES) > 0


# ---------------------------------------------------------------------------
# This port matches the contract
# ---------------------------------------------------------------------------


def test_declares_exactly_the_contract_classes() -> None:
    """Membership in both directions.

    A port carrying an extra class Go does not define is as much a
    divergence as a port missing one: an adopter branching on it gets a
    number no other runtime produces.
    """

    assert exit_classes() == [row["class"] for row in CLASSES]


@pytest.mark.parametrize("row", CLASSES, ids=_class_id)
def test_class_resolves_to_contract_exit_and_transience(row: dict) -> None:
    assert exit_code_for_class(row["class"]) == row["exit"]
    assert transience_for_code(row["class"]) == row["transience"]


@pytest.mark.parametrize("row", CLASSES, ids=_class_id)
def test_transience_vocabulary_is_the_contract_vocabulary(row: dict) -> None:
    """An agent branching on a fourth string has no defined behavior."""

    assert transience_for_code(row["class"]) in CONTRACT["transiences"]


def test_does_not_invent_a_code_for_an_adopter_class() -> None:
    """An unknown class resolves to nothing, not to a fallback number.

    A built-in fallback is what turns "kit added a class and this port
    never copied it" into an assertion against exit 1 that reads as a
    real failure rather than a stale table.
    """

    assert exit_code_for_class("ADOPTER_SPECIFIC") is None
    assert transience_for_code("ADOPTER_SPECIFIC") == "unknown"


# ---------------------------------------------------------------------------
# Extension band
# ---------------------------------------------------------------------------


def test_exports_every_band_slot_envelope_owns() -> None:
    owned = [s for s in BAND if s["owner"] == ENVELOPE_OWNER]
    assert owned
    for slot in owned:
        assert exit_code_for_class(slot["class"]) == slot["exit"]


def test_leaves_foreign_band_slots_unclaimed() -> None:
    """A port exporting LEAK_DETECTED would mint a number the
    conformance tree owns, which is the collision the band record
    exists to stop.
    """

    foreign = [s for s in BAND if s["owner"] != ENVELOPE_OWNER]
    assert foreign
    for slot in foreign:
        assert exit_code_for_class(slot["class"]) is None
