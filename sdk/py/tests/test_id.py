"""Tests for hop_top_kit.id — TypeID primitive (ADR 0001).

Intentionally does NOT use ``from __future__ import annotations``:
``TypeId["task"]`` is a *runtime* expression that returns a generated
Pydantic field class, not a forward reference. With PEP 563 string
annotations the literal would be treated as an unresolved name.
"""

from typing import Literal
from uuid import UUID

import pytest
from pydantic import BaseModel, ValidationError
from typeid import TypeID

from hop_top_kit.id import (
    IdError,
    InvalidPrefixError,
    InvalidSuffixError,
    Parsed,
    PrefixMismatchError,
    Typed,
    TypeId,
    new,
    parse,
)

# ---------------------------------------------------------------------------
# Canonical fixture set — see ADR 0001 / T-0751.
#
# These exact (prefix, uuidv7) pairs are the source of truth for cross-
# language parity work in T-0753. Do not change without bumping the
# parity-fixture version across all five SDKs.
# ---------------------------------------------------------------------------

FIXTURES: list[tuple[str, str]] = [
    ("task", "01940000-0000-7000-8000-000000000000"),
    ("invoice", "01940000-0000-7000-8000-000000000001"),
    ("user", "01940000-0000-7000-8000-0000000000ff"),
]


def _fixture_typeid(prefix: str, uuid_hex: str) -> str:
    """Produce the canonical TypeID string for a (prefix, uuid) pair.

    Uses the upstream ``TypeID.from_uuid`` constructor — the same path
    every kit-using language is expected to expose for fixture
    generation.
    """
    return str(TypeID.from_uuid(suffix=UUID(uuid_hex), prefix=prefix))


# ---------------------------------------------------------------------------
# new()
# ---------------------------------------------------------------------------


class TestNew:
    def test_returns_canonical_string_with_prefix(self):
        tid = new("task")
        assert isinstance(tid, str)
        assert tid.startswith("task_")
        # 26-char Crockford base32 suffix
        assert len(tid.split("_")[-1]) == 26

    def test_two_calls_produce_distinct_ids(self):
        a = new("user")
        b = new("user")
        assert a != b

    def test_invalid_prefix_raises_invalid_prefix_error(self):
        with pytest.raises(InvalidPrefixError):
            new("Bad-Prefix")  # uppercase + hyphen → invalid

    def test_invalid_prefix_is_an_id_error(self):
        with pytest.raises(IdError):
            new("1leading_digit")


# ---------------------------------------------------------------------------
# parse()
# ---------------------------------------------------------------------------


class TestParse:
    @pytest.mark.parametrize(("prefix", "uuid_hex"), FIXTURES)
    def test_round_trip_from_canonical_fixture(self, prefix, uuid_hex):
        canonical = _fixture_typeid(prefix, uuid_hex)

        result = parse(canonical)

        assert isinstance(result, Parsed)
        assert result.prefix == prefix
        assert isinstance(result.uuid, UUID)
        assert result.uuid == UUID(uuid_hex)

    def test_parse_then_regenerate_round_trip(self):
        original = new("invoice")
        parsed = parse(original)
        # Re-encoding the parsed components must yield the same string.
        rebuilt = str(TypeID.from_uuid(suffix=parsed.uuid, prefix=parsed.prefix))
        assert rebuilt == original

    def test_invalid_suffix_raises_invalid_suffix_error(self):
        with pytest.raises(InvalidSuffixError):
            parse("task_not_a_real_suffix")

    def test_empty_string_raises_invalid_suffix_error(self):
        with pytest.raises(InvalidSuffixError):
            parse("")

    def test_invalid_prefix_in_string_raises_id_error(self):
        # Build a string with a structurally invalid prefix segment.
        # Upstream may surface either prefix or suffix validation —
        # either way the kit promise is that it's an IdError.
        with pytest.raises(IdError):
            parse("BAD_01h45ytscbebyvny4gc8tyutss")


# ---------------------------------------------------------------------------
# Typed[P]
# ---------------------------------------------------------------------------


