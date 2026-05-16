"""TextFlag — +append/^prepend/=replace semantics for text-valued CLI options.

Usage with Click/Typer::

    from hop_top_kit.textflag import TextFlag

    desc = TextFlag()
    @click.option("--desc", callback=desc.click_callback)
    # --desc "replace" --desc +"append line" --desc +="inline" --desc ^"prepend"
"""

from __future__ import annotations


class TextFlag:
    """Manages a text value with +/^/= prefix operations.

    Operations:
        val       replace (default)
        =val      replace (explicit)
        +val      append on new line
        +=val     append inline
        ^val      prepend on new line
        ^=val     prepend inline
        =         clear
    """

    def __init__(self, initial: str = "") -> None:
        self._text = initial

    def set(self, val: str) -> None:
        if not val:
            return

        if val.startswith("+="):
            self._text += val[2:]
        elif val[0] == "+":
            body = val[1:]
            self._text = body if not self._text else self._text + "\n" + body
        elif val.startswith("^="):
            self._text = val[2:] + self._text
        elif val[0] == "^":
            body = val[1:]
            self._text = body if not self._text else body + "\n" + self._text
        elif val[0] == "=":
            self._text = val[1:]
        else:
            self._text = val

    def append(self, val: str) -> None:
        """Append val on new line (no prefix interpretation)."""
        self._text = val if not self._text else self._text + "\n" + val

    def append_inline(self, val: str) -> None:
        """Append val inline (no prefix interpretation)."""
        self._text += val

    def prepend(self, val: str) -> None:
        """Prepend val on new line (no prefix interpretation)."""
        self._text = val if not self._text else val + "\n" + self._text

    def prepend_inline(self, val: str) -> None:
        """Prepend val inline (no prefix interpretation)."""
        self._text = val + self._text

    def value(self) -> str:
        return self._text

    def __str__(self) -> str:
        return self._text

    def click_callback(
        self,
        ctx: object,
        param: object,
        value: str,
    ) -> str:
        """Click/Typer option callback."""
        self.set(value)
        return self.value()
