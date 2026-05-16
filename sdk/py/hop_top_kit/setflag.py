"""SetFlag — +append/-remove/=replace semantics for list-valued CLI options.

Usage with Click/Typer::

    from hop_top_kit.setflag import SetFlag

    tags = SetFlag()
    @click.option("--tag", callback=tags.click_callback, multiple=True,
                  expose_value=False)
    # --tag feat --tag +docs --tag -bug --tag =a,b --tag =
"""

from __future__ import annotations


class SetFlag:
    """Manages a list of string values with +/-/= prefix operations.

    Operations:
        val       append (default)
        +val      append (explicit)
        -val      remove
        =a,b      replace all
        =         clear all

    Comma-separated values are split. Duplicates suppressed.
    """

    def __init__(self, initial: list[str] | None = None) -> None:
        self._items: list[str] = list(initial) if initial else []

    def set(self, val: str) -> None:
        if not val:
            return

        op = val[0]
        if op == "=":
            raw = val[1:]
            self._items = _split_and_trim(raw) if raw else []
            return
        if op == "-":
            target = val[1:]
            self._items = [s for s in self._items if s != target]
            return
        if op == "+":
            val = val[1:]

        for v in _split_and_trim(val):
            if v not in self._items:
                self._items.append(v)

    def add(self, val: str) -> None:
        """Add val literally (no prefix interpretation)."""
        if val not in self._items:
            self._items.append(val)

    def remove(self, val: str) -> None:
        """Remove val literally (no prefix interpretation)."""
        self._items = [s for s in self._items if s != val]

    def clear(self) -> None:
        """Remove all items."""
        self._items = []

    def values(self) -> list[str]:
        return list(self._items)

    def __str__(self) -> str:
        return ",".join(self._items)

    def click_callback(
        self,
        ctx: object,
        param: object,
        value: str,
    ) -> list[str]:
        """Click/Typer option callback."""
        self.set(value)
        return self.values()


def _split_and_trim(s: str) -> list[str]:
    return [p.strip() for p in s.split(",") if p.strip()]
