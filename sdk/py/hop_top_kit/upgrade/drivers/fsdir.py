"""A schema driver for a filesystem directory: Go ``upgrade/driver/fsdir``.

The version lives in the Migrator's version file; ``set_version`` does
nothing. A backup is ``<name>.tar.gz`` inside the backup directory: gzip over
tar, member names relative to the directory (no ``./`` prefix, no trailing
slash), the directory itself as the first member ``.``, parents before
children in name order, empty directories kept. Go and Python archives
restore with either port.

Restore never trusts the archive: it refuses absolute names, ``..``
components, links that leave the directory, hard or symbolic, links whose
target climbs with ``..`` after a named component (``d/..``: ``d`` may be a
link back to the root), a member named like an earlier link, and device,
fifo or other special members. It extracts into a sibling temp directory and
swaps it in only when extraction succeeded, so a failed restore leaves the
current directory as it was. Backup runs the same check on every symbolic link
and fails, naming the path, on one restore would refuse: a backup never holds
what its own rollback cannot restore.

Python 3.11.0 to 3.11.3 have no ``tarfile`` extraction filters, and their
extraction follows links already on disk. There the driver handles no links
at all: ``backup`` raises :class:`OSError` on a symbolic link and ``restore``
raises :class:`UnsafeArchiveError` on any link member. Use 3.11.4 or later
for trees that hold links.
"""

from __future__ import annotations

import contextlib
import gzip
import os
import posixpath
import shutil
import tarfile
import tempfile
from pathlib import Path

from hop_top_kit.upgrade.versionfile import read_version_file, read_version_file_in

_ROOT_MODE = 0o750


class UnsafeArchiveError(ValueError):
    """A backup archive member that must not be extracted."""


def _parts(name: str) -> list[str]:
    seps = {"/", os.sep}
    for s in seps - {"/"}:
        name = name.replace(s, "/")
    return [p for p in name.split("/") if p not in ("", ".")]


def _escapes(path: str) -> bool:
    n = posixpath.normpath(path)
    return n == ".." or n.startswith("../") or posixpath.isabs(n)


def _bad_link_target(linkname: str) -> bool:
    """A target that is empty, absolute, or climbs with ``..`` after a named
    component. Lexically ``d/..`` is ``.``; on disk it is the parent of
    wherever ``d`` points, and ``d`` may be a link to the root."""
    if not linkname or "\x00" in linkname or os.path.isabs(linkname):
        return True
    parts = [p for p in linkname.split("/") if p not in ("", ".")]
    named = False
    for p in parts:
        if p == "..":
            if named:
                return True
        else:
            named = True
    return False


def _check_member(m: tarfile.TarInfo, symlinks: set[str]) -> None:
    """Raise :class:`UnsafeArchiveError` unless *m* is a regular file,
    directory, or a link that stays inside the directory. *symlinks* holds the
    symbolic links seen so far: no member may sit below one or bear its name."""
    name = m.name
    if not name or "\x00" in name:
        raise UnsafeArchiveError(f"path traversal blocked: {name!r}")
    if (
        name.startswith("/")
        or os.path.isabs(name)
        or (os.sep != "/" and (name.startswith(os.sep) or ":" in name.split("/", 1)[0]))
    ):
        raise UnsafeArchiveError(f"path traversal blocked: {name}")
    parts = _parts(name)
    if ".." in parts:
        raise UnsafeArchiveError(f"path traversal blocked: {name}")
    rel = "/".join(parts) or "."
    for i in range(1, len(parts)):
        if "/".join(parts[:i]) in symlinks:
            raise UnsafeArchiveError(f"path traversal blocked: {name} below a symbolic link")
    if rel in symlinks:
        raise UnsafeArchiveError(f"path traversal blocked: {name} repeats a symbolic link's name")
    if m.isdir() or m.isreg():
        return
    if m.issym():
        target = posixpath.join(posixpath.dirname(rel), m.linkname)
        if _bad_link_target(m.linkname) or _escapes(target):
            raise UnsafeArchiveError(f"link target escapes: {name} -> {m.linkname}")
        symlinks.add(rel)
        return
    if m.islnk():
        target = posixpath.normpath(m.linkname)
        if _bad_link_target(m.linkname) or _escapes(target):
            raise UnsafeArchiveError(f"link target escapes: {name} -> {m.linkname}")
        src = _parts(target)
        if any("/".join(src[:i]) in symlinks for i in range(1, len(src) + 1)):
            raise UnsafeArchiveError(f"link source below a symbolic link: {name} -> {m.linkname}")
        return
    raise UnsafeArchiveError(f"special file not allowed: {name}")


_HAS_DATA_FILTER = hasattr(tarfile, "data_filter")
_LINKS_NEED = "Python 3.11.4 or later (tarfile extraction filters)"


def _keep_dir_mode(member: tarfile.TarInfo, dest: str) -> tarfile.TarInfo | None:
    """The stdlib ``data`` filter, except that a directory keeps its recorded
    permission bits: the filter would leave them to the umask, widening a
    private directory on restore."""
    out = tarfile.data_filter(member, dest)
    if out is not None and out.isdir():
        out = out.replace(mode=member.mode & 0o777, deep=False)
    return out