class TestTyped:
    def test_subscriptable(self):
        # No exception → annotation usage works at runtime.
        alias = Typed[str]
        assert alias is not None

    def test_value_is_just_a_string(self):
        # Typed[P] is a phantom alias; values flowing through are plain
        # canonical strings.
        tid: Typed[str] = new("task")  # type: ignore[assignment]
        assert isinstance(tid, str)
        assert tid.startswith("task_")

    def test_subscript_evaluates_to_str(self):
        # Documented invariant: Typed[...] *is* str at runtime so the
        # subscripted form works in isinstance() checks.
        assert Typed[Literal["task"]] is str
        assert Typed[str] is str

    def test_isinstance_works_with_subscripted_typed(self):
        tid = new("task")
        assert isinstance(tid, Typed[Literal["task"]])


# ---------------------------------------------------------------------------
# Pydantic v2 integration via TypeId[...]
# ---------------------------------------------------------------------------


class TestPydanticIntegration:
    def test_accepts_matching_prefix(self):
        class Task(BaseModel):
            id: TypeId[Literal["task"]]

        canonical = _fixture_typeid("task", FIXTURES[0][1])
        m = Task(id=canonical)
        # JSON round-trip emits canonical string.
        assert m.model_dump(mode="json") == {"id": canonical}

    def test_stored_value_is_canonical_str(self):
        """Validator must return canonical str, not a TypeID instance.

        Kit invariant (ADR 0001 § Wire form): a TypeID field IS the
        canonical string everywhere — python dump, json dump, bus
        payloads, logs. ``model.field`` must be a plain :class:`str`.
        """

        class Task(BaseModel):
            id: TypeId[Literal["task"]]

        canonical = _fixture_typeid("task", FIXTURES[0][1])
        m = Task(id=canonical)

        assert isinstance(m.id, str)
        assert not isinstance(m.id, TypeID)
        assert m.id == canonical

        # Both dump modes emit the bare canonical string.
        assert m.model_dump(mode="python") == {"id": canonical}
        assert m.model_dump(mode="json") == {"id": canonical}

    def test_accepts_typeid_instance_and_normalises_to_str(self):
        class Task(BaseModel):
            id: TypeId[Literal["task"]]

        canonical = _fixture_typeid("task", FIXTURES[0][1])
        m = Task(id=TypeID.from_string(canonical))
        assert isinstance(m.id, str)
        assert m.id == canonical

    def test_rejects_mismatched_prefix(self):
        class Task(BaseModel):
            id: TypeId[Literal["task"]]

        wrong = _fixture_typeid("invoice", FIXTURES[1][1])
        with pytest.raises(ValidationError):
            Task(id=wrong)

    def test_prefix_mismatch_error_is_value_error_and_id_error(self):
        """PrefixMismatchError must subclass both ValueError and IdError.

        ValueError → Pydantic surfaces it as a ValidationError.
        IdError    → kit-wide ``except IdError`` still catches it when
                     raised directly from ``parse``-style call sites.
        """
        assert issubclass(PrefixMismatchError, ValueError)
        assert issubclass(PrefixMismatchError, IdError)

    def test_rejects_invalid_string(self):
        class Task(BaseModel):
            id: TypeId[Literal["task"]]

        with pytest.raises(ValidationError):
            Task(id="task_garbage")

    def test_json_schema_marks_typeid_format(self):
        class Task(BaseModel):
            id: TypeId[Literal["task"]]

        schema = Task.model_json_schema()
        field_schema = schema["properties"]["id"]
        assert field_schema.get("type") == "string"
        assert field_schema.get("format") == "typeid"


# ---------------------------------------------------------------------------
# Error hierarchy — sanity check.
# ---------------------------------------------------------------------------


class TestErrorHierarchy:
    def test_subclasses_inherit_from_id_error(self):
        assert issubclass(InvalidPrefixError, IdError)
        assert issubclass(InvalidSuffixError, IdError)
        assert issubclass(PrefixMismatchError, IdError)
