"""
hop_top_kit.output.formatter — Formatter Protocol + OptionSpec/ColumnSpec.

Mirrors hop.top/kit/go/console/output/formatter.go.

Public surface
--------------
OptionSpec     — frozen dataclass describing one --format-opt key
ColumnSpec     — frozen dataclass describing one column (header/key/priority)
Formatter      — runtime_checkable Protocol implemented by built-ins + adopters
parse_options  — validate raw key=value pairs against specs (mirrors Go ParseOptions)

Coercion rules (parse_options)
------------------------------
- 'string' : raw value passed through.
- 'int'    : strconv-style int(); raises ValueError on parse fail.
- 'bool'   : true|false|1|0|yes|no (case-insensitive).
- 'enum'   : value must be in spec.enum.
- key-only form (no '=') is valid only for type=='bool' → True.
- unknown keys raise ValueError listing the valid set.
- defaults from specs fill in any keys not present in pairs.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass
from typing import Any, Protocol, TextIO, runtime_checkable

OptionType = str  # 'string' | 'int' | 'bool' | 'enum'

_VALID_TYPES: tuple[str, ...] = ("string", "int", "bool", "enum")


@dataclass(frozen=True)
class OptionSpec:
    """Describes one option accepted by a Formatter via --format-opt key=value."""

    name: str
    type: OptionType
    usage: str = ""
    default: Any = None
    enum: tuple[str, ...] = ()


@dataclass(frozen=True)
class ColumnSpec:
    """Describes one column of a row payload (header + lookup key + priority).

    ``header`` and ``key`` are the same name: validation and value lookup are
    one operation. Go cannot express a header/key split via ``table:""`` tags,
    so no SDK may; the redundant ``key`` field is kept for source compatibility
    and pinned to ``header`` at construction so drift is impossible.

    ``priority`` is accepted and stored but ignored by the payload SDKs; the
    hide-on-overflow behavior it drives is a Go-only feature today.
    """

    header: str
    key: str
    priority: int = 5

    def __post_init__(self) -> None:
        if self.key != self.header:
            raise ValueError(
                f"ColumnSpec: header {self.header!r} and key {self.key!r} must be "
                f"the same name (header == key is universal across SDKs)"
            )


@runtime_checkable
class Formatter(Protocol):
    """A Formatter encodes a value to a TextIO in a specific format.

    Adopters expose attributes ``key`` (str) and ``extensions`` (tuple[str, ...]),
    plus methods ``options()`` (returning a list of OptionSpec) and ``render(out,
    data, opts, cols)``. ``runtime_checkable`` lets the Registry validate at
    register() time using ``isinstance(f, Formatter)`` for clearer errors.

    ``render`` may accept an optional trailing ``columns`` argument carrying
    the caller's ColumnSpec list, which sets default column order and header
    names. Dispatch passes it only to formatters whose signature accepts it,
    so the four-argument form remains valid.
    """

    key: str
    extensions: tuple[str, ...]

    def options(self) -> list[OptionSpec]: ...

    def render(
        self,
        out: TextIO,
        data: Any,
        opts: dict[str, Any],
        cols: list[str],
        columns: list[ColumnSpec] | None = None,
    ) -> None: ...


# ---------------------------------------------------------------------------
# parse_options
# ---------------------------------------------------------------------------


def parse_options(
    pairs: Iterable[str],
    specs: Iterable[OptionSpec],
) -> dict[str, Any]:
    """Validate raw `key=value` pairs against *specs* and return the coerced map.

    Mirrors Go's ``ParseOptions``. Unknown keys, type errors, and out-of-enum
    values raise ``ValueError`` with the offending key + valid set.

    A pair without '=' is treated as bool-true; only valid for ``type='bool'``.

    Defaults from specs fill in any keys not present in *pairs*.
    """
    spec_list = list(specs)
    spec_by_name: dict[str, OptionSpec] = {s.name: s for s in spec_list}

    out: dict[str, Any] = {}
    for raw in pairs:
        if "=" in raw:
            key, val = raw.split("=", 1)
            has_eq = True
        else:
            key, val, has_eq = raw, "", False
        key = key.strip()
        if not key:
            raise ValueError(f"empty option key in {raw!r}")
        spec = spec_by_name.get(key)
        if spec is None:
            valid = ", ".join(s.name for s in spec_list)
            raise ValueError(f"unknown option {key!r} (valid: {valid})")
        if not has_eq:
            if spec.type != "bool":
                raise ValueError(f"option {key!r} requires a value (e.g. {key}=...)")
            out[key] = True
            continue
        out[key] = _coerce(spec, val)

    for s in spec_list:
        if s.name in out:
            continue
        if s.default is not None:
            out[s.name] = s.default
    return out


def _coerce(spec: OptionSpec, val: str) -> Any:
    if spec.type not in _VALID_TYPES:
        raise ValueError(f"option {spec.name!r}: unknown type {spec.type!r}")
    if spec.type == "string":
        return val
    if spec.type == "int":
        try:
            return int(val)
        except ValueError as exc:
            raise ValueError(f"option {spec.name!r}: {val!r} is not an int") from exc
    if spec.type == "bool":
        low = val.strip().lower()
        if low in ("true", "1", "yes", "t", "y"):
            return True
        if low in ("false", "0", "no", "f", "n"):
            return False
        raise ValueError(f"option {spec.name!r}: {val!r} is not a bool")
    # enum
    if val in spec.enum:
        return val
    allowed = ", ".join(spec.enum)
    raise ValueError(f"option {spec.name!r}: {val!r} not in {{{allowed}}}")