def _extract(archive: Path, dest: Path) -> None:
    """Extract *archive* into the existing directory *dest*. Every member is
    checked before any is written. Python 3.12 and 3.11.4 and later also
    extract through the stdlib ``data`` filter (no setuid bits, no
    group/other write, no ownership); on 3.11.0 to 3.11.3, which have no
    filters, the same bits are cleared here and any link member is refused:
    their extraction writes through links already on disk."""
    with tarfile.open(archive, "r:gz") as tar:
        members = tar.getmembers()
        symlinks: set[str] = set()
        for m in members:
            _check_member(m, symlinks)
            if not _HAS_DATA_FILTER and (m.issym() or m.islnk()):
                raise UnsafeArchiveError(f"link member needs {_LINKS_NEED}: {m.name}")
        if _HAS_DATA_FILTER:
            tar.extractall(dest, members=members, filter=_keep_dir_mode)
            return
        for m in members:
            m.mode &= 0o777
            m.uid = m.gid = 0
            m.uname = m.gname = ""
        tar.extractall(dest, members=members)  # members checked above


def _add_tree(tar: tarfile.TarFile, root: Path) -> None:
    """Add *root* itself as ``.`` and everything below it, depth first in
    name order, as Go's ``filepath.Walk`` visits it. A symbolic link goes
    through the check restore applies to it, so a backup never holds a link its
    own restore would refuse."""
    symlinks: set[str] = set()

    def add(path: Path, arcname: str) -> None:
        info = tar.gettarinfo(str(path), arcname)
        if info is None or not (info.isdir() or info.isreg() or info.issym()):
            raise OSError(f"unsupported file type: {path}")
        if info.issym() and not _HAS_DATA_FILTER:
            raise OSError(f"symbolic link needs {_LINKS_NEED}, or restore refuses it: {path}")
        if info.issym():
            try:
                _check_member(info, symlinks)
            except UnsafeArchiveError as e:
                raise UnsafeArchiveError(f"{path}: restore would refuse this link: {e}") from e
        info.mtime = int(info.mtime)  # no pax record for a fractional time
        if info.isreg():
            with path.open("rb") as f:
                tar.addfile(info, f)
        else:
            tar.addfile(info)
        if info.isdir():
            for child in sorted(os.listdir(path)):
                add(path / child, child if arcname == "." else f"{arcname}/{child}")

    add(root, ".")


class FsDirDriver:
    """Backs up and restores one directory as ``<name>.tar.gz``.

    Args:
        name: Schema name, matched against ``Migration.schema``; also the
            archive's file name stem.
        path: The directory this schema owns.
        tool: Tool whose standard data dir holds the version file.
        data_dir: Read the version file under this dir instead; pass the
            Migrator's own ``data_dir`` when it sets one.

    A directory that does not exist is backed up as nothing, and restoring
    from such a backup raises :class:`FileNotFoundError` (as Go does).
    """

    def __init__(
        self,
        name: str,
        path: str | os.PathLike[str],
        *,
        tool: str = "",
        data_dir: str | os.PathLike[str] | None = None,
    ) -> None:
        self._name = name
        self._path = Path(path)
        self._tool = tool
        self._data_dir = data_dir

    @property
    def name(self) -> str:
        return self._name

    def version(self) -> str:
        if self._data_dir:
            return read_version_file_in(self._data_dir, self._name)
        return read_version_file(self._tool, self._name)

    def set_version(self, version: str) -> None:
        """No-op: the Migrator wrote the version file already."""

    def _archive(self, directory: Path) -> Path:
        return Path(directory) / f"{self._name}.tar.gz"

    def backup(self, dest: Path) -> None:
        dest = Path(dest)
        dest.mkdir(mode=0o750, parents=True, exist_ok=True)
        if not self._path.exists():
            return
        final = self._archive(dest)
        partial = final.with_name(final.name + ".partial")
        try:
            with (
                partial.open("wb") as raw,
                gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz,
                tarfile.open(fileobj=gz, mode="w", format=tarfile.PAX_FORMAT) as tar,
            ):
                _add_tree(tar, self._path)
            os.replace(partial, final)
        except BaseException:
            with contextlib.suppress(OSError):
                partial.unlink()
            raise

    def restore(self, src: Path) -> None:
        archive = self._archive(src)
        if not archive.is_file():
            raise FileNotFoundError(f"backup archive not found: {archive}")
        target = self._path
        parent = target.parent
        parent.mkdir(parents=True, exist_ok=True)
        stage = Path(tempfile.mkdtemp(prefix=f".{target.name}.restore-", dir=parent))
        old = Path(tempfile.mkdtemp(prefix=f".{target.name}.old-", dir=parent))
        kept = old / "kept"
        try:
            stage.chmod(_ROOT_MODE)
            _extract(archive, stage)
            if os.path.lexists(target):
                os.replace(target, kept)
            os.replace(stage, target)
        except BaseException:
            shutil.rmtree(stage, ignore_errors=True)
            if os.path.lexists(kept) and not os.path.lexists(target):
                os.replace(kept, target)  # put the current directory back
            if not os.path.lexists(kept):
                shutil.rmtree(old, ignore_errors=True)
            raise
        shutil.rmtree(old, ignore_errors=True)
