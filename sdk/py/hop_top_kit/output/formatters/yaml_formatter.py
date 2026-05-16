"""yaml built-in formatter — PyYAML safe_dump."""

from __future__ import annotations

import dataclasses
from typing import Any, TextIO

import yaml

from hop_top_kit.output.formatter import OptionSpec


class YAMLFormatter:
    key = "yaml"
    extensions: tuple[str, ...] = (".yaml", ".yml")

    def options(self) -> list[OptionSpec]:
        return [
            OptionSpec(
                name="default-flow-style",
                type="bool",
                default=False,
                usage="emit flow-style YAML (compact, JSON-like)",
            )
        ]

    def render(
        self,
        out: TextIO,
        data: Any,
        opts: dict[str, Any],
        cols: list[str],
    ) -> None:
        flow = opts.get("default-flow-style", False)
        out.write(
            yaml.safe_dump(
                _to_yaml_safe(data),
                default_flow_style=flow,
                sort_keys=False,
            )
        )


def _to_yaml_safe(v: Any) -> Any:
    """Recursively convert dataclass instances to dicts for safe_dump."""
    if dataclasses.is_dataclass(v) and not isinstance(v, type):
        return dataclasses.asdict(v)
    if isinstance(v, list):
        return [_to_yaml_safe(item) for item in v]
    if isinstance(v, dict):
        return {k: _to_yaml_safe(val) for k, val in v.items()}
    return v
