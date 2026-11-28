"""Run the agent-skill snippets and compare their output with the expected files.

``skills/hop-top-kit-py/assets/snippets/<name>/<name>.py`` is code the skill
tells an agent to copy. It is executed here so it cannot rot: each case runs
the file as a subprocess and compares the combined output with
``<name>.expected``. Plain pytest: no network, no Docker, only the ``dev``
extra.
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest

SDK_ROOT = Path(__file__).resolve().parents[1]
SNIPPETS = SDK_ROOT / "skills" / "hop-top-kit-py" / "assets" / "snippets"

#: Snippet name -> the argument lists it runs with, in order. Each snippet
#: runs with ``--help`` and at least one data command.
CASES: dict[str, list[list[str]]] = {
    "quickstart": [["--help"], ["list"], ["list", "--format", "json"]],
    "mcp": [["--help"], ["tools"]],
}


def _run(script: Path, args: list[str]) -> str:
    env = {
        **os.environ,
        # Pin what changes the help layout and colours across terminals.
        "COLUMNS": "100",
        "NO_COLOR": "1",
        "TERM": "dumb",
        "PYTHONPATH": os.pathsep.join(filter(None, [str(SDK_ROOT), os.environ.get("PYTHONPATH")])),
    }
    done = subprocess.run(
        [sys.executable, str(script), *args],
        capture_output=True,
        text=True,
        env=env,
        cwd=SDK_ROOT,
        timeout=60,
    )
    assert done.returncode == 0, f"{script.name} {args}: exit {done.returncode}\n{done.stderr}"
    return done.stdout


def _transcript(name: str) -> str:
    script = SNIPPETS / name / f"{name}.py"
    parts = []
    for args in CASES[name]:
        parts.append(f"$ {name}.py {' '.join(args)}\n{_run(script, args)}")
    return "\n".join(parts)


def test_every_snippet_directory_has_a_case():
    on_disk = sorted(p.name for p in SNIPPETS.iterdir() if p.is_dir())
    assert on_disk == sorted(CASES), "a snippet no test runs, or a case without a snippet"


@pytest.mark.parametrize("name", sorted(CASES))
def test_snippet_output_matches_expected(name: str):
    expected = (SNIPPETS / name / f"{name}.expected").read_text()
    assert _transcript(name) == expected
