"""Lazy binary resolver for the kit sidecar."""

from __future__ import annotations

import hashlib
import http.client
import os
import platform
import posixpath
import shutil
import stat
import subprocess
import tarfile
import tempfile
import urllib.request
import zipfile
from pathlib import Path

REPO = "hop-top/kit"
VERSION = "0.1.0"

_CHECKSUMS_MAX_BYTES = 1 << 20


def _platform_key() -> tuple[str, str]:
    os_map = {"Darwin": "darwin", "Linux": "linux", "Windows": "windows"}
    arch_map = {
        "x86_64": "amd64",
        "AMD64": "amd64",
        "arm64": "arm64",
        "aarch64": "arm64",
    }
    os_name = os_map.get(platform.system())
    arch = arch_map.get(platform.machine())
    if not os_name or not arch:
        raise RuntimeError(
            f"Unsupported platform: {platform.system()}/{platform.machine()}"
        )
    return os_name, arch


def _bin_dir() -> Path:
    """Resolve target directory for the kit binary."""
    # Prefer virtualenv bin
    venv = os.environ.get("VIRTUAL_ENV")
    if venv:
        return Path(venv) / "bin"
    # Fallback: ~/.local/bin
    return Path.home() / ".local" / "bin"


def _fetch_checksums(version: str) -> str:
    """Download the release checksums file; raise if it cannot be read."""
    url = f"https://github.com/{REPO}/releases/download/v{version}/checksums.txt"
    try:
        with urllib.request.urlopen(url, timeout=30) as resp:
            return resp.read(_CHECKSUMS_MAX_BYTES).decode()
    except (OSError, ValueError, http.client.HTTPException) as e:
        raise RuntimeError(
            f"Failed to fetch checksums from {url}: {e}\n"
            "Refusing to install an unverified kit binary; "
            "install kit manually and add to PATH."
        ) from e


def _parse_checksum_line(line: str) -> tuple[str, str] | None:
    """Parse ``<hash>  <file>`` (GNU coreutils) or ``<hash> <file>``."""
    line = line.strip()
    if not line or line.startswith("#"):
        return None
    idx = line.find("  ")
    parts = [line[:idx], line[idx + 2 :]] if idx > 0 else line.split()
    if len(parts) != 2:
        return None
    return parts[0], parts[1]


def _find_checksum(text: str, filename: str) -> str:
    """Return the expected hash for ``filename``; raise if it has no entry."""
    for line in text.splitlines():
        parsed = _parse_checksum_line(line)
        if parsed and posixpath.basename(parsed[1]) == filename:
            return parsed[0]
    raise RuntimeError(
        f"no checksum found for {filename!r} in release checksums; "
        "refusing to install an unverified kit binary"
    )


def _verify_checksum(path: Path, expected: str) -> bool:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(8192), b""):
            h.update(chunk)
    return h.hexdigest() == expected.lower()


def _download(url: str, dest: Path) -> None:
    urllib.request.urlretrieve(url, dest)


def _extract(archive: Path, dest_dir: Path, bin_name: str) -> Path:
    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive) as zf:
            zf.extractall(dest_dir)
    else:
        with tarfile.open(archive, "r:gz") as tf:
            tf.extractall(dest_dir)
    return dest_dir / bin_name


def find_kit_binary(version: str | None = None) -> str:
    """Find or download the kit binary. Returns path to executable."""
    ver = (version or VERSION).lstrip("v")

    found = shutil.which("kit")
    if found:
        try:
            out = subprocess.check_output(
                [found, "--version"], text=True, timeout=5
            ).strip()
            found_ver = out.lstrip("v").split(".")
            want_ver = ver.split(".")
            if found_ver[:2] == want_ver[:2]:
                return found
        except Exception:
            pass

    bin_dir = _bin_dir()
    bin_name = "kit.exe" if platform.system() == "Windows" else "kit"
    local_bin = bin_dir / bin_name
    if local_bin.exists():
        try:
            out = subprocess.check_output(
                [str(local_bin), "--version"], text=True, timeout=5
            ).strip()
            found_ver = out.lstrip("v").split(".")
            want_ver = ver.split(".")
            if found_ver[:2] == want_ver[:2]:
                return str(local_bin)
        except Exception:
            pass

    os_name, arch = _platform_key()
    ext = "zip" if os_name == "windows" else "tar.gz"
    archive_name = f"kit_{os_name}_{arch}.{ext}"
    url = f"https://github.com/{REPO}/releases/download/v{ver}/{archive_name}"

    expected = _find_checksum(_fetch_checksums(ver), archive_name)

    bin_dir.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as tmp:
        archive_path = Path(tmp) / archive_name
        try:
            _download(url, archive_path)
        except Exception as e:
            raise RuntimeError(
                f"Failed to download kit binary from {url}: {e}\n"
                "Install kit manually and add to PATH."
            ) from e

        if not _verify_checksum(archive_path, expected):
            raise RuntimeError(f"Checksum mismatch for {archive_name}")

        _extract(archive_path, Path(tmp), bin_name)
        src = Path(tmp) / bin_name
        if not src.exists():
            raise RuntimeError(f"Binary not found in archive: {bin_name}")

        shutil.move(str(src), str(local_bin))
        if os_name != "windows":
            local_bin.chmod(local_bin.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP)

    return str(local_bin)
