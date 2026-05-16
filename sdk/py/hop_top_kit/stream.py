"""hop_top_kit.stream — Factor 3: stream and exit discipline.

Mirrors Go kit/stream design. Structured data goes to stdout,
human-readable logs/progress to stderr. Exit codes follow the
12-factor AI CLI spec.
"""

from __future__ import annotations

import sys
from dataclasses import dataclass
from enum import IntEnum
from typing import IO


class ExitCode(IntEnum):
    OK = 0
    ERROR = 1
    USAGE = 2
    NOT_FOUND = 3
    CONFLICT = 4
    AUTH = 5
    PERMISSION = 6
    TIMEOUT = 7
    CANCELLED = 8


@dataclass
class StreamWriter:
    """Dual-stream writer: data (stdout) for structured output,
    human (stderr) for logs and progress."""

    data: IO  # stdout — structured output
    human: IO  # stderr — logs, progress
    is_tty: bool


def create_stream_writer() -> StreamWriter:
    """Create a StreamWriter wired to stdout/stderr."""
    return StreamWriter(
        data=sys.stdout,
        human=sys.stderr,
        is_tty=sys.stderr.isatty(),
    )
