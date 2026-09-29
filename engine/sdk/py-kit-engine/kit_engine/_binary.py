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
        raise RuntimeError(f"Unsupported platform: {platform.system()}/{platform.machine()}")
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


def _member_basename(name: str) -> str:
    return name.replace("\\", "/").rstrip("/").rsplit("/", 1)[-1]


def _zip_member_is_regular(info: zipfile.ZipInfo) -> bool:
    if info.is_dir():
        return False
    # No unix file-type bits (Windows-built archives, bare permission bits)
    # means a plain file; anything typed must be a regular file, not a link.
    file_type = stat.S_IFMT(info.external_attr >> 16)
    return file_type in (0, stat.S_IFREG)


def _extract(archive: Path, dest_dir: Path, bin_name: str) -> Path:
    """Copy only the kit binary out of ``archive`` to ``dest_dir / bin_name``.

    The first regular-file member whose basename matches ``bin_name`` is
    streamed to a path chosen here. Member names never become filesystem
    paths and link members are skipped, so a crafted archive cannot write
    outside ``dest_dir`` or smuggle in a symlink.
    """
    dest = dest_dir / bin_name
    want = bin_name.lower()
    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive) as zf:
            for info in zf.infolist():
                if _zip_member_is_regular(info) and _member_basename(info.filename).lower() == want:
                    with zf.open(info) as src, open(dest, "xb") as out:
                        shutil.copyfileobj(src, out)
                    return dest
    else:
        with tarfile.open(archive, "r:gz") as tf:
            for member in tf:
                if not member.isreg() or _member_basename(member.name).lower() != want:
                    continue
                src = tf.extractfile(member)
                if src is None:
                    break
                with src, open(dest, "xb") as out:
                    shutil.copyfileobj(src, out)
                return dest
    raise RuntimeError(f"Binary not found in archive: {bin_name}")


def _version_matches(binary: str, want: str) -> bool:
    """True if ``binary --version`` reports the same major.minor as ``want``.

    A binary that cannot be run, fails, times out or prints undecodable
    output is treated as a mismatch so the caller falls through to download.
    """
    try:
        out = subprocess.check_output([binary, "--version"], text=True, timeout=5)
    except (OSError, subprocess.SubprocessError, ValueError):
        return False
    return out.strip().lstrip("v").split(".")[:2] == want.split(".")[:2]


def find_kit_binary(version: str | None = None) -> str:
    """Find or download the kit binary. Returns path to executable."""
    ver = (version or VERSION).lstrip("v")

    found = shutil.which("kit")
    if found and _version_matches(found, ver):
        return found

    bin_dir = _bin_dir()
    bin_name = "kit.exe" if platform.system() == "Windows" else "kit"
    local_bin = bin_dir / bin_name
    if local_bin.exists() and _version_matches(str(local_bin), ver):
        return str(local_bin)

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

        src = _extract(archive_path, Path(tmp), bin_name)
        shutil.move(str(src), str(local_bin))
        if os_name != "windows":
            local_bin.chmod(local_bin.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP)

    return str(local_bin)
