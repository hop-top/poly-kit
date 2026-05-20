"""Install-id reader/writer.

Persists 32 cryptographically random bytes at
``<XDG_STATE_HOME>/kit/telemetry/installation_id`` (perms 0600, parent 0700)
and exposes the lowercase hex SHA-256 of those bytes as the install id.

Cross-language compatible with `go/runtime/telemetry/installid.go`: both
languages write 32 raw bytes at the same path and hash them identically.
"""

from __future__ import annotations

import contextlib
import hashlib
import os
import secrets
import threading
from pathlib import Path

_ID_BYTES = 32
_PERMS_FILE = 0o600
_PERMS_DIR = 0o700

# In-process lock guards the temp-file → rename critical section so concurrent
# threads in a single Python process don't race each other. Cross-process
# races (rare in practice) are handled by the rename-failure fallback below.
_lock = threading.Lock()


def _xdg_state_home() -> Path:
    return Path(os.environ.get("XDG_STATE_HOME") or (Path.home() / ".local" / "state"))


def install_id_path() -> Path:
    """Canonical path for the persisted install id (32 raw bytes)."""
    return _xdg_state_home() / "kit" / "telemetry" / "installation_id"


def _hash(b: bytes) -> str:
    return hashlib.sha256(b).hexdigest()


def _read_existing(p: Path) -> str:
    data = p.read_bytes()
    if len(data) != _ID_BYTES:
        raise ValueError(f"install_id: file has wrong size {len(data)} bytes, expected {_ID_BYTES}")
    return _hash(data)


def get_install_id() -> str:
    """Return the lowercase hex SHA-256 of the persisted install bytes.

    Generates and persists 32 fresh random bytes on first call. Race-safe
    across concurrent first calls: last-writer-wins on bytes, and observers
    converge on whichever file survived the rename.
    """
    p = install_id_path()
    if p.exists():
        return _read_existing(p)

    with _lock:
        # Re-check inside the lock: another thread may have generated already.
        if p.exists():
            return _read_existing(p)

        p.parent.mkdir(parents=True, mode=_PERMS_DIR, exist_ok=True)
        # Best-effort tighten parent perms in case it pre-existed with looser bits.
        with contextlib.suppress(OSError):
            os.chmod(p.parent, _PERMS_DIR)

        fresh = secrets.token_bytes(_ID_BYTES)
        # Unique tmp suffix so cross-process races don't clobber each other.
        tmp = p.with_name(f"{p.name}.new.{os.getpid()}.{secrets.token_hex(4)}")
        tmp.write_bytes(fresh)
        os.chmod(tmp, _PERMS_FILE)

        try:
            os.rename(tmp, p)
        except OSError:
            # Another process beat us — observe whichever file landed.
            if p.exists():
                tmp.unlink(missing_ok=True)
                return _read_existing(p)
            raise

        return _hash(fresh)


def rotate() -> str:
    """Replace the persisted bytes with 32 fresh random bytes.

    Returns the new hex id.
    """
    p = install_id_path()
    with _lock:
        p.parent.mkdir(parents=True, mode=_PERMS_DIR, exist_ok=True)
        fresh = secrets.token_bytes(_ID_BYTES)
        tmp = p.with_name(f"{p.name}.new.{os.getpid()}.{secrets.token_hex(4)}")
        tmp.write_bytes(fresh)
        os.chmod(tmp, _PERMS_FILE)
        os.rename(tmp, p)
        return _hash(fresh)


def reset_for_test() -> None:
    """Test helper: delete the persisted install-id file (idempotent)."""
    install_id_path().unlink(missing_ok=True)
