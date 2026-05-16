"""errcorrect — corrective error model (12-factor AI CLI, Factor 4).

Errors carry machine-readable code, human-readable message, likely
root cause, suggested fix, and alternatives. Supports both structured
(dict/JSON) and terminal-formatted output.
"""

from __future__ import annotations

from dataclasses import asdict, dataclass, field


@dataclass
class CorrectedError(Exception):
    """Error with corrective guidance."""

    code: str
    message: str
    cause: str = ""
    fix: str = ""
    alternatives: list[str] = field(default_factory=list)
    retryable: bool = False

    def __str__(self) -> str:
        return f"{self.code}: {self.message}"

    def to_dict(self) -> dict:
        return asdict(self)

    def format_terminal(self, no_color: bool = False) -> str:
        bold = "" if no_color else "\033[1m"
        red = "" if no_color else "\033[31m"
        yellow = "" if no_color else "\033[33m"
        reset = "" if no_color else "\033[0m"

        lines = [f"{red}{bold}Error [{self.code}]{reset}: {self.message}"]

        if self.cause:
            lines.append(f"  {yellow}Cause:{reset} {self.cause}")
        if self.fix:
            lines.append(f"  {yellow}Fix:{reset} {self.fix}")
        if self.alternatives:
            lines.append(f"  {yellow}Alternatives:{reset}")
            for alt in self.alternatives:
                lines.append(f"    - {alt}")

        return "\n".join(lines)
