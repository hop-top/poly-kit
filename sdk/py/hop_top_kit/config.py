"""
hop_top_kit.config — layered YAML configuration loader.

Merge order (low → high priority):
    system → user → project → env_override

Each file layer merges into the result with these rules:

- Dicts merge recursively, key by key, so a later layer only overrides the
  keys it sets (``db: {port: 2}`` keeps an earlier ``db.host``).
- Lists and scalars are replaced whole.
- A null (``~``) clears a dict or list, sets a key that is absent or already
  null, and leaves a scalar untouched.
- On a kind conflict (dict vs non-dict) the later value replaces the earlier.

These match Go's ``kit/core/config`` for struct fields and the TypeScript
``load`` defaults. Go differs where its target type differs: a ``map[...]``
entry or an ``any`` field is replaced whole rather than merged, and a value
of the wrong kind for a typed field is a decode error.

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
    provided.  Nested dicts merge key by key at every depth; lists and
    scalars are replaced; a null clears a dict or list but leaves a scalar
    (see the module docs).  *dst* is updated in place, but nested dicts
    already in it are copied before a layer merges into them, never mutated.

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
    """Parse *path* and deep-merge its mapping into *dst*.

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
        _merge_into(dst, data)


def _merge_into(dst: dict, src: dict) -> None:
    """Merge *src* into *dst* in place with the module merge rules.

    A nested dict in *dst* is replaced by a merged copy rather than mutated,
    so dicts shared through YAML aliases (or by the caller) never see a
    merge meant for one key.
    """
    for key, value in src.items():
        cur = dst.get(key)
        if value is None:
            if cur is not None and not isinstance(cur, (dict, list)):
                continue  # a null leaves a scalar untouched
            dst[key] = None
        elif isinstance(value, dict) and isinstance(cur, dict):
            merged = dict(cur)
            _merge_into(merged, value)
            dst[key] = merged
        else:
            dst[key] = value
