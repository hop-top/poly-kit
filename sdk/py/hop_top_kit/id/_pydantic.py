"""hop_top_kit.id._pydantic — Pydantic v2 type adapter.

Builds on the upstream ``typeid.integrations.pydantic.TypeIDField`` but
translates the underlying ``TypeIDException`` family into ``ValueError``
so Pydantic surfaces a clean :class:`pydantic.ValidationError` instead of
leaking the upstream exception class.

Stored canonical form (``str(TypeID)``) matches ADR 0001 § Wire form —
JSON / bus payloads carry the ``<prefix>_<suffix>`` string, never a
struct.
"""

from __future__ import annotations

from typing import Any, Literal, get_args, get_origin

from pydantic.json_schema import JsonSchemaValue
from pydantic_core import core_schema
from typeid import TypeID
from typeid.core.errors import TypeIDException


def _validate_factory(expected_prefix: str):
    """Return a Pydantic plain-validator closed over ``expected_prefix``."""

    def _validate(v: Any) -> TypeID:
        if isinstance(v, TypeID):
            tid = v
        elif isinstance(v, str):
            try:
                tid = TypeID.from_string(v)
            except TypeIDException as exc:
                # Pydantic v2 converts ValueError → ValidationError; the
                # upstream raw exception leaks otherwise.
                raise ValueError(f"invalid typeid: {exc}") from exc
        else:
            raise ValueError(
                f"TypeID must be str or TypeID, got {type(v).__name__}",
            )

        if tid.prefix != expected_prefix:
            raise ValueError(
                f"TypeID prefix mismatch: expected '{expected_prefix}', got '{tid.prefix}'",
            )
        return tid

    return _validate


class _TypeIdBase:
    """Base for prefix-specialised Pydantic field types."""

    _expected_prefix: str = ""

    @classmethod
    def __get_pydantic_core_schema__(
        cls,
        source_type: Any,
        handler: Any,
    ) -> core_schema.CoreSchema:
        return core_schema.no_info_plain_validator_function(
            _validate_factory(cls._expected_prefix),
            json_schema_input_schema=core_schema.str_schema(),
            serialization=core_schema.plain_serializer_function_ser_schema(
                str,
                when_used="json",
            ),
        )

    @classmethod
    def __get_pydantic_json_schema__(
        cls,
        core_schema_: core_schema.CoreSchema,
        handler: Any,
    ) -> JsonSchemaValue:
        schema = handler(core_schema_.get("json_schema_input_schema", core_schema_))
        schema.update({"type": "string", "format": "typeid"})
        if cls._expected_prefix:
            schema.setdefault(
                "description",
                f"TypeID with prefix '{cls._expected_prefix}'",
            )
            schema.setdefault(
                "examples",
                [f"{cls._expected_prefix}_01hxxxxxxxxxxxxxxxxxxxxxxx"],
            )
        return schema


class TypeId:
    """Pydantic v2 type adapter for typeid-validated string fields.

    Usage::

        from pydantic import BaseModel
        from hop_top_kit.id import TypeId

        class Task(BaseModel):
            id: TypeId["task"]

    Supported subscript forms:
        - ``TypeId["task"]``               — bare string literal
        - ``TypeId[Literal["task"]]``      — typing.Literal wrapper
        - ``TypeId[("task",)]``            — one-tuple form (rare)

    Behaviour:
        - Accepts canonical ``<prefix>_<suffix>`` strings or already-parsed
          ``typeid.TypeID`` instances.
        - Raises ``pydantic.ValidationError`` on malformed strings or
          prefix mismatch.
        - Serialises to canonical string in JSON mode.
        - JSON Schema marks the field as ``{"type": "string", "format":
          "typeid"}`` for cross-tool schema consumers.
    """

    def __class_getitem__(cls, item: Any) -> type:
        if isinstance(item, tuple):
            if len(item) != 1:
                raise TypeError("TypeId[...] expects a single prefix")
            item = item[0]

        if get_origin(item) is Literal:
            args = get_args(item)
            if len(args) != 1 or not isinstance(args[0], str):
                raise TypeError(
                    "TypeId[Literal['prefix']] expects a single string literal",
                )
            prefix = args[0]
        elif isinstance(item, str):
            prefix = item
        else:
            raise TypeError(
                "TypeId[...] expects a string prefix or Literal['prefix']",
            )

        return type(
            f"TypeId_{prefix}",
            (_TypeIdBase,),
            {"_expected_prefix": prefix},
        )
