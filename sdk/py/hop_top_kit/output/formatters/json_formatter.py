"""json built-in formatter — stdlib json with configurable indent."""

from __future__ import annotations

import dataclasses
import json
from typing import Any, TextIO

from hop_top_kit.output.formatter import OptionSpec


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
    ) -> None:
        indent = opts.get("indent", 2)
        if indent == 0:
            indent = None  # json.dumps None → compact
        out.write(json.dumps(_to_jsonable(data), indent=indent))
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
