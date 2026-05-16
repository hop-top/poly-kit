"""hop_top_kit.progress — Factor 9: observable long-running operations.

Emits JSON lines in non-TTY (machine) mode, human-readable text
in TTY mode. Mirrors Go kit/progress design.
"""

from __future__ import annotations

import json
from dataclasses import asdict, dataclass
from typing import IO


@dataclass
class ProgressEvent:
    phase: str
    step: str
    current: int
    total: int
    percent: float
    message: str = ""


@dataclass
class JobHandle:
    id: str
    status: str  # running, completed, failed, cancelled


class ProgressReporter:
    """Emit progress events as JSON (non-TTY) or human text (TTY)."""

    def __init__(self, w: IO, is_tty: bool) -> None:
        self._w = w
        self._is_tty = is_tty

    def emit(self, event: ProgressEvent) -> None:
        if self._is_tty:
            self._w.write(
                f"[{event.phase}] {event.step}"
                f" {event.current}/{event.total}"
                f" ({event.percent}%)"
                f" {event.message}\n"
            )
        else:
            self._w.write(json.dumps(asdict(event)) + "\n")

    def done(self, message: str) -> None:
        if self._is_tty:
            self._w.write(f"done: {message}\n")
        else:
            self._w.write(json.dumps({"done": True, "message": message}) + "\n")
