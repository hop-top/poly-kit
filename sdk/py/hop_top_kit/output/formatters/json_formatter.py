"""json built-in formatter — stdlib json with configurable indent.

Key order mirrors the tabular formatters: ``columns`` sets it, ``cols``
reorders + selects, payload insertion order is the fallback.
"""

from __future__ import annotations

import dataclasses
import json
from typing import TYPE_CHECKING, Any, TextIO

from hop_top_kit.output.formatter import OptionSpec
from hop_top_kit.output.projection import project_payload

if TYPE_CHECKING:
    from hop_top_kit.output.formatter import ColumnSpec


class JSONFormatter:
    key = "json"
    extensions: tuple[str, ...] = (".json",)

    def options(self) -> list[OptionSpec]:
        return [
            OptionSpec(
                name="indent",
                type="int",
                default=2,
                usage="indent width in spaces (0 disables pretty-print)",
            )
        ]

    def render(
        self,
        out: TextIO,
        data: Any,
        opts: dict[str, Any],
        cols: list[str],
        columns: list[ColumnSpec] | None = None,
    ) -> None:
        indent = opts.get("indent", 2)
        if indent == 0:
            indent = None  # json.dumps None → compact
        keys = cols or [c.header for c in columns or []]
        out.write(json.dumps(_to_jsonable(project_payload(data, keys)), indent=indent))
        out.write("\n")


def _to_jsonable(v: Any) -> Any:
    """Convert dataclass instances to dicts so json.dumps doesn't crash."""
    if dataclasses.is_dataclass(v) and not isinstance(v, type):
        return dataclasses.asdict(v)
    if isinstance(v, list):
        return [_to_jsonable(item) for item in v]
    if isinstance(v, dict):
        return {k: _to_jsonable(val) for k, val in v.items()}
    return v
