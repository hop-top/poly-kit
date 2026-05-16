"""Direct kit submodule loader -- bypasses __init__.py to avoid
heavy dependencies (tui, llm) that aren't needed for this example.
"""

from __future__ import annotations

import importlib.util
import os
import sys
import types


def _load_module(name: str, file_path: str) -> types.ModuleType:
    """Load a single .py file as a module without triggering __init__.py."""
    spec = importlib.util.spec_from_file_location(name, file_path)
    if spec is None or spec.loader is None:
        msg = f"cannot load {file_path}"
        raise ImportError(msg)
    mod = importlib.util.module_from_spec(spec)
    sys.modules[name] = mod
    spec.loader.exec_module(mod)
    return mod


_KIT_PY = os.path.join(os.path.dirname(__file__), "..", "..", "..", "py", "hop_top_kit")


def kit_config() -> types.ModuleType:
    return _load_module("_kit_config", os.path.join(_KIT_PY, "config.py"))


def kit_bus() -> types.ModuleType:
    return _load_module("_kit_bus", os.path.join(_KIT_PY, "bus.py"))


def kit_log() -> types.ModuleType:
    return _load_module("_kit_log", os.path.join(_KIT_PY, "log.py"))


def kit_progress() -> types.ModuleType:
    return _load_module("_kit_progress", os.path.join(_KIT_PY, "progress.py"))
