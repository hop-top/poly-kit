"""hop_top_kit.id._pydantic — Pydantic v2 type adapter.

We do **not** delegate to the upstream
``typeid.integrations.pydantic.TypeIDField``: it validates into a
``typeid.TypeID`` instance and only serialises to a string in JSON mode,
which would force every kit adopter to special-case ``mode="python"``
dumps. Kit's invariant (per ADR 0001 § Wire form) is that a TypeID-typed
field *is* the canonical ``<prefix>_<suffix>`` string everywhere — JSON,
``model_dump()`` (python mode), bus payloads, logs.

This module builds a custom ``CoreSchema`` that:

* accepts ``str`` or ``typeid.TypeID`` input,
* validates prefix + suffix grammar against the upstream parser,
* raises :class:`hop_top_kit.id.PrefixMismatchError` (a
  :class:`ValueError` subclass — Pydantic surfaces it as a clean
  :class:`pydantic.ValidationError`) on prefix mismatch,
* returns the canonical :class:`str`, so ``model.field`` is always a
  ``str`` and ``model_dump()`` emits the bare string in both ``python``
  and ``json`` modes.
"""

from __future__ import annotations

from typing import Any, Literal, get_args, get_origin

from pydantic.json_schema import JsonSchemaValue
from pydantic_core import core_schema
from typeid import TypeID
from typeid.core.errors import TypeIDException

from ._core import PrefixMismatchError


def _validate_factory(expected_prefix: str):
    """Return a Pydantic validator closed over ``expected_prefix``.

    The validator is wired into an ``after_validator`` chained off a
    ``str_schema()``, so Pydantic has already coerced the input to
    :class:`str` (or kept it as a :class:`typeid.TypeID` via the
    ``isinstance``-branch below) before this function runs.
    """

    def _validate(v: Any) -> str:
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
            raise PrefixMismatchError(
                f"TypeID prefix mismatch: expected '{expected_prefix}', got '{tid.prefix}'",
            )
        # Return the canonical string — kit fields are str everywhere
        # (python dump, json dump, bus payloads). See module docstring.
        return str(tid)

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
        # ``no_info_after_validator_function`` chains the kit validator
        # off ``any_schema()`` (we accept TypeID instances too, not just
        # str), but ``json_schema_input_schema`` keeps the OpenAPI /
        # JSON-Schema view as ``string``.
        return core_schema.no_info_after_validator_function(
            _validate_factory(cls._expected_prefix),
            core_schema.any_schema(),
            json_schema_input_schema=core_schema.str_schema(),
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
        - Stores the canonical :class:`str` on the model (per ADR 0001 §
          Wire form): ``model.id`` is always a string, ``model_dump()``
          emits the bare string in both ``python`` and ``json`` modes.
        - Raises ``pydantic.ValidationError`` on malformed strings or
          prefix mismatch (via :class:`PrefixMismatchError`, which
          subclasses :class:`ValueError`).
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
