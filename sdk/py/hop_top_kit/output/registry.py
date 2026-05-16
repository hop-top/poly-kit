"""
hop_top_kit.output.registry — Formatter registry (mirrors Go output.Registry).

Public surface
--------------
Registry            — class with register/override/lookup/keys/formatters/extension_map
default_registry    — module-level singleton (built-ins register here)
new_registry()      — factory for an empty Registry
"""

from __future__ import annotations

import threading

from hop_top_kit.output.formatter import Formatter


class Registry:
    """Holds Formatter implementations keyed by ``Formatter.key``.

    ``register`` raises ``ValueError`` on duplicate key (Python idiom for
    "this argument can't be accepted in current state"). Adopters
    intentionally replacing a built-in must call ``override``.
    """

    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._by_key: dict[str, Formatter] = {}

    def register(self, f: Formatter) -> None:
        """Register *f*. Raises ValueError on empty key or duplicate."""
        with self._lock:
            self._validate(f)
            if f.key in self._by_key:
                raise ValueError(
                    f"output: formatter {f.key!r} already registered (use override to replace)"
                )
            self._by_key[f.key] = f

    def override(self, f: Formatter) -> None:
        """Replace (or register) the formatter for ``f.key``."""
        with self._lock:
            self._validate(f)
            self._by_key[f.key] = f

    def lookup(self, key: str) -> Formatter | None:
        """Return the formatter registered under *key*, or None."""
        with self._lock:
            return self._by_key.get(key)

    def keys(self) -> list[str]:
        """Return all registered keys, sorted for stable output."""
        with self._lock:
            return sorted(self._by_key)

    def formatters(self) -> list[Formatter]:
        """Return all registered formatters in key order."""
        with self._lock:
            return [self._by_key[k] for k in sorted(self._by_key)]

    def extension_map(self) -> dict[str, str]:
        """Return ``ext → key`` for every registered Extension.

        Iterates keys in sorted order so collision resolution is
        deterministic (later writes win, consistent with Go).
        """
        with self._lock:
            out: dict[str, str] = {}
            for k in sorted(self._by_key):
                f = self._by_key[k]
                for ext in f.extensions:
                    out[ext.lower()] = k
            return out

    # ------------------------------------------------------------------
    # Internal
    # ------------------------------------------------------------------

    @staticmethod
    def _validate(f: Formatter) -> None:
        if not isinstance(f, Formatter):
            raise ValueError(
                "output: object does not implement Formatter protocol "
                "(required attrs: key, extensions; methods: options, render)"
            )
        if not f.key:
            raise ValueError("output: formatter key is empty")


def new_registry() -> Registry:
    """Return a fresh empty Registry — useful for tests + multi-CLI binaries."""
    return Registry()


# Module-level singleton; built-ins register here at import time.
default_registry: Registry = Registry()
