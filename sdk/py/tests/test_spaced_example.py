"""The spaced example driven end to end, as the cross-language parity suite
drives it: ``examples/spaced/py/spaced.py`` run as a subprocess from
``sdk/py``.

A leaf that reads a root flag must get it from the context typer hands it.
typer>=0.26 dispatches through its vendored Click, so a lookup through the
``click`` package's context stack finds nothing and the flag reads as unset.
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
from pathlib import Path

import pytest

_SDK = Path(__file__).resolve().parents[1]
_SPACED = _SDK.parents[1] / "examples" / "spaced" / "py" / "spaced.py"
_ANSI = re.compile(r"\x1b\[[0-9;]*m")

pytestmark = pytest.mark.skipif(not _SPACED.is_file(), reason="examples/spaced is monorepo-only")


def _spaced(
    tmp_path: Path, *args: str, extra_env: dict[str, str] | None = None
) -> subprocess.CompletedProcess[str]:
    env = dict(os.environ)
    env.update(extra_env or {})
    env.update(
        HOME=str(tmp_path),
        XDG_CONFIG_HOME=str(tmp_path / "config"),
        XDG_DATA_HOME=str(tmp_path / "data"),
        XDG_STATE_HOME=str(tmp_path / "state"),
        XDG_CACHE_HOME=str(tmp_path / "cache"),
    )
    return subprocess.run(
        [sys.executable, str(_SPACED), *args],
        capture_output=True,
        text=True,
        cwd=_SDK,
        env=env,
        timeout=60,
        check=False,
    )


def test_launch_logs_info_without_quiet(tmp_path: Path) -> None:
    """Control: the INFO lines ``--quiet`` must drop are emitted by default."""
    r = _spaced(tmp_path, "launch", "starman", "--dry-run")
    assert r.returncode == 0, r.stderr
    assert "INFO" in _ANSI.sub("", r.stderr)


def test_root_quiet_suppresses_leaf_info(tmp_path: Path) -> None:
    """Root ``--quiet`` reaches the ``launch`` leaf: INFO gone, WARN kept."""
    r = _spaced(tmp_path, "--quiet", "launch", "starman", "--dry-run")
    assert r.returncode == 0, r.stderr
    stderr = _ANSI.sub("", r.stderr)
    assert "INFO" not in stderr, stderr
    assert "WARN" in stderr, stderr


def test_root_telemetry_flag_completes_by_name(tmp_path: Path) -> None:
    """``--telemetry`` is added to the root after typer builds it; typer's
    completion lists only options of the Click layer it runs on."""
    r = _spaced(
        tmp_path,
        extra_env={"_SPACED_COMPLETE": "complete_zsh", "_TYPER_COMPLETE_ARGS": "spaced --tel"},
    )
    assert r.returncode == 0, r.stderr
    assert '"--telemetry"' in r.stdout, r.stdout


def test_root_telemetry_flag_parses(tmp_path: Path) -> None:
    r = _spaced(tmp_path, "--telemetry=off", "mission", "list")
    assert r.returncode == 0, r.stderr
    assert "Starman" in r.stdout
