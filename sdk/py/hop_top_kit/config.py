"""
hop_top_kit.config — layered YAML configuration loader.

Merge order (low → high priority):
    system → user → project → env_override

Missing files are silently skipped; malformed YAML raises ``yaml.YAMLError``.

Usage::

    from hop_top_kit.config import Options, load

    cfg = {}
    load(cfg, Options(
        system_config_path="/etc/mytool/config.yaml",
        user_config_path="~/.config/mytool/config.yaml",
        project_config_path=".mytool.yaml",
        env_override=lambda d: d.update({"debug": True}),
    ))
    print(cfg["debug"])  # True
"""

from __future__ import annotations

import os
from collections.abc import Callable
from dataclasses import dataclass, field

import yaml


@dataclass
class Options:
    """Configuration options for :func:`load`.

    Attributes:
        system_config_path:  Path to the system-wide YAML config (lowest priority).
        user_config_path:    Path to the per-user YAML config.
        project_config_path: Path to the project-level YAML config (highest file priority).
        env_override:        Optional callable ``(dict) -> None`` applied after all file
                             layers.  Use it to inject environment-variable overrides.
                             The callable receives the already-merged dict and may mutate
                             it freely; its return value is ignored.
    """

    system_config_path: str = ""
    user_config_path: str = ""
    project_config_path: str = ""
    env_override: Callable[[dict], None] | None = field(default=None, repr=False)


_DEFAULT_OPTIONS = Options()


def load(dst: dict, opts: Options = _DEFAULT_OPTIONS) -> dict:
    """Load layered YAML configuration into *dst* and return it.

    Merges configuration from up to three YAML files in order of increasing
    priority (system → user → project), then calls ``opts.env_override`` if
    provided.  Each layer's keys overwrite any previously set values for the
    same key (shallow merge).

    Args:
        dst:  Target dictionary.  Modified in place *and* returned.
        opts: :class:`Options` instance.  All paths are optional; empty strings
              and non-existent paths are silently skipped.

    Returns:
        ``dst`` — the same object that was passed in.

    Raises:
        yaml.YAMLError: If any present YAML file is malformed.

    Example::

        cfg = {}
        load(cfg, Options(
            system_config_path="/etc/app/config.yaml",
            project_config_path=".app.yaml",
        ))
    """
    for path in (
        opts.system_config_path,
        opts.user_config_path,
        opts.project_config_path,
    ):
        _merge_file(dst, path)

    if opts.env_override is not None:
        opts.env_override(dst)

    return dst


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------


def _merge_file(dst: dict, path: str) -> None:
    """Parse *path* and shallow-merge its top-level keys into *dst*.

    Skips silently if *path* is empty or the file does not exist.
    Raises ``yaml.YAMLError`` for malformed YAML.
    """
    if not path:
        return

    expanded = os.path.expanduser(path)
    if not os.path.isfile(expanded):
        return

    with open(expanded, encoding="utf-8") as fh:
        data = yaml.safe_load(fh)

    if isinstance(data, dict):
        dst.update(data)
